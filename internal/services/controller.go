// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/systemd"
)

// defaultKillProcessTimeout is the grace period between SIGTERM and SIGKILL
// when reaping a service's processes. Long enough for well-behaved daemons to
// flush state (mailboxd's JVM shutdown hook needs ~30 s); short enough that
// zmcontrol stop still completes in a reasonable time.
const defaultKillProcessTimeout = 30 * time.Second

// Controller owns the host boundary (sys.System), the install layout, the
// service registry and the lifecycle state every start/stop/status/reload runs
// against. It replaces what used to be package-level seams: each former
// package function is a method with the same name and parameters.
//
// A Controller is safe for concurrent use once constructed. The registry is a
// plain map owned by the Controller; mutate it (via Registry) only before
// sharing the Controller between goroutines.
type Controller struct {
	env sys.System
	p   paths

	baseDir string
	runDir  string

	registry map[string]*ServiceDef

	// loadConfig loads the resolved localconfig (localconfig.LoadResolvedConfig
	// by default).
	loadConfig func() (map[string]string, error)
	// loadRawConfig loads the unresolved localconfig the Advanced status hook
	// hands to the admin client (localconfig.LoadLocalConfig by default).
	loadRawConfig func() (map[string]string, error)
	// ldapProbe answers whether slapd serves queries at urls. The default
	// dials the network, which stays real.
	ldapProbe func(ctx context.Context, urls []string) error

	noRewrite   bool
	killTimeout time.Duration

	systemd     *systemd.Manager
	modeOnce    sync.Once
	systemdMode bool
}

// Option configures a Controller.
type Option func(*Controller)

// WithBaseDir roots the install layout at dir instead of config.ZextrasBase.
func WithBaseDir(dir string) Option {
	return func(c *Controller) { c.baseDir = dir }
}

// WithRunDir moves the runtime directory (pidfiles, the stats pid bundle and
// the sd_notify sockets) from /run/carbonio to dir.
func WithRunDir(dir string) Option {
	return func(c *Controller) { c.runDir = dir }
}

// WithConfigLoader replaces the resolved-localconfig loader.
func WithConfigLoader(load func() (map[string]string, error)) Option {
	return func(c *Controller) { c.loadConfig = load }
}

// WithRawConfigLoader replaces the unresolved-localconfig loader used by the
// Advanced status hook.
func WithRawConfigLoader(load func() (map[string]string, error)) Option {
	return func(c *Controller) { c.loadRawConfig = load }
}

// WithRegistry replaces the default service registry with reg. The map is used
// as-is (not copied).
func WithRegistry(reg map[string]*ServiceDef) Option {
	return func(c *Controller) { c.registry = reg }
}

// WithNoRewrite disables config rewriting before service start.
func WithNoRewrite(noRewrite bool) Option {
	return func(c *Controller) { c.noRewrite = noRewrite }
}

// WithKillTimeout sets the SIGTERM→SIGKILL grace period used when reaping a
// service's processes.
func WithKillTimeout(d time.Duration) Option {
	return func(c *Controller) { c.killTimeout = d }
}

// WithLDAPProbe replaces the slapd readiness probe.
func WithLDAPProbe(probe func(ctx context.Context, urls []string) error) Option {
	return func(c *Controller) { c.ldapProbe = probe }
}

// NewController returns a Controller acting through env. Without options it
// is the production controller: install base config.ZextrasBase, runtime
// directory /run/carbonio, resolved localconfig, a fresh copy of the default
// registry whose hooks are bound to the Controller.
func NewController(env sys.System, opts ...Option) *Controller {
	c := &Controller{env: env, systemd: systemd.NewManager(env)}

	// The production defaults are expressed as the same options callers use to
	// tailor a Controller; caller options are applied after, so they win.
	defaults := []Option{
		WithBaseDir(config.ZextrasBase),
		WithRunDir(defaultRunDir),
		WithConfigLoader(localconfig.LoadResolvedConfig),
		WithRawConfigLoader(localconfig.LoadLocalConfig),
		WithLDAPProbe(probeLDAPReady),
		WithKillTimeout(defaultKillProcessTimeout),
	}

	for _, opt := range slices.Concat(defaults, opts) {
		opt(c)
	}

	c.p = newPaths(c.baseDir, c.runDir)

	if c.registry == nil {
		WithRegistry(c.defaultRegistry())(c)
	}

	return c
}

// Registry returns the Controller's live service registry, keyed by canonical
// service name. It is the Controller's own map: tests (and callers that tailor
// the service set) may add or replace entries before sharing the Controller.
func (c *Controller) Registry() map[string]*ServiceDef {
	return c.registry
}
