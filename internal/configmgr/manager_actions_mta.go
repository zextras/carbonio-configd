// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// normalizePostconfBool mirrors the legacy Jython zmconfigd (jylibs/mtaconfig.py):
// POSTCONF values resolved from a typed lookup (VAR/LOCAL/FILE/MAPLOCAL) are
// normalized to Postfix booleans — "TRUE" -> "yes", "FALSE" -> "no" —
// case-insensitively. Literal POSTCONF values are passed through unchanged, so
// a literal "TRUE" stays "TRUE" exactly as the legacy implementation did.
func normalizePostconfBool(valueSpec, resolved string) string {
	if valueType, _ := parseValueSpec(valueSpec); valueType == configTypeLITERAL {
		return resolved
	}

	switch strings.ToUpper(resolved) {
	case constTRUE:
		return constYes
	case constFALSE:
		return constNo
	default:
		return resolved
	}
}

// cancelled reports whether ctx has been cancelled, without blocking.
func cancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// doPostconf applies the postconf settings of the plan and returns the ones
// that must be retried next cycle.
func (cm *ConfigManager) doPostconf(ctx context.Context, entries []plan.Postconf) ([]plan.Postconf, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	logger.DebugContext(ctx, "Executing postconf commands")

	// Collect all operations first for batch execution
	ops := make([]mtaops.PostconfOperation, 0, len(entries))
	specs := make(map[string]string, len(entries))
	// retry holds the directives that must be retried next cycle.
	retry := make(map[string]string)

	var errs []error

	for _, e := range entries {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "Postconf operations cancelled by shutdown signal")

			// Nothing was applied yet: everything stays pending.
			return entries, errors.Join(errs...)
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, e.Key, e.Value)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve postconf value",
				"key", e.Key,
				"error", err)
			errs = append(errs, err)
			retry[e.Key] = e.Value

			continue
		}

		specs[e.Key] = e.Value
		ops = append(ops, mtaops.PostconfOperation{
			Key:   e.Key,
			Value: normalizePostconfBool(e.Value, resolvedValue),
		})
	}

	errs = append(errs, cm.executePostconfOps(ctx, ops, specs, retry)...)

	// Only successfully applied directives are cleared; the rest stay pending
	// so the next cycle retries them (legacy clears only after a clean pass).
	var pending []plan.Postconf

	for _, e := range entries {
		if _, failed := retry[e.Key]; failed {
			pending = append(pending, e)
		}
	}

	return pending, errors.Join(errs...)
}

// executePostconfOps applies ops in one postconf call. When the batch fails it
// falls back to one call per key, so a single bad directive cannot block the
// others; keys that still fail are recorded in pending.
func (cm *ConfigManager) executePostconfOps(
	ctx context.Context,
	ops []mtaops.PostconfOperation,
	specs, pending map[string]string,
) []error {
	if len(ops) == 0 {
		return nil
	}

	batchErr := cm.mtaExecutor.ExecutePostconfBatch(ctx, ops)
	if batchErr == nil {
		logger.DebugContext(ctx, "Successfully executed postconf batch",
			"operation_count", len(ops))

		return nil
	}

	logger.ErrorContext(ctx, "Failed to execute postconf batch, retrying per key",
		"error", batchErr)

	var errs []error

	for _, op := range ops {
		if err := cm.mtaExecutor.ExecutePostconf(ctx, op); err != nil {
			logger.ErrorContext(ctx, "Failed to execute postconf",
				"key", op.Key,
				"error", err)
			errs = append(errs, err)
			pending[op.Key] = specs[op.Key]
		}
	}

	return errs
}

// doPostconfd removes the postconfd keys of the plan in one batch. A failed
// (or cancelled) batch leaves every directive pending for the next cycle.
func (cm *ConfigManager) doPostconfd(ctx context.Context, entries []plan.Postconfd) ([]plan.Postconfd, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	logger.DebugContext(ctx, "Executing postconfd commands")

	// Collect all postconfd operations for batch execution
	ops := make([]mtaops.PostconfdOperation, 0, len(entries))

	for _, e := range entries {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "Postconfd operations cancelled by shutdown signal")

			return entries, nil
		}

		ops = append(ops, mtaops.PostconfdOperation{Key: e.Key})
	}

	if err := cm.mtaExecutor.ExecutePostconfdBatch(ctx, ops); err != nil {
		logger.ErrorContext(ctx, "Failed to execute postconfd batch",
			"error", err)

		return entries, err
	}

	logger.DebugContext(ctx, "Successfully executed postconfd batch",
		"operation_count", len(ops))

	return nil, nil
}

// doLdap writes the LDAP attributes of the plan and returns the ones that must
// be retried next cycle.
func (cm *ConfigManager) doLdap(ctx context.Context, entries []plan.Ldap) ([]plan.Ldap, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	logger.DebugContext(ctx, "Processing LDAP attributes",
		"attribute_count", len(entries))

	var (
		errs    []error
		pending []plan.Ldap
	)

	// Resolve and execute each LDAP directive
	for i, e := range entries {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "LDAP operations cancelled by shutdown signal")

			return append(pending, entries[i:]...), errors.Join(errs...)
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, e.Key, e.Value)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve LDAP value",
				"key", e.Key,
				"error", err)
			errs = append(errs, err)
			pending = append(pending, e)

			continue
		}

		op := mtaops.LdapOperation{Key: e.Key, Value: resolvedValue}

		if err := cm.mtaExecutor.ExecuteLdapWrite(ctx, op); err != nil {
			logger.ErrorContext(ctx, "Failed to execute LDAP write",
				"key", e.Key,
				"value", redact.ForLog(e.Key, op.Value),
				"error", err)
			errs = append(errs, err)
			pending = append(pending, e)

			continue
		}

		logger.DebugContext(ctx, "Successfully executed LDAP write",
			"key", e.Key,
			"value", redact.ForLog(e.Key, op.Value))
	}

	logger.DebugContext(ctx, "LDAP operations complete")

	return pending, errors.Join(errs...)
}

// doMapfile syncs the mapped files of the plan serially. Failures are
// reported but not retried: only sections that changed (or the first run)
// carry them, as before.
func (cm *ConfigManager) doMapfile(ctx context.Context, entries []plan.Mapfile) error {
	logger.DebugContext(ctx, "Checking for MAPFILE operations")

	var errs []error

	for _, m := range entries {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "Mapfile operations cancelled by shutdown signal")

			return errors.Join(errs...)
		}

		if err := cm.syncMapfile(ctx, m); err != nil {
			logger.ErrorContext(ctx, "Failed to execute MAPFILE",
				"var_name", m.Key,
				"error", err)
			errs = append(errs, err)

			continue
		}

		logger.DebugContext(ctx, "Successfully executed MAPFILE", "var_name", m.Key)
	}

	logger.DebugContext(ctx, "MAPFILE operations complete")

	return errors.Join(errs...)
}

// healMapfiles restores the mapped files behind the MAPFILE variables of the
// compared sections, as legacy lookUpConfig(MAPFILE) does on every lookup: a
// file is written when missing or different and removed when the value is
// unset. It never reports a change, so a restored file does not restart
// anything; failures are only logged.
func (cm *ConfigManager) healMapfiles(ctx context.Context, entries []plan.Mapfile) {
	for _, m := range entries {
		if err := cm.syncMapfile(ctx, m); err != nil {
			logger.ErrorContext(ctx, "Failed to sync mapped file", "key", m.Key, "error", err)
		}
	}
}

// syncMapfile executes one mapped-file operation with the content configured
// in the current snapshot.
func (cm *ConfigManager) syncMapfile(ctx context.Context, m plan.Mapfile) error {
	op := mtaops.MapfileOperation{Key: m.Key, IsLocal: m.Local}

	if !m.Local {
		// Without the configured value the executor would treat the key as
		// unset and delete the mapped file.
		op.Base64Data = cm.mappedFileBase64(m.Key)
	}

	return cm.mtaExecutor.ExecuteMapfile(ctx, op)
}

// mappedFileBase64 returns the configured value of a MAPFILE key base64-encoded,
// as MapfileOperation expects. zmprov text output carries binary attributes as
// "attr:: <base64>" and the loader keeps that payload verbatim (legacy
// lookUpConfig b64decodes it), while the native LDAP client returns the raw
// bytes (e.g. the PEM of zimbraSSLDHParam), which must be encoded first. Raw PEM
// is never valid base64 ('-', newlines), so the two forms cannot be confused.
func (cm *ConfigManager) mappedFileBase64(key string) string {
	val, _ := cm.State.Snapshot().Var(key)
	if val == "" {
		return ""
	}

	if _, err := base64.StdEncoding.DecodeString(val); err == nil {
		return val
	}

	return base64.StdEncoding.EncodeToString([]byte(val))
}
