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
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Sources groups the pre-loaded configuration and service handles a Generator
// is built from. Zero-value fields are allowed (testing mode).
type Sources struct {
	Local  *config.LocalConfig
	Global *config.GlobalConfig
	Server *config.ServerConfig
	Ldap   *ldap.Ldap
	Cache  *cache.ConfigCache
}

// NewGenerator creates a new proxy configuration generator
func NewGenerator(ctx context.Context, env sys.System, cfg *config.Config, src Sources) (*Generator, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Initialize generator with configuration
	g := &Generator{
		env:           env,
		Config:        cfg,
		LocalConfig:   src.Local,
		GlobalConfig:  src.Global,
		ServerConfig:  src.Server,
		LdapClient:    src.Ldap,
		Cache:         src.Cache,
		upstreamCache: &upstreamQueryCache{},
		WorkingDir:    cfg.BaseDir,
		TemplateDir:   filepath.Join(cfg.BaseDir, "conf", "nginx", "templates"),
		ConfDir:       filepath.Join(cfg.BaseDir, "conf"),
		IncludesDir:   filepath.Join(cfg.BaseDir, "conf", "nginx", "includes"),
		Hostname:      cfg.Hostname,
		DryRun:        false,
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
func LoadConfiguration(ctx context.Context, env sys.System, cfg *config.Config, src Sources) (*Generator, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	logger.DebugContext(ctx, "Loading proxy configuration from environment")

	// Use provided configs, or create empty ones if nil (for testing)
	if src.Local == nil {
		src.Local = &config.LocalConfig{Data: config.NewConfigMap()}

		logger.DebugContext(ctx, "Using empty LocalConfig (testing mode)")
	}

	if src.Global == nil {
		src.Global = &config.GlobalConfig{Data: config.NewConfigMap()}

		logger.DebugContext(ctx, "Using empty GlobalConfig (testing mode)")
	}

	if src.Server == nil {
		src.Server = &config.ServerConfig{
			Data:          config.NewConfigMap(),
			ServiceConfig: config.NewConfigMap(),
		}

		logger.DebugContext(ctx, "Using empty ServerConfig (testing mode)")
	}

	logger.DebugContext(ctx, "Proxy configuration loaded",
		"local_count", src.Local.Data.Len(),
		"global_count", src.Global.Data.Len(),
		"server_count", src.Server.Data.Len())

	return NewGenerator(ctx, env, cfg, src)
}

// SetDryRun enables or disables dry-run mode
func (g *Generator) SetDryRun(ctx context.Context, dryRun bool) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	g.DryRun = dryRun
	if dryRun {
		logger.InfoContext(ctx, "Proxy generator in dry-run mode - no files will be written")
	}
}

// SetVerbose enables or disables verbose logging mode
func (g *Generator) SetVerbose(ctx context.Context, verbose bool) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	g.Verbose = verbose
	if verbose {
		logger.InfoContext(ctx, "Proxy generator in verbose mode - detailed logging enabled")
	}
}
