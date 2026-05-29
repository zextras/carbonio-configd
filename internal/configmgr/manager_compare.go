// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// CheckConditional delegates to the embedded Comparator.
//
// Deprecated: use ConfigManager.Comparator() for new code.
func (cm *ConfigManager) CheckConditional(ctx context.Context, cfgType, key string) (bool, error) {
	if cm.comparator == nil {
		cm.comparator = NewComparator(cm, cm.State, cm.ServiceMgr)
	}

	return cm.comparator.CheckConditional(ctx, cfgType, key)
}

// CompareKeys delegates to the embedded Comparator.
func (cm *ConfigManager) CompareKeys(ctx context.Context) error {
	if cm.comparator == nil {
		cm.comparator = NewComparator(cm, cm.State, cm.ServiceMgr)
	}

	return cm.comparator.CompareKeys(ctx)
}

// processConditionals recursively evaluates and processes conditional blocks.
func (cm *ConfigManager) processConditionals(ctx context.Context, conditionals []config.Conditional) {
	for _, cond := range conditionals {
		// Evaluate the conditional
		shouldProcess, err := cm.CheckConditional(ctx, cond.Type, cond.Key)
		if err != nil {
			logger.DebugContext(ctx, "Error evaluating conditional",
				"type", cond.Type,
				"key", cond.Key,
				"error", err)

			continue
		}

		// Handle negation
		if cond.Negated {
			shouldProcess = !shouldProcess
		}

		if !shouldProcess {
			logger.DebugContext(ctx, "Conditional evaluated to false, skipping",
				"type", cond.Type,
				"key", cond.Key)

			continue
		}

		logger.DebugContext(ctx, "Conditional evaluated to true, processing directives",
			"type", cond.Type,
			"key", cond.Key)

		// Process directives in this conditional block
		for postconfKey, postconfVal := range cond.Postconf {
			cm.State.CurPostconf(ctx, postconfKey, postconfVal)
		}

		for postconfdKey, postconfdVal := range cond.Postconfd {
			cm.State.CurPostconfd(ctx, postconfdKey, postconfdVal)
		}

		for ldapKey, ldapVal := range cond.Ldap {
			cm.State.CurLdap(ctx, ldapKey, ldapVal)
		}

		// Process nested conditionals recursively
		if len(cond.Nested) > 0 {
			cm.processConditionals(ctx, cond.Nested)
		}
	}
}
