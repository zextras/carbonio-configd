// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/mtaops"
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
		return "yes"
	case constFALSE:
		return "no"
	default:
		return resolved
	}
}

func (cm *ConfigManager) doPostconf(ctx context.Context) error {
	if len(cm.State.CurrentActions.Postconf) == 0 {
		return nil
	}

	logger.DebugContext(ctx, "Executing postconf commands")

	// Collect all operations first for batch execution
	ops := make([]mtaops.PostconfOperation, 0, len(cm.State.CurrentActions.Postconf))

	var errs []error

	for key, valueSpec := range cm.State.CurrentActions.Postconf {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Postconf operations cancelled by shutdown signal")

			if len(errs) > 0 {
				return errors.Join(errs...)
			}

			return nil
		default:
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, key, valueSpec)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve postconf value",
				"key", key,
				"error", err)
			errs = append(errs, err)

			continue
		}

		// Add to batch
		ops = append(ops, mtaops.PostconfOperation{
			Key:   key,
			Value: normalizePostconfBool(valueSpec, resolvedValue),
		})
	}

	// Execute all operations in a single batch
	if len(ops) > 0 {
		if err := cm.mtaExecutor.ExecutePostconfBatch(ctx, ops); err != nil {
			logger.ErrorContext(ctx, "Failed to execute postconf batch",
				"error", err)
			errs = append(errs, err)
		} else {
			logger.DebugContext(ctx, "Successfully executed postconf batch",
				"operation_count", len(ops))
		}
	}

	cm.State.ClearPostconf()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (cm *ConfigManager) doPostconfd(ctx context.Context) error {
	if len(cm.State.CurrentActions.Postconfd) == 0 {
		return nil
	}

	logger.DebugContext(ctx, "Executing postconfd commands")

	// Collect all postconfd operations for batch execution
	ops := make([]mtaops.PostconfdOperation, 0, len(cm.State.CurrentActions.Postconfd))

	for key := range cm.State.CurrentActions.Postconfd {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Postconfd operations cancelled by shutdown signal")

			return nil
		default:
		}

		ops = append(ops, mtaops.PostconfdOperation{
			Key: key,
		})
	}

	// Execute all operations in a single batch
	if len(ops) > 0 {
		if err := cm.mtaExecutor.ExecutePostconfdBatch(ctx, ops); err != nil {
			logger.ErrorContext(ctx, "Failed to execute postconfd batch",
				"error", err)
			cm.State.ClearPostconfd()

			return err
		}

		logger.DebugContext(ctx, "Successfully executed postconfd batch",
			"operation_count", len(ops))
	}

	cm.State.ClearPostconfd()

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
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "LDAP operations cancelled by shutdown signal")

			if len(errs) > 0 {
				return errors.Join(errs...)
			}

			return nil
		default:
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
				"value", op.Value,
				"error", err)
			errs = append(errs, err)
		} else {
			logger.DebugContext(ctx, "Successfully executed LDAP write",
				"key", key,
				"value", op.Value)
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
	if !section.Changed && !cm.State.FirstRun {
		return nil
	}

	var errs []error

	for varName, varType := range section.RequiredVars {
		if varType != configTypeMAPFILE && varType != configTypeMAPLOCAL {
			continue
		}

		op := mtaops.MapfileOperation{
			Key:     varName,
			IsLocal: varType == configTypeMAPLOCAL,
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
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Mapfile operations cancelled by shutdown signal")

			if len(errs) > 0 {
				return errors.Join(errs...)
			}

			return nil
		default:
		}

		errs = append(errs, cm.doMapfileSection(ctx, section)...)
	}

	logger.DebugContext(ctx, "MAPFILE operations complete")

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
