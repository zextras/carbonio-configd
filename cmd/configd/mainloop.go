// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// cmd/configd/mainloop.go
package main

import (
	"context"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/network"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/systemd"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

// MainLoopActionTrigger implements the network.ActionTrigger interface.
type MainLoopActionTrigger struct {
	ReloadChan   chan struct{}
	State        *state.State
	EventCounter int // Track number of events received since last poll
	Ctx          context.Context
}

// TriggerRewrite is called by the network handler to signal a rewrite.
func (t *MainLoopActionTrigger) TriggerRewrite(configs []string) {
	// Use the stored context from main loop
	ctx := t.Ctx
	ctx = logger.ContextWithComponent(ctx, "mainloop")
	logger.DebugContext(ctx, "Triggering rewrite for configs", "configs", configs)

	for _, cfg := range configs {
		logger.DebugContext(ctx, "Processing rewrite request", "config", cfg)
	}

	t.State.AddRequestedConfigs(ctx, configs)
	t.EventCounter++ // Track that we received an event

	select {
	case t.ReloadChan <- struct{}{}:
		logger.DebugContext(ctx, "Reload signal sent to main loop from network handler")
	default:
		logger.DebugContext(ctx, "Reload channel blocked, main loop already processing or not ready")
	}
}

// startSdWatchdogKeepAlive launches a goroutine that pings systemd's watchdog at pingInterval.
func startSdWatchdogKeepAlive(ctx context.Context, notifier *sdnotify.Notifier, pingInterval time.Duration) {
	logger.InfoContext(ctx, "Starting systemd watchdog keep-alive",
		"ping_interval", pingInterval)

	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := notifier.WatchdogPing(); err != nil {
					logger.ErrorContext(ctx, "Failed to send watchdog ping", "error", err)
				}
			}
		}
	}()
}

// runForcedMode processes forced rewrites and returns; the caller exits after.
func runForcedMode(
	ctx context.Context,
	args *Args,
	appState *state.State,
	mainCfg *config.Config,
	configManager *configmgr.ConfigManager,
	serviceManager services.Manager,
) {
	logger.InfoContext(ctx, "Processing forced rewrites as standalone process")

	for _, arg := range args.ForcedConfigs {
		appState.Forced++

		logger.InfoContext(ctx, "Adding forced config", "config", arg)
		appState.ForcedConfig[arg] = arg
	}

	if err := configManager.LoadAllConfigs(ctx); err != nil {
		logger.FatalContext(ctx, "Failed to load configs for forced run", "error", err)
	}

	if err := configManager.ParseMtaConfig(ctx, mainCfg.ConfigFile); err != nil {
		logger.FatalContext(ctx, "Failed to parse MTA config for forced run", "error", err)
	}

	buildServiceDependencies(ctx, serviceManager, appState)
	configManager.CompileActions(ctx)

	if err := configManager.DoConfigRewrites(ctx); err != nil {
		logger.ErrorContext(ctx, "Error during forced config rewrites", "error", err)
	}

	// No restarts on forced rewrites in Jython, so skipping DoRestarts here.
	logger.InfoContext(ctx, "Completed forced run", "program", mainCfg.Progname)
}

// isIdlePoll returns true when the loop should skip a config reload due to inactivity.
// Never skips when a reload signal was received — systemd expects READY=1 after RELOADING=1.
func isIdlePoll(
	cfg *config.Config,
	appState *state.State,
	trigger *MainLoopActionTrigger,
	lastEventCount int,
	reloadSignaled bool,
) bool {
	return cfg.SkipIdlePolls && !appState.FirstRun && !reloadSignaled && trigger.EventCounter == lastEventCount
}

// runLoadAndParse runs LoadAllConfigs then ParseMtaConfig, logging errors internally.
// Returns the durations of each phase and a non-nil error on any failure.
func runLoadAndParse(
	ctx context.Context,
	configManager *configmgr.ConfigManager,
	mainCfg *config.Config,
) (loadDur, parseDur time.Duration, err error) {
	t := time.Now()

	if err = configManager.LoadAllConfigs(ctx); err != nil {
		logger.ErrorContext(ctx, "Key lookup failed, sleeping", "error", err, "sleep_seconds", 60)

		return time.Since(t), 0, err
	}

	loadDur = time.Since(t)

	logger.DebugContext(ctx, "Timing: LoadAllConfigs completed",
		"duration_seconds", loadDur.Seconds(),
		"operation", "load_configs")

	select {
	case <-ctx.Done():
		return loadDur, 0, ctx.Err()
	default:
	}

	t = time.Now()

	if err = configManager.ParseMtaConfig(ctx, mainCfg.ConfigFile); err != nil {
		logger.ErrorContext(ctx, "Failed to parse MTA config (sleeping 60s)", "error", err)

		return loadDur, time.Since(t), err
	}

	parseDur = time.Since(t)

	logger.DebugContext(ctx, "Timing: ParseMtaConfig completed",
		"duration_seconds", parseDur.Seconds(),
		"operation", "parse_mta_config")

	select {
	case <-ctx.Done():
		return loadDur, parseDur, ctx.Err()
	default:
	}

	return loadDur, parseDur, nil
}

// maybeStartListener starts the network listener on the first non-forced, non-once run.
// Returns the existing server unchanged when conditions are not met.
func maybeStartListener(
	ctx context.Context,
	appState *state.State,
	args *Args,
	mainLoopTrigger *MainLoopActionTrigger,
	server *network.ThreadedStreamServer,
) *network.ThreadedStreamServer {
	if !appState.FirstRun || appState.Forced != 0 || server != nil || args.Once {
		return server
	}

	listenerPort, _ := strconv.Atoi(appState.LocalConfig.Data["zmconfigd_listen_port"])
	listenerAddr := "127.0.0.1"
	ipv6 := false

	if appState.ServerConfig.Data["zimbraIPMode"] == "ipv6" {
		listenerAddr = "::1"
		ipv6 = true
	}

	handler := &network.ConfigdRequestHandler{ActionTrigger: mainLoopTrigger}
	srv := network.NewThreadedStreamServer(listenerAddr, listenerPort, ipv6, handler)

	if err := srv.ServeForever(ctx); err != nil {
		logger.FatalContext(ctx, "Failed to start listener",
			"error", err,
			"listener_addr", listenerAddr,
			"listener_port", listenerPort)
	}

	logger.InfoContext(ctx, "Network listener started (async)",
		"listener_addr", listenerAddr,
		"listener_port", listenerPort)

	return srv
}

// notifyReady sends sd_notify READY=1 after each loop completion.
// On first loop this transitions from "activating" to "active"; subsequent calls are no-ops per spec.
func notifyReady(ctx context.Context, notifier *sdnotify.Notifier, loopCount int) {
	if err := notifier.Ready(); err != nil {
		logger.ErrorContext(ctx, "Failed to send sd_notify READY", "error", err)
	} else if notifier.Enabled() && loopCount == 0 {
		logger.InfoContext(ctx, "Sent sd_notify READY=1 to systemd")
	}
}

// phaseTimings carries the elapsed time of each runConfigPhases step for later logging.
type phaseTimings struct {
	buildDeps      time.Duration
	compareKeys    time.Duration
	compileActions time.Duration
	rewrites       time.Duration
}

// runConfigPhases executes the per-iteration dependency, compare, compile, and rewrite phases.
// Returns phase timings and skipIter=true when the caller should continue the outer loop
// (after a CompareKeys failure and the 60s back-off sleep).
func runConfigPhases(
	ctx context.Context,
	mainCfg *config.Config,
	appState *state.State,
	configManager *configmgr.ConfigManager,
	serviceManager services.Manager,
	wd *watchdog.Watchdog,
	reloadChan chan struct{},
) (timings phaseTimings, skipIter bool) {
	phaseStart := time.Now()

	buildServiceDependencies(ctx, serviceManager, appState)

	timings.buildDeps = time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: buildServiceDependencies completed",
		"duration_seconds", timings.buildDeps.Seconds(),
		"operation", "build_service_dependencies")

	phaseStart = time.Now()

	if err := configManager.CompareKeys(ctx); err != nil {
		logger.ErrorContext(ctx, "Configuration inconsistency detected (sleeping 60s)", "error", err)
		SleepWithContext(ctx, 60*time.Second, reloadChan)

		return timings, true
	}

	timings.compareKeys = time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: CompareKeys completed",
		"duration_seconds", timings.compareKeys.Seconds(),
		"operation", "compare_keys")

	if appState.FirstRun {
		phaseStart = time.Now()

		updateWatchdogServices(ctx, wd, appState, mainCfg)

		logger.DebugContext(ctx, "Timing: updateWatchdogServices completed",
			"duration_seconds", time.Since(phaseStart).Seconds(),
			"operation", "update_watchdog_services")
	}

	phaseStart = time.Now()

	configManager.CompileActions(ctx)

	timings.compileActions = time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: CompileActions completed",
		"duration_seconds", timings.compileActions.Seconds(),
		"operation", "compile_actions")

	phaseStart = time.Now()

	if err := configManager.DoConfigRewrites(ctx); err != nil {
		logger.ErrorContext(ctx, "Error during config rewrites", "error", err)
	}

	timings.rewrites = time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: DoConfigRewrites completed",
		"duration_seconds", timings.rewrites.Seconds(),
		"operation", "do_config_rewrites")

	return timings, false
}

// daemonLoopDeps bundles dependencies of the daemon main loop.
type daemonLoopDeps struct {
	cfg            *config.Config
	appState       *state.State
	configManager  *configmgr.ConfigManager
	serviceManager services.Manager
	wd             *watchdog.Watchdog
	args           *Args
	notifier       *sdnotify.Notifier
	trigger        *MainLoopActionTrigger
	reloadChan     chan struct{}
}

// loopIterState carries mutable per-iteration counters between runs.
type loopIterState struct {
	lastEventCount int
	loopCount      int
	reloadSignaled bool
	server         *network.ThreadedStreamServer
}

// iterOutcome signals how runLoopIteration wants the caller to proceed.
type iterOutcome int

const (
	iterContinue iterOutcome = iota
	iterExit
)

// runDaemonLoop is the main event loop for the configd daemon.
func runDaemonLoop(ctx context.Context, deps *daemonLoopDeps) {
	st := &loopIterState{}

	defer func() {
		if st.server != nil {
			st.server.Shutdown(ctx)
		}
	}()

	for {
		if runLoopIteration(ctx, deps, st) == iterExit {
			return
		}
	}
}

// runLoopIteration performs one main-loop iteration. It returns iterExit
// when the daemon should stop and iterContinue when the loop should run again.
func runLoopIteration(ctx context.Context, deps *daemonLoopDeps, st *loopIterState) iterOutcome {
	if outcome := consumeReloadSignal(ctx, deps.reloadChan, st); outcome == iterExit {
		return iterExit
	}

	if isIdlePoll(deps.cfg, deps.appState, deps.trigger, st.lastEventCount, st.reloadSignaled) {
		handleIdleSleep(ctx, deps, st)

		return iterContinue
	}

	st.lastEventCount = deps.trigger.EventCounter
	t1 := time.Now()

	loadDur, parseDur, err := runLoadAndParse(ctx, deps.configManager, deps.cfg)
	if err != nil {
		SleepWithContext(ctx, 60*time.Second, deps.reloadChan)

		return iterContinue
	}

	st.server = maybeStartListener(ctx, deps.appState, deps.args, deps.trigger, st.server)

	timings, skipIter := runConfigPhases(
		ctx, deps.cfg, deps.appState, deps.configManager,
		deps.serviceManager, deps.wd, deps.reloadChan,
	)
	if skipIter {
		return iterContinue
	}

	if ctxDone(ctx) {
		logger.InfoContext(ctx, "Shutdown detected after config rewrites. Exiting main loop.",
			"reason", "shutdown_signal")

		return iterExit
	}

	restartsDur := runRestartsPhase(ctx, deps.cfg, deps.configManager)

	return finalizeIteration(ctx, deps, st, &loopPhaseDurations{
		t1:       t1,
		load:     loadDur,
		parse:    parseDur,
		config:   timings,
		restarts: restartsDur,
	})
}

// consumeReloadSignal drains a ctx-cancellation or reloadChan signal without
// blocking. It sets reloadSignaled on the caller's state and returns iterExit
// when ctx is cancelled.
func consumeReloadSignal(ctx context.Context, reloadChan chan struct{}, st *loopIterState) iterOutcome {
	select {
	case <-ctx.Done():
		logger.InfoContext(ctx, "Context cancelled, exiting main loop", "reason", "shutdown_signal")

		return iterExit
	case <-reloadChan:
		logger.InfoContext(ctx, "Received reload signal, re-evaluating configurations")

		st.reloadSignaled = true
	default:
	}

	return iterContinue
}

// handleIdleSleep logs the idle skip and sleeps for the configured interval,
// updating reloadSignaled if the sleep was interrupted by a reload.
func handleIdleSleep(ctx context.Context, deps *daemonLoopDeps, st *loopIterState) {
	logger.DebugContext(ctx, "Skipping idle config poll", "reason", "no_events_since_last_check")
	logger.DebugContext(ctx, "Sleeping", "interval_seconds", deps.cfg.Interval)

	if SleepWithContext(ctx, time.Duration(deps.cfg.Interval)*time.Second, deps.reloadChan) {
		st.reloadSignaled = true
	}
}

// runRestartsPhase runs DoRestarts when restarts are enabled and returns the
// elapsed time (zero when disabled).
func runRestartsPhase(ctx context.Context, cfg *config.Config, cm *configmgr.ConfigManager) time.Duration {
	if !cfg.RestartConfig {
		return 0
	}

	phaseStart := time.Now()

	cm.DoRestarts(ctx)

	dur := time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: DoRestarts completed",
		"duration_seconds", dur.Seconds(),
		"operation", "do_restarts")

	return dur
}

// ctxDone reports whether ctx has been cancelled without blocking.
func ctxDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// loopPhaseDurations groups timings produced during a single iteration.
type loopPhaseDurations struct {
	t1       time.Time
	load     time.Duration
	parse    time.Duration
	config   phaseTimings
	restarts time.Duration
}

// finalizeIteration commits post-rewrite bookkeeping: first-run flag,
// sd_notify ready ping, timing logs, and the interval sleep or Once exit.
func finalizeIteration(
	ctx context.Context,
	deps *daemonLoopDeps,
	st *loopIterState,
	dur *loopPhaseDurations,
) iterOutcome {
	deps.appState.SetFirstRun(false)

	lt := time.Since(dur.t1)

	notifyReady(ctx, deps.notifier, st.loopCount)

	st.reloadSignaled = false
	st.loopCount++

	_ = deps.notifier.Status("loop %d completed in %.1fs, next in %ds",
		st.loopCount, lt.Seconds(), deps.cfg.Interval)

	logger.DebugContext(ctx, "Timing: Loop timing breakdown",
		"load_configs_seconds", dur.load.Seconds(),
		"parse_mta_seconds", dur.parse.Seconds(),
		"build_deps_seconds", dur.config.buildDeps.Seconds(),
		"compare_keys_seconds", dur.config.compareKeys.Seconds(),
		"compile_actions_seconds", dur.config.compileActions.Seconds(),
		"rewrites_seconds", dur.config.rewrites.Seconds(),
		"restarts_seconds", dur.restarts.Seconds())
	logger.InfoContext(ctx, "Loop completed", "total_duration_seconds", lt.Seconds())

	if deps.args.Once {
		logger.InfoContext(ctx, "Single-run mode: Exiting after one loop completion",
			"total_duration_seconds", lt.Seconds())

		return iterExit
	}

	logger.DebugContext(ctx, "Sleeping for interval", "interval_seconds", deps.cfg.Interval)

	if SleepWithContext(ctx, time.Duration(deps.cfg.Interval)*time.Second, deps.reloadChan) {
		st.reloadSignaled = true
	}

	return iterContinue
}

// RunMainLoop contains the core logic of the configd daemon.
func RunMainLoop(
	ctx context.Context,
	mainCfg *config.Config,
	appState *state.State,
	ldapClient *ldap.Ldap,
	args *Args,
	notifier *sdnotify.Notifier) {
	ctx = logger.ContextWithComponent(ctx, "mainloop")
	// Create cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cacheInstance := cache.New(ctx, false) // skipCache=false to enable caching

	configManager := configmgr.NewConfigManager(ctx, mainCfg, appState, ldapClient, cacheInstance)
	serviceManager := services.NewServiceManager()

	systemdManager := systemd.NewManager()
	if systemdManager.IsSystemdEnabled(ctx) {
		logger.InfoContext(ctx, "Detected systemd-enabled environment",
			"use_systemctl", true,
			"fallback", "zm*ctl")

		serviceManager.UseSystemd = true

		configManager.ServiceMgr.SetUseSystemd(true)
	} else {
		logger.InfoContext(ctx, "Detected traditional environment",
			"use_systemctl", false,
			"scripts_only", "zm*ctl")

		serviceManager.UseSystemd = false
	}

	serviceManager.DisableRestarts = args.DisableRestarts

	watchdogInterval := time.Duration(mainCfg.WatchdogInterval) * time.Second
	if watchdogInterval == 0 {
		watchdogInterval = 120 * time.Second
	}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  watchdogInterval,
		ServiceManager: serviceManager,
		State:          appState,
		ConfigLookup: func(key string) string {
			if val, exists := appState.LocalConfig.Data[key]; exists {
				return val
			}

			return ""
		},
	})

	reloadChan := make(chan struct{}, 1)
	SetupSignalHandler(appState, cancel, reloadChan, notifier)

	// Start watchdog in daemon mode (not for forced configs)
	if !args.HasForcedConfigs() {
		wd.Start(ctx)
		defer wd.Stop(ctx)
	}

	// Start systemd watchdog keep-alive goroutine if WATCHDOG_USEC is set.
	// Pings at half the interval so we stay well within the WatchdogSec deadline.
	if wdInterval, ok := sdnotify.WatchdogEnabled(); ok {
		pingInterval := wdInterval / 2 //nolint:mnd // half of WatchdogSec is the recommended ping interval
		startSdWatchdogKeepAlive(ctx, notifier, pingInterval)
	}

	mainLoopTrigger := &MainLoopActionTrigger{
		ReloadChan:   reloadChan,
		State:        appState,
		EventCounter: 0,
		Ctx:          ctx,
	}

	if args.HasForcedConfigs() {
		runForcedMode(ctx, args, appState, mainCfg, configManager, serviceManager)
		// Watchdog was never started in forced config mode, no defer to worry about
		os.Exit(0) //nolint:gocritic // exitAfterDefer false positive - wd.Stop() defer is in mutually exclusive if block
	}

	runDaemonLoop(ctx, &daemonLoopDeps{
		cfg:            mainCfg,
		appState:       appState,
		configManager:  configManager,
		serviceManager: serviceManager,
		wd:             wd,
		args:           args,
		notifier:       notifier,
		trigger:        mainLoopTrigger,
		reloadChan:     reloadChan,
	})
}

// buildServiceDependencies extracts dependencies from MTA config sections and sets them in the service manager.
func buildServiceDependencies(ctx context.Context, serviceMgr services.Manager, appState *state.State) {
	ctx = logger.ContextWithComponent(ctx, "mainloop")

	if appState.MtaConfig.Sections == nil {
		return
	}

	deps := make(map[string][]string)

	for name, section := range appState.MtaConfig.Sections {
		if len(section.Depends) > 0 {
			dependList := make([]string, 0, len(section.Depends))
			for dep := range section.Depends {
				dependList = append(dependList, dep)
			}

			deps[name] = dependList
		}
	}

	serviceMgr.SetDependencies(ctx, deps)
	logger.DebugContext(ctx, "Set service dependencies",
		"section_count", len(deps))
}

// updateWatchdogServices updates the watchdog with currently tracked services.
// This is called on first run to notify watchdog about services that should be monitored.
func updateWatchdogServices(ctx context.Context, wd *watchdog.Watchdog, appState *state.State, mainCfg *config.Config) {
	ctx = logger.ContextWithComponent(ctx, "mainloop")
	// Get watchdog service list from config (defaults to ["antivirus"])
	watchdogServices := mainCfg.WdList
	if len(watchdogServices) == 0 {
		watchdogServices = []string{"antivirus"}
	}

	// Enable monitoring for configured watchdog services
	wd.UpdateServiceList(ctx, watchdogServices)

	// For services that are currently tracked and in the watchdog list, mark them as available
	for service := range appState.CurrentActions.Services {
		// Check if service is in watchdog list
		isInWatchdogList := slices.Contains(watchdogServices, service)

		if isInWatchdogList {
			// Mark service as available for watchdog
			wd.AddService(ctx, service)
		}
	}
}
