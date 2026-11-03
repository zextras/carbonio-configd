// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// runtimeOpts selects how buildRuntime wires the process.
type runtimeOpts struct {
	// readOnly runs the manager and the service control on sys.ReadOnly(env):
	// whatever they try to write, run or signal fails (shadow mode).
	readOnly bool
	// skipCache disables the LDAP result cache (plan).
	skipCache bool
	// disableRestarts is --disable-restarts.
	disableRestarts bool
	// loadLocal loads the effective localconfig; nil means
	// localconfig.LoadResolvedConfig. Tests inject it.
	loadLocal func() (map[string]string, error)
}

// runtime is everything the daemon, the shadow run, plan and the forced
// rewrite share: the configuration, its state, the LDAP clients, the one
// service manager and the config manager that drives them.
type runtime struct {
	cfg   *config.Config
	state *state.State
	ldap  *ldap.Ldap
	cm    *configmgr.ConfigManager
	// sm is the service manager of the process: restarts, the watchdog and
	// status checks share it.
	sm *services.ServiceManager
	// ctl is the host controller; it also provides the watchdog's process snapshots.
	ctl *services.Controller
	// hooks is the service backend selected by zmconfigd_service_hooks, nil on a host.
	hooks *services.HookBackend
	// env is the system the manager runs on (read-only for shadow).
	env sys.System
}

// buildRuntime is the only place that loads the effective localconfig, builds
// the LDAP clients, picks the service backend (validating its hooks file),
// applies the scope and the template dir, and applies --disable-restarts.
func buildRuntime(ctx context.Context, env sys.System, opts *runtimeOpts) (*runtime, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "main")

	cfg, err := config.NewConfig()
	if err != nil {
		return nil, fmt.Errorf("initialize config: %w", err)
	}

	if opts.readOnly {
		env = sys.ReadOnly(env)
	}

	applyRuntimeKeys(ctx, cfg, opts)

	appState := state.NewState()
	ldapClient := ldap.NewLdap(ctx, cfg)

	// Initial local config, replaced by the first load: the listen port and
	// IP mode for contact_service.
	appState.Publish(snapshot.New(snapshot.Parts{Local: map[string]string{
		"zmconfigd_listen_port": "7171",
		"zimbraIPMode":          rewrite.IPModeIPv4,
	}}))

	ctl := services.NewController(env)
	sm := services.NewServiceManager(ctl)

	var hooks *services.HookBackend

	if cfg.ServiceHooks != "" {
		if hooks, err = loadHooks(env, ctl, cfg.ServiceHooks); err != nil {
			return nil, err
		}

		sm = services.NewServiceManager(hooks)
	}

	sm.DisableRestarts = opts.disableRestarts

	cm := configmgr.NewConfigManager(ctx, cfg, appState, ldapClient, cache.New(ctx, opts.skipCache), env, sm)

	return &runtime{cfg: cfg, state: appState, ldap: ldapClient, cm: cm, sm: sm, ctl: ctl, hooks: hooks, env: env}, nil
}

// applyRuntimeKeys reads the keys that shape the wiring (template dir, scope,
// hooks) from the effective localconfig. A load failure leaves the defaults:
// the cycle's own load retries and reports it.
func applyRuntimeKeys(ctx context.Context, cfg *config.Config, opts *runtimeOpts) {
	load := opts.loadLocal
	if load == nil {
		load = localconfig.LoadResolvedConfig
	}

	data, err := load()
	if err != nil {
		logger.WarnContext(ctx, "Cannot read localconfig for the runtime settings, using defaults", "error", err)

		return
	}

	cfg.SetRuntimeVals(snapshot.New(snapshot.Parts{Local: data}).LocalConfig())
}

// loadHooks reads and checks the hooks file.
func loadHooks(env sys.System, ctl *services.Controller, path string) (*services.HookBackend, error) {
	data, err := env.FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service hooks %s: %w", path, err)
	}

	hooks, err := services.ParseHooks(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	backend, err := services.NewHookBackend(ctl, hooks)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return backend, nil
}

// validateHooks checks that the hooks file covers what this process will do:
// a restart hook for every service a RESTART line of an in-scope section names,
// and a status hook for every service the watchdog monitors. It must run on
// the parsed zmconfigd.cf. It is a no-op on a host.
func (rt *runtime) validateHooks(conf *config.MtaConfig, watchdogOn bool) error {
	if rt.hooks == nil {
		return nil
	}

	restart := map[string]bool{}

	for name, sec := range conf.Sections {
		if sec == nil || (len(rt.cfg.Sections) > 0 && !slices.Contains(rt.cfg.Sections, name)) {
			continue
		}

		for svc := range sec.Restarts {
			restart[strings.ToLower(svc)] = true
		}
	}

	var status []string

	if watchdogOn {
		status = rt.watchdogServices()
	}

	return rt.hooks.Validate(slices.Sorted(maps.Keys(restart)), status)
}

// watchdogServices is the list of services the watchdog monitors.
func (rt *runtime) watchdogServices() []string {
	if len(rt.cfg.WdList) == 0 {
		return []string{componentAntivirus}
	}

	return rt.cfg.WdList
}
