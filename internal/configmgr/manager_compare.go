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
	return cm.comparator.CheckConditional(ctx, cfgType, key)
}

// CompareKeys delegates to the embedded Comparator.
func (cm *ConfigManager) CompareKeys(ctx context.Context) error {
	return cm.comparator.CompareKeys(ctx)
}

// processConditionals evaluates conditional blocks and applies the POSTCONF and
// POSTCONFD directives of those that hold (the only ones legacy honours).
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
			cm.State.SetPostconf(ctx, postconfKey, postconfVal)
		}

		for postconfdKey, postconfdVal := range cond.Postconfd {
			cm.State.SetPostconfd(ctx, postconfdKey, postconfdVal)
		}
	}
}
