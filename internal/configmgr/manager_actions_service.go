// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/proxy"
)

// DoRestarts executes service restarts based on the current state.
// DoRestarts executes service restarts using the ServiceManager with dependency cascading.
func (cm *ConfigManager) DoRestarts(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Executing service restarts")

	// Transfer State.CurrentActions.Restarts to ServiceManager.RestartQueue
	// This bridges the gap between the state tracking and service control layers
	for service := range cm.State.CurrentActions.Restarts {
		if err := cm.ServiceMgr.AddRestart(ctx, service); err != nil {
			logger.WarnContext(ctx, "Failed to queue restart",
				"service", service,
				"error", err)
		}
	}

	// Create a lookup function that wraps LookUpConfig for SERVICE_* keys
	configLookup := func(key string) string {
		// Extract service name from SERVICE_<name> key format
		// Key is expected in format "SERVICE_MTA", "SERVICE_PROXY", etc.
		if len(key) > 8 && key[:8] == "SERVICE_" {
			serviceName := strings.ToLower(key[8:])

			value, err := cm.LookUpConfig(ctx, "SERVICE", serviceName)
			if err == nil && strings.EqualFold(value, constTRUE) {
				return serviceEnabled
			}
		}

		return serviceDisabled
	}

	// Process all queued restarts with dependency cascading
	if err := cm.ServiceMgr.ProcessRestarts(ctx, configLookup); err != nil {
		logger.ErrorContext(ctx, "Error during service restarts",
			"error", err)
	}

	logger.DebugContext(ctx, "Service restarts complete")
}

// ProcessIsRunning checks if a service is currently running.
func (cm *ConfigManager) ProcessIsRunning(ctx context.Context, service string) bool {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	// Use the ServiceMgr to check process status
	running, err := cm.ServiceMgr.IsRunning(ctx, service)
	if err != nil {
		logger.WarnContext(ctx, "Error checking if service is running",
			"service", service,
			"error", err)
	}

	return running
}

// ProcessIsNotRunning checks if a service is currently not running.
func (cm *ConfigManager) ProcessIsNotRunning(ctx context.Context, service string) bool {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	return !cm.ProcessIsRunning(ctx, service)
}

// RunProxygenWithConfigs executes proxy configuration generation with loaded configs.
// This method provides loaded LocalConfig, GlobalConfig, and ServerConfig to the proxy generator,
// allowing it to resolve variables from actual LDAP data.
func (cm *ConfigManager) RunProxygenWithConfigs(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	startTime := time.Now()

	logger.DebugContext(ctx, "Running proxygen with loaded configurations")

	// NOTE: DO NOT invalidate LDAP cache here. The configs are already loaded
	// and passed to the proxy generator. Cache invalidation should only happen
	// when SIGHUP/network reload is explicitly requested (see LoadAllConfigsWithRetry).

	// Create proxy generator with loaded configs from state
	initStart := time.Now()
	gen, err := proxy.LoadConfiguration(
		ctx,
		cm.mainConfig,
		cm.State.LocalConfig,
		cm.State.GlobalConfig,
		cm.State.ServerConfig,
		cm.LdapClient,
		cm.Cache)
	initDuration := time.Since(initStart)
	logger.DebugContext(ctx, "Proxy generator initialization completed",
		"duration_seconds", initDuration.Seconds())

	if err != nil {
		return fmt.Errorf("failed to initialize proxy generator: %w", err)
	}

	// Generate all nginx configuration files
	logger.DebugContext(ctx, "Generating nginx proxy configuration files")

	genStart := time.Now()

	if err := gen.GenerateAll(ctx); err != nil {
		return fmt.Errorf("proxy configuration generation failed: %w", err)
	}

	genDuration := time.Since(genStart)

	totalDuration := time.Since(startTime)
	logger.DebugContext(ctx, "RunProxygenWithConfigs timing",
		"init_seconds", initDuration.Seconds(),
		"generation_seconds", genDuration.Seconds(),
		"total_seconds", totalDuration.Seconds())

	logger.DebugContext(ctx, "Proxy configuration generation completed successfully")

	return nil
}
