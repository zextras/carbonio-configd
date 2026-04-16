// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/state"
)

// CheckConditional evaluates a conditional expression based on configuration type and key.
func (cm *ConfigManager) CheckConditional(ctx context.Context, cfgType, key string) (bool, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	negate := false
	originalKey := key

	logger.DebugContext(ctx, "Conditional entry",
		"key", key,
		"type", cfgType)

	if strings.HasPrefix(key, "!") {
		negate = true
		key = strings.TrimPrefix(key, "!")
	}

	logger.DebugContext(ctx, "Conditional after negate check",
		"key", key,
		"type", cfgType,
		"negate", negate)

	value, err := cm.LookUpConfig(ctx, cfgType, key)
	if err != nil {
		// If key not found, treat as false for conditionals, unless it's a real error
		logger.DebugContext(ctx, "LookUpConfig for conditional failed",
			"error", err)

		return negate, nil // If lookup fails, it's effectively false, then apply negate
	}

	logger.DebugContext(ctx, "Conditional after lookUpConfig",
		"key", key,
		"value", value,
		"type", cfgType,
		"negate", negate)

	isFalse := state.IsFalseValue(value)
	rvalue := !isFalse

	if negate {
		rvalue = !rvalue
	}

	logger.DebugContext(ctx, "Checking conditional result",
		"original_key", originalKey,
		"type", cfgType,
		"value", value,
		"return", rvalue)

	return rvalue, nil
}

// CompareKeys compares current configuration values with previous ones and updates the state.
func (cm *ConfigManager) CompareKeys(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Comparing keys")

	if err := cm.checkAllServicesEnabled(ctx); err != nil {
		return err
	}

	cm.compareSectionKeys(ctx)
	cm.applyServiceStatusChanges(ctx)
	cm.trackNewlyEnabledServices(ctx)

	logger.DebugContext(ctx, "Key comparison complete")

	return nil
}

// checkAllServicesEnabled returns an error when every known service is disabled
// (mirrors the Jython "all services detected disabled" guard).
func (cm *ConfigManager) checkAllServicesEnabled(ctx context.Context) error {
	stoppedServices := 0
	totalServices := 0

	for service := range cm.State.CurrentActions.Services {
		totalServices++

		isRunning, err := cm.LookUpConfig(ctx, "SERVICE", service)
		if err != nil || state.IsFalseValue(isRunning) {
			stoppedServices++
		}
	}

	if totalServices > 1 && stoppedServices == totalServices {
		return fmt.Errorf("all services detected disabled")
	}

	return nil
}

// compareSectionKeys iterates over MTA config sections and records which keys have changed.
func (cm *ConfigManager) compareSectionKeys(ctx context.Context) {
	for sn, section := range cm.State.MtaConfig.Sections {
		logger.DebugContext(ctx, "Checking keys for section", "section", sn)

		if cm.isSectionSkipped(ctx, sn) {
			continue
		}

		section.Changed = false

		cm.State.ResetChangedKeys(sn)

		for key, cfgType := range section.RequiredVars {
			cm.compareOneKey(ctx, sn, section, key, cfgType)
		}
	}
}

// isSectionSkipped returns true when forced-config mode is active and sn is not in the forced set.
func (cm *ConfigManager) isSectionSkipped(ctx context.Context, sn string) bool {
	if len(cm.State.ForcedConfig) == 0 {
		return false
	}

	if _, ok := cm.State.ForcedConfig[sn]; !ok {
		return true
	}

	logger.DebugContext(ctx, "Processing forced keys for section", "section", sn)

	return false
}

// compareOneKey compares a single required variable against its previous value and
// updates Changed / LastVal / ChangedKeys as needed.
func (cm *ConfigManager) compareOneKey(
	ctx context.Context, sn string, section *config.MtaConfigSection, key, cfgType string,
) {
	lookupKey := key
	if after, ok := strings.CutPrefix(key, "!"); ok {
		lookupKey = after
	}

	prevVal := cm.State.LastVal(ctx, sn, cfgType, lookupKey, "")
	currentVal, err := cm.LookUpConfig(ctx, cfgType, lookupKey)

	logger.DebugContext(ctx, "Comparing key values",
		"key", lookupKey,
		"current", currentVal,
		"previous", prevVal)

	if err != nil || currentVal == "" {
		if prevVal != "" {
			logger.InfoContext(ctx, "Variable changed to undefined",
				"key", lookupKey,
				"previous_value", prevVal)
			cm.State.DelVal(sn, cfgType, lookupKey)

			section.Changed = true
		}

		return
	}

	if prevVal != currentVal {
		if !cm.State.FirstRun {
			logger.InfoContext(ctx, "Variable changed",
				"key", lookupKey,
				"previous_value", prevVal,
				"current_value", currentVal)
		}

		cm.State.LastVal(ctx, sn, cfgType, lookupKey, currentVal)
		cm.State.ChangedKeysForSection(sn, lookupKey)

		section.Changed = true
	}
}

// applyServiceStatusChanges queues stop actions for services that are now disabled.
func (cm *ConfigManager) applyServiceStatusChanges(ctx context.Context) {
	for service := range cm.State.CurrentActions.Services {
		isRunning, err := cm.LookUpConfig(ctx, "SERVICE", service)
		if err != nil || state.IsFalseValue(isRunning) {
			logger.InfoContext(ctx, "Service was disabled, need to stop", "service", service)
			cm.State.CurRestarts(service, 0)
		}
	}
}

// trackNewlyEnabledServices detects services not yet in CurrentActions and either
// sets their initial status (first run) or queues a start action.
func (cm *ConfigManager) trackNewlyEnabledServices(ctx context.Context) {
	for service := range cm.State.ServerConfig.ServiceConfig {
		if _, exists := cm.State.CurrentActions.Services[service]; exists {
			continue
		}

		if cm.State.FirstRun {
			cm.initServiceOnFirstRun(ctx, service)
		} else {
			logger.InfoContext(ctx, "Service was enabled, need to start", "service", service)
			cm.State.CurRestarts(service, 1)
		}
	}
}

// initServiceOnFirstRun records the initial running/stopped state for a service on first run.
func (cm *ConfigManager) initServiceOnFirstRun(ctx context.Context, service string) {
	if !cm.ServiceMgr.HasCommand(service) {
		logger.DebugContext(ctx, "Command not defined for service", "service", service)
	}

	logger.DebugContext(ctx, "Tracking service", "service", service)

	if state.IsTrueValue(cm.State.ServerConfig.ServiceConfig[service]) {
		cm.State.CurServices(service, "running")
	} else {
		cm.State.CurServices(service, "stopped")
	}
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
