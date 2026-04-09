// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - configuration loading and initialization
package proxy

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// NewGenerator creates a new proxy configuration generator
func NewGenerator(ctx context.Context, cfg *config.Config,
	localCfg *config.LocalConfig,
	globalCfg *config.GlobalConfig,
	serverCfg *config.ServerConfig,
	ldapClient *ldap.Ldap,
	cacheInstance *cache.ConfigCache) (*Generator, error) {
	ctx = logger.ContextWithComponent(ctx, "proxy")

	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Initialize generator with configuration
	g := &Generator{
		Config:       cfg,
		LocalConfig:  localCfg,
		GlobalConfig: globalCfg,
		ServerConfig: serverCfg,
		LdapClient:   ldapClient,
		Cache:        cacheInstance,
		upstreamCache: &upstreamQueryCache{
			attributeServers:    make(map[string][]UpstreamServer),
			attributeServersSSL: make(map[string][]UpstreamServer),
		}, // Initialize upstream query cache with attribute maps
		WorkingDir:  cfg.BaseDir,
		TemplateDir: filepath.Join(cfg.BaseDir, "conf", "nginx", "templates"),
		ConfDir:     filepath.Join(cfg.BaseDir, "conf"),
		IncludesDir: filepath.Join(cfg.BaseDir, "conf", "nginx", "includes"),
		Hostname:    cfg.Hostname,
		DryRun:      false,
	}

	logger.DebugContext(ctx, "Initializing proxy generator",
		"workdir", g.WorkingDir,
		"hostname", g.Hostname)

	// Register all variables
	t1 := time.Now()

	g.RegisterVariables(ctx)

	registerDuration := time.Since(t1)
	logger.DebugContext(ctx, "Registered proxy configuration variables",
		"variable_count", len(g.Variables),
		"duration_seconds", registerDuration.Seconds())

	// Prefetch all upstream LDAP data in parallel before variable resolution
	// This populates the cache so subsequent queries during variable resolution are fast
	// Expected time savings: 2-3 seconds (parallel execution vs sequential during resolution)
	t2 := time.Now()

	if err := g.PrefetchUpstreamData(ctx); err != nil {
		logger.WarnContext(ctx, "Failed to prefetch upstream data, continuing with on-demand queries",
			"error", err)
		// Continue anyway - queries will happen on-demand during variable resolution
	}

	prefetchDuration := time.Since(t2)
	logger.DebugContext(ctx, "Prefetch upstream data completed",
		"duration_seconds", prefetchDuration.Seconds())

	// Resolve all variable values
	logger.DebugContext(ctx, "About to resolve all variables")

	t3 := time.Now()

	if err := g.ResolveAllVariables(ctx); err != nil {
		logger.ErrorContext(ctx, "Failed to resolve variables",
			"error", err)

		return nil, fmt.Errorf("failed to resolve variables: %w", err)
	}

	resolveDuration := time.Since(t3)

	logger.DebugContext(ctx, "Resolved all proxy configuration variables",
		"duration_seconds", resolveDuration.Seconds())

	return g, nil
}

// LoadConfiguration loads configurations from a ConfigManager's state.
// This is a convenience function that creates a generator with pre-loaded configurations.
// In production, this should be called with configs from ConfigManager.State.
func LoadConfiguration(ctx context.Context, cfg *config.Config,
	localCfg *config.LocalConfig,
	globalCfg *config.GlobalConfig,
	serverCfg *config.ServerConfig,
	ldapClient *ldap.Ldap,
	cacheInstance *cache.ConfigCache) (*Generator, error) {
	ctx = logger.ContextWithComponent(ctx, "proxy")

	logger.DebugContext(ctx, "Loading proxy configuration from environment")

	// Use provided configs, or create empty ones if nil (for testing)
	if localCfg == nil {
		localCfg = &config.LocalConfig{Data: make(map[string]string)}

		logger.DebugContext(ctx, "Using empty LocalConfig (testing mode)")
	}

	if globalCfg == nil {
		globalCfg = &config.GlobalConfig{Data: make(map[string]string)}

		logger.DebugContext(ctx, "Using empty GlobalConfig (testing mode)")
	}

	if serverCfg == nil {
		serverCfg = &config.ServerConfig{
			Data:          make(map[string]string),
			ServiceConfig: make(map[string]string),
		}

		logger.DebugContext(ctx, "Using empty ServerConfig (testing mode)")
	}

	logger.DebugContext(ctx, "Proxy configuration loaded",
		"local_count", len(localCfg.Data),
		"global_count", len(globalCfg.Data),
		"server_count", len(serverCfg.Data))

	return NewGenerator(ctx, cfg, localCfg, globalCfg, serverCfg, ldapClient, cacheInstance)
}

// SetDryRun enables or disables dry-run mode
func (g *Generator) SetDryRun(ctx context.Context, dryRun bool) {
	ctx = logger.ContextWithComponent(ctx, "proxy")

	g.DryRun = dryRun
	if dryRun {
		logger.InfoContext(ctx, "Proxy generator in dry-run mode - no files will be written")
	}
}

// SetVerbose enables or disables verbose logging mode
func (g *Generator) SetVerbose(ctx context.Context, verbose bool) {
	ctx = logger.ContextWithComponent(ctx, "proxy")

	g.Verbose = verbose
	if verbose {
		logger.InfoContext(ctx, "Proxy generator in verbose mode - detailed logging enabled")
	}
}

// IsVerbose returns whether verbose mode is enabled
func (g *Generator) IsVerbose() bool {
	return g.Verbose
}

// IsDryRun returns whether dry-run mode is enabled
func (g *Generator) IsDryRun() bool {
	return g.DryRun
}

// ReloadConfiguration reloads configuration and re-resolves all variables
func (g *Generator) ReloadConfiguration(ctx context.Context, localCfg *config.LocalConfig,
	globalCfg *config.GlobalConfig,
	serverCfg *config.ServerConfig) error {
	ctx = logger.ContextWithComponent(ctx, "proxy")

	logger.InfoContext(ctx, "Reloading proxy configuration")

	g.LocalConfig = localCfg
	g.GlobalConfig = globalCfg
	g.ServerConfig = serverCfg

	// Invalidate upstream query cache on reload
	g.ClearUpstreamCache(ctx)

	// Re-resolve all variables with new configuration
	if err := g.ResolveAllVariables(ctx); err != nil {
		return fmt.Errorf("failed to resolve variables after reload: %w", err)
	}

	logger.InfoContext(ctx, "Proxy configuration reloaded successfully")

	return nil
}

// ClearUpstreamCache clears the cached upstream query results
// This should be called when configuration changes or before each generation cycle
func (g *Generator) ClearUpstreamCache(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "proxy")
	if g.upstreamCache != nil {
		logger.DebugContext(ctx, "Clearing upstream query cache")

		g.upstreamCache.reverseProxyBackends = nil
		g.upstreamCache.reverseProxyBackendsSSL = nil
		g.upstreamCache.memcachedServers = nil
		// Clear attribute caches
		if g.upstreamCache.attributeServers != nil {
			g.upstreamCache.attributeServers = make(map[string][]UpstreamServer)
		}

		if g.upstreamCache.attributeServersSSL != nil {
			g.upstreamCache.attributeServersSSL = make(map[string][]UpstreamServer)
		}
		// Clear cached gas output
		g.upstreamCache.gasOutput = ""
		g.upstreamCache.populated = false
	}
}

// GetConfigSummary returns a summary of the current configuration
func (g *Generator) GetConfigSummary() map[string]any {
	return map[string]any{
		"working_dir":  g.WorkingDir,
		"template_dir": g.TemplateDir,
		"conf_dir":     g.ConfDir,
		"includes_dir": g.IncludesDir,
		"hostname":     g.Hostname,
		"dry_run":      g.DryRun,
		"verbose":      g.Verbose,
		"var_count":    len(g.Variables),
		"domain_count": len(g.Domains),
		"server_count": len(g.Servers),
	}
}
