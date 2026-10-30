// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/proxy"
)

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

	// Create proxy generator with the configs of the current snapshot
	snap := cm.State.Snapshot()
	initStart := time.Now()
	gen, err := proxy.LoadConfiguration(
		ctx,
		cm.env,
		cm.mainConfig,
		snap.LocalConfig(),
		snap.GlobalConfig(),
		snap.ServerConfig(),
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
