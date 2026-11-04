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

	// Settings holds the legacy ProxyConfGen location/prefix options; the
	// zero value keeps the defaults.
	Settings *Settings
}

// Settings are the ProxyConfGen options that decide where templates are read
// and files are written. Empty fields keep the legacy defaults.
type Settings struct {
	// TemplateDir is the template directory (-t), default <workdir>/conf/nginx/templates.
	TemplateDir string
	// IncludeDir is the directory of the included files relative to
	// <workdir>/conf (-i), default nginx/includes.
	IncludeDir string
	// ConfPrefix is the generated file prefix (-p), default nginx.conf.
	ConfPrefix string
	// TemplatePrefix is the template file prefix (-P), default ConfPrefix.
	TemplatePrefix string
	// ForceDNSResolution is -f.
	ForceDNSResolution bool
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
		Hostname:      cfg.Hostname,
		DryRun:        false,
	}

	g.setLayout(cfg.BaseDir)

	g.applySettings(src.Settings)

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

// applySettings overlays the ProxyConfGen location/prefix options on the
// defaults NewGenerator derived from the base directory.
func (g *Generator) applySettings(s *Settings) {
	if s == nil {
		s = &Settings{}
	}

	g.ConfPrefix = nginxConfPrefix
	if s.ConfPrefix != "" {
		g.ConfPrefix = s.ConfPrefix
	}

	g.TemplatePrefix = g.ConfPrefix
	if s.TemplatePrefix != "" {
		g.TemplatePrefix = s.TemplatePrefix
	}

	if s.TemplateDir != "" {
		g.TemplateDir = s.TemplateDir
	}

	if s.IncludeDir != "" {
		g.IncludesDir = filepath.Join(g.ConfDir, s.IncludeDir)
	}

	g.ForceDNSResolution = s.ForceDNSResolution
}

// confPrefix is the generated file prefix, nginx.conf unless -p was given.
func (g *Generator) confPrefix() string {
	if g.ConfPrefix == "" {
		return nginxConfPrefix
	}

	return g.ConfPrefix
}

// templatePrefix is the template file prefix, the config prefix unless -P was given.
func (g *Generator) templatePrefix() string {
	if g.TemplatePrefix == "" {
		return g.confPrefix()
	}

	return g.TemplatePrefix
}

// setLayout sets the working, template, conf and includes directories of a
// working directory (ProxyConfGen defaults).
func (g *Generator) setLayout(workDir string) {
	g.WorkingDir = workDir
	g.TemplateDir = filepath.Join(workDir, "conf", "nginx", "templates")
	g.ConfDir = filepath.Join(workDir, "conf")
	g.IncludesDir = filepath.Join(workDir, "conf", "nginx", "includes")
}
