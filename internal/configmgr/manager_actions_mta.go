// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/mtaops"
)

func (cm *ConfigManager) doPostconf(ctx context.Context) {
	if len(cm.State.CurrentActions.Postconf) == 0 {
		return
	}

	logger.DebugContext(ctx, "Executing postconf commands")

	// Collect all operations first for batch execution
	ops := make([]mtaops.PostconfOperation, 0, len(cm.State.CurrentActions.Postconf))

	for key, valueSpec := range cm.State.CurrentActions.Postconf {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Postconf operations cancelled by shutdown signal")

			return
		default:
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, key, valueSpec)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve postconf value",
				"key", key,
				"error", err)

			continue
		}

		// Add to batch
		ops = append(ops, mtaops.PostconfOperation{
			Key:   key,
			Value: resolvedValue,
		})
	}

	// Execute all operations in a single batch
	if len(ops) > 0 {
		if err := cm.mtaExecutor.ExecutePostconfBatch(ctx, ops); err != nil {
			logger.ErrorContext(ctx, "Failed to execute postconf batch",
				"error", err)
		} else {
			logger.DebugContext(ctx, "Successfully executed postconf batch",
				"operation_count", len(ops))
		}
	}

	cm.State.ClearPostconf()
}

func (cm *ConfigManager) doPostconfd(ctx context.Context) {
	if len(cm.State.CurrentActions.Postconfd) == 0 {
		return
	}

	logger.DebugContext(ctx, "Executing postconfd commands")

	// Collect all postconfd operations for batch execution
	ops := make([]mtaops.PostconfdOperation, 0, len(cm.State.CurrentActions.Postconfd))

	for key := range cm.State.CurrentActions.Postconfd {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Postconfd operations cancelled by shutdown signal")

			return
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
		} else {
			logger.DebugContext(ctx, "Successfully executed postconfd batch",
				"operation_count", len(ops))
		}
	}

	cm.State.ClearPostconfd()
}

func (cm *ConfigManager) doLdap(ctx context.Context) {
	if len(cm.State.CurrentActions.Ldap) == 0 {
		return
	}

	logger.DebugContext(ctx, "Processing LDAP attributes",
		"attribute_count", len(cm.State.CurrentActions.Ldap))

	// Resolve and execute each LDAP directive
	for key, valueSpec := range cm.State.CurrentActions.Ldap {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "LDAP operations cancelled by shutdown signal")

			return
		default:
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, key, valueSpec)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve LDAP value",
				"key", key,
				"error", err)

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
		} else {
			logger.DebugContext(ctx, "Successfully executed LDAP write",
				"key", key,
				"value", op.Value)
			cm.State.DelLdap(key)
		}
	}

	logger.DebugContext(ctx, "LDAP operations complete")
}

func (cm *ConfigManager) doMapfile(ctx context.Context) {
	// MAPFILE operations are tracked in RequiredVars as type "MAPFILE" or "MAPLOCAL"
	// We need to check for changed MAPFILE/MAPLOCAL variables and write them to files
	logger.DebugContext(ctx, "Checking for MAPFILE operations")

	for _, section := range cm.State.MtaConfig.Sections {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Mapfile operations cancelled by shutdown signal")

			return
		default:
		}

		if !section.Changed && !cm.State.FirstRun {
			continue
		}

		for varName, varType := range section.RequiredVars {
			if varType != configTypeMAPFILE && varType != configTypeMAPLOCAL {
				continue
			}

			isLocal := (varType == "MAPLOCAL")

			// Create the operation
			op := mtaops.MapfileOperation{
				Key:     varName,
				IsLocal: isLocal,
			}

			// Execute the operation (fetches from LDAP, decodes, writes file)
			if err := cm.mtaExecutor.ExecuteMapfile(ctx, op); err != nil {
				logger.ErrorContext(ctx, "Failed to execute MAPFILE",
					"var_name", varName,
					"error", err)
			} else {
				logger.DebugContext(ctx, "Successfully executed MAPFILE",
					"var_name", varName)
			}
		}
	}

	logger.DebugContext(ctx, "MAPFILE operations complete")
}
