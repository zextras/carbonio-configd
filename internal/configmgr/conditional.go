// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/redact"
)

// CheckConditional evaluates a conditional expression based on configuration
// type and key: the value is truthy unless it is a legacy false value, a "!"
// prefix on the key negates the result, and a failed lookup yields the
// negation flag alone.
func (cm *ConfigManager) CheckConditional(ctx context.Context, cfgType, key string) (bool, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	negate := false
	originalKey := key

	if rest, ok := strings.CutPrefix(key, "!"); ok {
		negate = true
		key = rest
	}

	value, err := cm.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		// If key not found, treat as false for conditionals, then apply negate.
		logger.DebugContext(ctx, "LookUpConfig for conditional failed",
			"error", err)

		return negate, nil
	}

	rvalue := config.IsTrueValue(value)
	if negate {
		rvalue = !rvalue
	}

	logger.DebugContext(ctx, "Checking conditional result",
		"original_key", originalKey,
		"type", cfgType,
		"value", redact.ForLog(key, value),
		"return", rvalue)

	return rvalue, nil
}
