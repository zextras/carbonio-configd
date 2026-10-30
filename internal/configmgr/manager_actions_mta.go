// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/mtaops"
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

func (cm *ConfigManager) doPostconf(ctx context.Context) error {
	if len(cm.State.CurrentActions.Postconf) == 0 {
		return nil
	}

	logger.DebugContext(ctx, "Executing postconf commands")

	// Collect all operations first for batch execution
	ops := make([]mtaops.PostconfOperation, 0, len(cm.State.CurrentActions.Postconf))
	specs := make(map[string]string, len(cm.State.CurrentActions.Postconf))
	// pending holds the directives that must be retried next cycle.
	pending := make(map[string]string)

	var errs []error

	for key, valueSpec := range cm.State.CurrentActions.Postconf {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "Postconf operations cancelled by shutdown signal")

			if len(errs) > 0 {
				return errors.Join(errs...)
			}

			return nil
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, key, valueSpec)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve postconf value",
				"key", key,
				"error", err)
			errs = append(errs, err)
			pending[key] = valueSpec

			continue
		}

		specs[key] = valueSpec
		ops = append(ops, mtaops.PostconfOperation{
			Key:   key,
			Value: normalizePostconfBool(valueSpec, resolvedValue),
		})
	}

	errs = append(errs, cm.executePostconfOps(ctx, ops, specs, pending)...)

	// Only successfully applied directives are cleared; the rest stay pending
	// so the next cycle retries them (legacy clears only after a clean pass).
	cm.State.ClearPostconf()

	for key, valueSpec := range pending {
		cm.State.SetPostconf(ctx, key, valueSpec)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
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

func (cm *ConfigManager) doPostconfd(ctx context.Context) error {
	if len(cm.State.CurrentActions.Postconfd) == 0 {
		return nil
	}

	logger.DebugContext(ctx, "Executing postconfd commands")

	// Collect all postconfd operations for batch execution
	ops := make([]mtaops.PostconfdOperation, 0, len(cm.State.CurrentActions.Postconfd))

	for key := range cm.State.CurrentActions.Postconfd {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "Postconfd operations cancelled by shutdown signal")

			return nil
		}

		ops = append(ops, mtaops.PostconfdOperation{
			Key: key,
		})
	}

	// Execute all operations in a single batch. Only clear pending postconfd
	// state on success; a failed batch leaves the directives intact so the
	// next cycle retries them, mirroring doLdap's retry-on-failure semantics.
	if len(ops) > 0 {
		if err := cm.mtaExecutor.ExecutePostconfdBatch(ctx, ops); err != nil {
			logger.ErrorContext(ctx, "Failed to execute postconfd batch",
				"error", err)

			return err
		}

		logger.DebugContext(ctx, "Successfully executed postconfd batch",
			"operation_count", len(ops))
		cm.State.ClearPostconfd()
	}

	return nil
}

func (cm *ConfigManager) doLdap(ctx context.Context) error {
	if len(cm.State.CurrentActions.Ldap) == 0 {
		return nil
	}

	logger.DebugContext(ctx, "Processing LDAP attributes",
		"attribute_count", len(cm.State.CurrentActions.Ldap))

	var errs []error

	// Resolve and execute each LDAP directive
	for key, valueSpec := range cm.State.CurrentActions.Ldap {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "LDAP operations cancelled by shutdown signal")

			if len(errs) > 0 {
				return errors.Join(errs...)
			}

			return nil
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, key, valueSpec)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve LDAP value",
				"key", key,
				"error", err)
			errs = append(errs, err)

			continue
		}

		// Create the operation
		op := mtaops.LdapOperation{
			Key:   key,
			Value: resolvedValue,
		}

		// Execute the operation
		if err := cm.mtaExecutor.ExecuteLdapWrite(ctx, op); err != nil {
			logger.ErrorContext(ctx, "Failed to execute LDAP write",
				"key", key,
				"value", redact.ForLog(key, op.Value),
				"error", err)
			errs = append(errs, err)
		} else {
			logger.DebugContext(ctx, "Successfully executed LDAP write",
				"key", key,
				"value", redact.ForLog(key, op.Value))
			cm.State.DelLdap(key)
		}
	}

	logger.DebugContext(ctx, "LDAP operations complete")

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// doMapfileSection processes MAPFILE/MAPLOCAL operations for a single MTA
// config section. It returns the slice of errors encountered while executing
// individual mapfile operations.
func (cm *ConfigManager) doMapfileSection(ctx context.Context, section *config.MtaConfigSection) []error {
	if !section.Changed && !cm.State.IsFirstRun() {
		return nil
	}

	var errs []error

	for varName, varType := range section.RequiredVars {
		if varType != configTypeMAPFILE && varType != configTypeMAPLOCAL {
			continue
		}

		op := mtaops.MapfileOperation{
			Key:     strings.TrimPrefix(varName, "!"),
			IsLocal: varType == configTypeMAPLOCAL,
		}

		if !op.IsLocal {
			// Without the configured value the executor would treat the key as
			// unset and delete the mapped file.
			op.Base64Data = cm.mappedFileBase64(op.Key)
		}

		if err := cm.mtaExecutor.ExecuteMapfile(ctx, op); err != nil {
			logger.ErrorContext(ctx, "Failed to execute MAPFILE",
				"var_name", varName,
				"error", err)
			errs = append(errs, err)

			continue
		}

		logger.DebugContext(ctx, "Successfully executed MAPFILE", "var_name", varName)
	}

	return errs
}

func (cm *ConfigManager) doMapfile(ctx context.Context) error {
	// MAPFILE operations are tracked in RequiredVars as type "MAPFILE" or
	// "MAPLOCAL". We need to check for changed sections and write the
	// corresponding files.
	logger.DebugContext(ctx, "Checking for MAPFILE operations")

	var errs []error

	for _, section := range cm.State.MtaConfig.Sections {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "Mapfile operations cancelled by shutdown signal")

			if len(errs) > 0 {
				return errors.Join(errs...)
			}

			return nil
		}

		errs = append(errs, cm.doMapfileSection(ctx, section)...)
	}

	logger.DebugContext(ctx, "MAPFILE operations complete")

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// SyncMappedFile brings the file behind a MAPFILE key in line with its
// configured value, as legacy lookUpConfig(MAPFILE) does on every lookup: the
// file is written when missing or different and removed when the value is
// unset. It never reports a change, so a restored file does not restart anything.
func (cm *ConfigManager) SyncMappedFile(ctx context.Context, key string) error {
	return cm.mtaExecutor.ExecuteMapfile(ctx, mtaops.MapfileOperation{
		Key:        key,
		Base64Data: cm.mappedFileBase64(key),
	})
}

// mappedFileBase64 returns the configured value of a MAPFILE key base64-encoded,
// as MapfileOperation expects. zmprov text output carries binary attributes as
// "attr:: <base64>" and the loader keeps that payload verbatim (legacy
// lookUpConfig b64decodes it), while the native LDAP client returns the raw
// bytes (e.g. the PEM of zimbraSSLDHParam), which must be encoded first. Raw PEM
// is never valid base64 ('-', newlines), so the two forms cannot be confused.
func (cm *ConfigManager) mappedFileBase64(key string) string {
	val, _ := cm.lookupVarKey(key)
	if val == "" {
		return ""
	}

	if _, err := base64.StdEncoding.DecodeString(val); err == nil {
		return val
	}

	return base64.StdEncoding.EncodeToString([]byte(val))
}
