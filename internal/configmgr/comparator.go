// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/lookup"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
)

// Comparator detects configuration changes between successive load cycles.
type Comparator struct {
	lookup     lookup.ConfigLookup
	state      *state.State
	serviceMgr services.Manager
}

// NewComparator returns a Comparator bound to the supplied lookup, state and
// service manager. All three must be non-nil.
func NewComparator(cl lookup.ConfigLookup, st *state.State, sm services.Manager) *Comparator {
	return &Comparator{lookup: cl, state: st, serviceMgr: sm}
}

// CheckConditional evaluates a conditional expression based on configuration type and key.
func (c *Comparator) CheckConditional(ctx context.Context, cfgType, key string) (bool, error) {
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

	value, err := c.lookup.LookUpConfig(ctx, cfgType, key)
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
func (c *Comparator) CompareKeys(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Comparing keys")

	if err := c.checkAllServicesEnabled(ctx); err != nil {
		return err
	}

	c.compareSectionKeys(ctx)
	c.applyServiceStatusChanges(ctx)
	c.trackNewlyEnabledServices(ctx)

	logger.DebugContext(ctx, "Key comparison complete")

	return nil
}

// checkAllServicesEnabled returns an error when every known service is disabled
// (mirrors the Jython "all services detected disabled" guard).
func (c *Comparator) checkAllServicesEnabled(ctx context.Context) error {
	stoppedServices := 0
	totalServices := 0

	for service := range c.state.CurrentActions.Services {
		totalServices++

		isRunning, err := c.lookup.LookUpConfig(ctx, "SERVICE", service)
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
func (c *Comparator) compareSectionKeys(ctx context.Context) {
	for sn, section := range c.state.MtaConfig.Sections {
		logger.DebugContext(ctx, "Checking keys for section", "section", sn)

		if c.isSectionSkipped(ctx, sn) {
			continue
		}

		section.Changed = false

		c.state.ResetChangedKeys(sn)

		for key, cfgType := range section.RequiredVars {
			c.compareOneKey(ctx, sn, section, key, cfgType)
		}
	}
}

// isSectionSkipped returns true when forced-config mode is active and sn is not in the forced set.
func (c *Comparator) isSectionSkipped(ctx context.Context, sn string) bool {
	if len(c.state.ForcedConfig) == 0 {
		return false
	}

	if _, ok := c.state.ForcedConfig[sn]; !ok {
		return true
	}

	logger.DebugContext(ctx, "Processing forced keys for section", "section", sn)

	return false
}

// compareOneKey compares a single required variable against its previous value and
// updates Changed / LastVal / ChangedKeys as needed.
func (c *Comparator) compareOneKey(
	ctx context.Context, sn string, section *config.MtaConfigSection, key, cfgType string,
) {
	lookupKey := key
	if after, ok := strings.CutPrefix(key, "!"); ok {
		lookupKey = after
	}

	prevVal := c.state.LastVal(ctx, sn, cfgType, lookupKey, "")
	currentVal, err := c.lookup.LookUpConfig(ctx, cfgType, lookupKey)

	logger.DebugContext(ctx, "Comparing key values",
		"key", lookupKey,
		"current", currentVal,
		"previous", prevVal)

	if err != nil || currentVal == "" {
		if prevVal != "" {
			logger.InfoContext(ctx, "Variable changed to undefined",
				"key", lookupKey,
				"previous_value", prevVal)
			c.state.DelVal(sn, cfgType, lookupKey)

			section.Changed = true
		}

		return
	}

	if prevVal != currentVal {
		if !c.state.FirstRun {
			logger.InfoContext(ctx, "Variable changed",
				"key", lookupKey,
				"previous_value", prevVal,
				"current_value", currentVal)
		}

		c.state.LastVal(ctx, sn, cfgType, lookupKey, currentVal)
		c.state.ChangedKeysForSection(sn, lookupKey)

		section.Changed = true
	}
}

// applyServiceStatusChanges queues stop actions for services that are now disabled.
func (c *Comparator) applyServiceStatusChanges(ctx context.Context) {
	for service := range c.state.CurrentActions.Services {
		isRunning, err := c.lookup.LookUpConfig(ctx, "SERVICE", service)
		if err != nil || state.IsFalseValue(isRunning) {
			logger.InfoContext(ctx, "Service was disabled, need to stop", "service", service)
			c.state.CurRestarts(service, 0)
		}
	}
}

// trackNewlyEnabledServices detects services not yet in CurrentActions and either
// sets their initial status (first run) or queues a start action.
func (c *Comparator) trackNewlyEnabledServices(ctx context.Context) {
	for service := range c.state.ServerConfig.ServiceConfig.Snapshot() {
		if _, exists := c.state.CurrentActions.Services[service]; exists {
			continue
		}

		if c.state.FirstRun {
			c.initServiceOnFirstRun(ctx, service)
		} else {
			logger.InfoContext(ctx, "Service was enabled, need to start", "service", service)
			c.state.CurRestarts(service, 1)
		}
	}
}

// initServiceOnFirstRun records the initial running/stopped state for a service on first run.
func (c *Comparator) initServiceOnFirstRun(ctx context.Context, service string) {
	if !c.serviceMgr.HasCommand(service) {
		logger.DebugContext(ctx, "Command not defined for service", "service", service)
	}

	logger.DebugContext(ctx, "Tracking service", "service", service)

	val, _ := c.state.ServerConfig.ServiceConfig.Get(service)
	if state.IsTrueValue(val) {
		c.state.CurServices(service, "running")
	} else {
		c.state.CurServices(service, "stopped")
	}
}
