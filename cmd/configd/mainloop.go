// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// cmd/configd/mainloop.go
package main

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/ldapwatch"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/network"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

// componentAntivirus is the watchdog's default monitored service.
const componentAntivirus = "antivirus"

// MainLoopActionTrigger implements the network.ActionTrigger interface.
//
// REWRITE clients wait for the main loop (jylibs/listener.py holds them on
// lAction until the rewrite phase of the cycle that took their sections
// finished). A client is registered in waiting when it arrives. The loop moves
// the waiting clients to inflight just before it plans (claimWaiters), because
// Plan takes the requested sections; once the cycle's rewrite phase is over
// the inflight clients are released (completeWaiters), and when the cycle did
// not get to rewrite they go back to waiting (requeueWaiters), their sections
// still queued, for the next cycle. A client that arrives mid-cycle stays in
// waiting for the next one.
type MainLoopActionTrigger struct {
	ReloadChan   chan struct{}
	State        *state.State
	EventCounter atomic.Int64 // Track number of events received since last poll

	mu       sync.Mutex
	waiting  []*rewriteWaiter
	inflight []*rewriteWaiter
}

// rewriteWaiter is one REWRITE client waiting for the main loop.
type rewriteWaiter struct {
	done chan struct{}
}

// TriggerRewrite is called by the network handler to signal a rewrite. The
// returned wait is released once the main loop has processed the sections.
func (t *MainLoopActionTrigger) TriggerRewrite(ctx context.Context, configs []string) *network.RewriteWait {
	ctx = logger.ContextWithComponent(ctx, "mainloop")
	logger.DebugContext(ctx, "Triggering rewrite for configs", "configs", configs)

	for _, cfg := range configs {
		logger.DebugContext(ctx, "Processing rewrite request", "config", cfg)
	}

	// The sections are queued before the client registers: a loop that claims
	// the client has then certainly not planned yet without them.
	t.State.AddRequestedConfigs(ctx, configs)
	t.EventCounter.Add(1) // Track that we received an event

	w := &rewriteWaiter{done: make(chan struct{})}

	t.mu.Lock()
	t.waiting = append(t.waiting, w)
	t.mu.Unlock()

	select {
	case t.ReloadChan <- struct{}{}:
		logger.DebugContext(ctx, "Reload signal sent to main loop from network handler")
	default:
		logger.DebugContext(ctx, "Reload channel blocked, main loop already processing or not ready")
	}

	return &network.RewriteWait{Done: w.done, Cancel: func() { t.forget(w) }}
}

// forget drops a client that gave up (timeout, shutdown). Its sections stay
// queued, as in legacy.
func (t *MainLoopActionTrigger) forget(w *rewriteWaiter) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.waiting = slices.DeleteFunc(t.waiting, func(o *rewriteWaiter) bool { return o == w })
	t.inflight = slices.DeleteFunc(t.inflight, func(o *rewriteWaiter) bool { return o == w })
}

// claimWaiters marks the clients registered so far as depending on the cycle
// about to plan.
func (t *MainLoopActionTrigger) claimWaiters() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.inflight = append(t.inflight, t.waiting...)
	t.waiting = nil
}

// completeWaiters releases the clients of the cycle whose rewrite phase ended.
func (t *MainLoopActionTrigger) completeWaiters() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, w := range t.inflight {
		close(w.done)
	}

	t.inflight = nil
}

// requeueWaiters hands the claimed clients back to the next cycle, for a cycle
// that did not reach the rewrite phase.
func (t *MainLoopActionTrigger) requeueWaiters() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.waiting = append(t.inflight, t.waiting...)
	t.inflight = nil
}

// runForcedMode processes forced rewrites and returns the exit status: 0 when
// the rewrite ran, 1 when planning or a rewrite step failed (configd.py exits 1
// on a failed forced run, and callers such as zmtlsctl test the status). The
// caller exits after.
func runForcedMode(ctx context.Context, args *Args, rt *runtime) int {
	appState, mainCfg := rt.state, rt.cfg

	logger.InfoContext(ctx, "Processing forced rewrites as standalone process")

	for _, arg := range args.ForcedConfigs {
		appState.Forced++

		logger.InfoContext(ctx, "Adding forced config", "config", arg)
		appState.ForcedConfig[arg] = arg
	}

	if _, _, err := runLoadAndParse(ctx, rt.cm, mainCfg); err != nil {
		logger.FatalContext(ctx, "Failed to load configs for forced run", "error", err)
	}

	buildServiceDependencies(ctx, rt.sm, appState)

	return executeForced(ctx, rt.cm, mainCfg.Progname)
}

// executeForced plans and executes the forced rewrites of a loaded
// configuration. It returns the process exit status: 1 when the plan or a
// rewrite failed, so scripts and the REWRITE standalone fallback see it.
func executeForced(ctx context.Context, configManager *configmgr.ConfigManager, progname string) int {
	pl, err := configManager.Plan(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to plan forced config rewrites", "error", err)

		return 1
	}

	if err := configManager.Execute(ctx, pl); err != nil {
		logger.ErrorContext(ctx, "Error during forced config rewrites", "error", err)

		return 1
	}

	// No restarts on forced rewrites in Jython, so the plan's restarts are not applied here.
	logger.InfoContext(ctx, "Completed forced run", "program", progname)

	return 0
}

// isIdlePoll returns true when the loop should skip a config reload due to inactivity.
// Never skips when a reload signal was received — systemd expects READY=1 after RELOADING=1.
func isIdlePoll(
	cfg *config.Config,
	appState *state.State,
	trigger *MainLoopActionTrigger,
	lastEventCount int64,
	reloadSignaled bool,
) bool {
	return cfg.SkipIdlePolls && !appState.IsFirstRun() && !reloadSignaled && trigger.EventCounter.Load() == lastEventCount
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
		// A cancelled context is a shutdown, not a failed lookup: the caller exits
		// instead of sleeping, so don't raise an alerting ERROR for it.
		if ctx.Err() == nil {
			logger.ErrorContext(ctx, "Key lookup failed, sleeping", "error", err, "sleep_seconds", 60)
		}

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
		if ctx.Err() == nil {
			logger.ErrorContext(ctx, "Failed to parse MTA config (sleeping 60s)", "error", err)
			configManager.Metrics().LoadFailed("mta_config")
		}

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
	if !appState.IsFirstRun() || appState.Forced != 0 || server != nil || args.Once {
		return server
	}

	snap := appState.Snapshot()
	listenerPortStr, _ := snap.Local("zmconfigd_listen_port")
	listenerPort, _ := strconv.Atoi(listenerPortStr)
	listenerAddr := "127.0.0.1"
	ipv6 := false

	ipMode, _ := snap.Server("zimbraIPMode")
	if ipMode == "ipv6" {
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

// notifyReady sends sd_notify READY=1. The first one moves the unit from
// "activating" to "active"; later ones acknowledge a reload (the RELOADING=1
// sent by the signal handler needs a READY=1 to end the reload).
func notifyReady(ctx context.Context, notifier *sdnotify.Notifier, first bool) {
	if err := notifier.Ready(); err != nil {
		logger.ErrorContext(ctx, "Failed to send sd_notify READY", "error", err)
	} else if notifier.Enabled() {
		if first {
			logger.InfoContext(ctx, "Sent sd_notify READY=1 to systemd")
		} else {
			logger.DebugContext(ctx, "Sent sd_notify READY=1 to systemd after reload")
		}
	}
}

// signalReady sends READY=1 once the daemon is serving (the listener only
// starts after the first completed config pass, as in configd.py) and again
// whenever a reload was handled, whether it succeeded or not: systemd stays in
// "reloading" until it sees READY=1.
func signalReady(ctx context.Context, deps *daemonLoopDeps, st *loopIterState) {
	switch {
	case !st.ready && st.server != nil:
		st.ready = true

		notifyReady(ctx, deps.notifier, true)
	case st.ready && st.reloadSignaled:
		notifyReady(ctx, deps.notifier, false)
	}
}

// phaseTimings carries the elapsed time of each runConfigPhases step for later logging.
type phaseTimings struct {
	buildDeps time.Duration
	plan      time.Duration
	rewrites  time.Duration
}

// phaseResult is how the dependency, plan and execute phases of a cycle ended.
type phaseResult int

const (
	// phasesCompleted: the plan was executed without error.
	phasesCompleted phaseResult = iota
	// phasesPlanFailed: planning failed; nothing ran and the requests stay queued.
	phasesPlanFailed
	// phasesRewriteFailed: the plan ran but a rewrite step failed. The failed
	// actions and the plan's restarts are carried to the next cycle.
	phasesRewriteFailed
)

// runConfigPhases executes the per-iteration dependency, plan and execute phases.
// It returns the phase timings, the plan of the cycle and how the phases ended;
// the caller backs off after phasesPlanFailed and phasesRewriteFailed. REWRITE
// clients are released once the rewrite phase is over, as lAction.notifyAll()
// does in configd.py, even when it failed.
func runConfigPhases(
	ctx context.Context, deps *daemonLoopDeps,
) (timings phaseTimings, pl *plan.Plan, result phaseResult) {
	mainCfg, appState := deps.cfg, deps.appState
	configManager, serviceManager := deps.configManager, deps.serviceManager
	wd := deps.wd

	phaseStart := time.Now()

	buildServiceDependencies(ctx, serviceManager, appState)

	timings.buildDeps = time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: buildServiceDependencies completed",
		"duration_seconds", timings.buildDeps.Seconds(),
		"operation", "build_service_dependencies")

	phaseStart = time.Now()

	// Plan takes the requested sections: the clients registered until now wait
	// for this cycle.
	deps.trigger.claimWaiters()

	pl, err := configManager.Plan(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Configuration inconsistency detected (sleeping 60s)", "error", err)
		// The sections were queued again: legacy leaves the clients waiting
		// until the request is processed or they time out.
		deps.trigger.requeueWaiters()

		return timings, nil, phasesPlanFailed
	}

	timings.plan = time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: Plan completed",
		"duration_seconds", timings.plan.Seconds(),
		"operation", "plan")

	if appState.IsFirstRun() {
		phaseStart = time.Now()

		updateWatchdogServices(ctx, wd, pl.Tracked, mainCfg)

		logger.DebugContext(ctx, "Timing: updateWatchdogServices completed",
			"duration_seconds", time.Since(phaseStart).Seconds(),
			"operation", "update_watchdog_services")
	}

	phaseStart = time.Now()

	result = phasesCompleted

	if err := configManager.Execute(ctx, pl); err != nil {
		logger.ErrorContext(ctx, "Error during config rewrites", "error", err)

		result = phasesRewriteFailed
	}

	deps.trigger.completeWaiters()

	timings.rewrites = time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: Execute completed",
		"duration_seconds", timings.rewrites.Seconds(),
		"operation", "execute_plan")

	return timings, pl, result
}

// daemonLoopDeps bundles dependencies of the daemon main loop.
type daemonLoopDeps struct {
	env            sys.System
	cfg            *config.Config
	appState       *state.State
	configManager  *configmgr.ConfigManager
	serviceManager services.Manager
	wd             *watchdog.Watchdog
	args           *Args
	notifier       *sdnotify.Notifier
	trigger        *MainLoopActionTrigger
	reloadChan     chan struct{}
	// validate checks the service hooks against the first parsed zmconfigd.cf;
	// a failure ends the daemon. Nil (hosts, tests) skips the check.
	validate func(conf *config.MtaConfig, watchdogOn bool) error
	// ldapSource replaces the LDAP client as the source the LDAP change
	// watcher reads fingerprints from; production leaves it nil. ldapSubscriber
	// replaces the content-sync stream the same way and is only used together
	// with ldapSource (nil: the watcher polls).
	ldapSource     ldapwatch.Source
	ldapSubscriber ldapwatch.Subscriber
}

// loopIterState carries mutable per-iteration counters between runs.
type loopIterState struct {
	lastEventCount int64
	loopCount      int
	reloadSignaled bool
	server         *network.ThreadedStreamServer
	ldapWatch      *ldapWatch // nil when zmconfigd_ldap_change_trigger is off or in --once mode
	// ready records that READY=1 was sent: the listener is up, which only
	// happens after the first completed config pass.
	ready        bool
	hooksChecked bool // the service hooks were validated against the parsed config
	exitCode     int  // the status the daemon exits with
}

// iterOutcome signals how runLoopIteration wants the caller to proceed.
type iterOutcome int

const (
	iterContinue iterOutcome = iota
	iterExit
)

// runDaemonLoop is the main event loop for the configd daemon. It returns the
// exit status: 1 when the service hooks do not cover the configuration.
func runDaemonLoop(ctx context.Context, deps *daemonLoopDeps) int {
	st := &loopIterState{}

	defer func() {
		st.ldapWatch.stop()

		if st.server != nil {
			st.server.Shutdown(ctx)
		}
	}()

	for {
		outcome := runLoopIterationSafe(ctx, deps, st)
		if outcome == iterExit {
			return st.exitCode
		}
	}
}

// runLoopIterationSafe wraps runLoopIteration with panic recovery. After a
// recovered panic it backs off for the normal interval (interruptible by
// shutdown or reload) so a deterministic panic cannot spin the loop hot.
func runLoopIterationSafe(ctx context.Context, deps *daemonLoopDeps, st *loopIterState) (outcome iterOutcome) {
	completed := false

	// Deferred first, so it runs after recoverGoroutine has recovered the panic.
	defer func() {
		if completed {
			return
		}

		outcome = iterContinue

		logger.ErrorContext(ctx, "Main loop iteration panicked, backing off",
			"interval_seconds", deps.cfg.Interval)
		SleepWithContext(ctx, deps.env.Clock, time.Duration(deps.cfg.Interval)*time.Second, deps.reloadChan)
	}()

	defer recoverGoroutine(ctx, "main-loop")

	outcome = runLoopIteration(ctx, deps, st)
	completed = true

	return outcome
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

	st.lastEventCount = deps.trigger.EventCounter.Load()
	t1 := time.Now()

	loadDur, parseDur, err := runLoadAndParse(ctx, deps.configManager, deps.cfg)
	if err != nil {
		exportMetrics(ctx, deps)
		backOff(ctx, deps, st)

		return iterContinue
	}

	if !checkHooks(ctx, deps, st) {
		return iterExit
	}

	st.ldapWatch = syncLDAPWatch(ctx, deps, st.ldapWatch)

	return runCycle(ctx, deps, st, &loopPhaseDurations{t1: t1, load: loadDur, parse: parseDur})
}

// runCycle runs the plan, rewrite and restart phases of an iteration whose
// configuration was loaded and parsed, then starts the listener and finalizes.
// dur carries the timings measured so far; the later phases are added to it.
func runCycle(ctx context.Context, deps *daemonLoopDeps, st *loopIterState, dur *loopPhaseDurations) iterOutcome {
	timings, pl, result := runConfigPhases(ctx, deps)
	if result != phasesCompleted {
		exportMetrics(ctx, deps)

		if result == phasesRewriteFailed && deps.args.Once {
			return iterExit
		}

		backOff(ctx, deps, st)

		return iterContinue
	}

	if ctxDone(ctx) {
		logger.InfoContext(ctx, "Shutdown detected after config rewrites. Exiting main loop.",
			"reason", "shutdown_signal")

		return iterExit
	}

	restartsDur := runRestartsPhase(ctx, deps.configManager, pl)

	// As in configd.py, the listener starts only after the first rewrite and
	// restart pass, so a start script waiting on the service (READY=1, STATUS)
	// does not return before the configuration files are written.
	st.server = maybeStartListener(ctx, deps.appState, deps.args, deps.trigger, st.server)

	dur.config = timings
	dur.restarts = restartsDur
	dur.plan = pl

	return finalizeIteration(ctx, deps, st, dur)
}

// backOff ends a cycle that failed to load, plan or rewrite: configd.py
// sleeps 60s and tries again. A failed rewrite phase skips the restarts of the
// cycle (the plan's restarts are carried to the retry) and the failed cycle
// does not start the listener. A reload that was being handled is
// acknowledged to systemd before the sleep.
func backOff(ctx context.Context, deps *daemonLoopDeps, st *loopIterState) {
	signalReady(ctx, deps, st)

	if SleepWithContext(ctx, deps.env.Clock, 60*time.Second, deps.reloadChan) {
		st.reloadSignaled = true
	}
}

// checkHooks validates the service hooks against the parsed zmconfigd.cf once.
// It returns false, with the exit status set to 1, when the hooks do not cover
// the configuration.
func checkHooks(ctx context.Context, deps *daemonLoopDeps, st *loopIterState) bool {
	if st.hooksChecked || deps.validate == nil {
		return true
	}

	if err := deps.validate(deps.appState.GetMtaConfig(), deps.cfg.Watchdog); err != nil {
		logger.ErrorContext(ctx, "Service hooks do not cover the configuration, exiting", "error", err)

		st.exitCode = 1

		return false
	}

	st.hooksChecked = true

	return true
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

	if SleepWithContext(ctx, deps.env.Clock, time.Duration(deps.cfg.Interval)*time.Second, deps.reloadChan) {
		st.reloadSignaled = true
	}
}

// runRestartsPhase hands the plan's restarts to the restart policy and returns
// the elapsed time. The policy runs every cycle: with restarts disabled it only
// publishes the dependency graph and keeps the restarts pending.
func runRestartsPhase(ctx context.Context, cm *configmgr.ConfigManager, pl *plan.Plan) time.Duration {
	phaseStart := time.Now()

	cm.ApplyRestarts(ctx, pl)

	dur := time.Since(phaseStart)

	logger.DebugContext(ctx, "Timing: Restarts completed",
		"duration_seconds", dur.Seconds(),
		"operation", "restarts")

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
	plan     *plan.Plan
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

	// The first completed pass makes the daemon ready; a handled reload is
	// acknowledged (READY=1 again) before the flag is cleared.
	signalReady(ctx, deps, st)

	st.reloadSignaled = false
	st.loopCount++

	_ = deps.notifier.Status("loop %d completed in %.1fs, next in %ds",
		st.loopCount, lt.Seconds(), deps.cfg.Interval)

	logger.DebugContext(ctx, "Timing: Loop timing breakdown",
		"load_configs_seconds", dur.load.Seconds(),
		"parse_mta_seconds", dur.parse.Seconds(),
		"build_deps_seconds", dur.config.buildDeps.Seconds(),
		"plan_seconds", dur.config.plan.Seconds(),
		"rewrites_seconds", dur.config.rewrites.Seconds(),
		"restarts_seconds", dur.restarts.Seconds())
	logger.InfoContext(ctx, "Loop completed", "total_duration_seconds", lt.Seconds())

	observeCycle(ctx, deps, st.loopCount, dur, lt)

	if deps.args.Once {
		logger.InfoContext(ctx, "Single-run mode: Exiting after one loop completion",
			"total_duration_seconds", lt.Seconds())

		return iterExit
	}

	logger.DebugContext(ctx, "Sleeping for interval", "interval_seconds", deps.cfg.Interval)

	if SleepWithContext(ctx, deps.env.Clock, time.Duration(deps.cfg.Interval)*time.Second, deps.reloadChan) {
		st.reloadSignaled = true
	}

	return iterContinue
}

// RunMainLoop contains the core logic of the configd daemon.
// It returns an exit code: 0 for success, 1 for error.
func RunMainLoop(ctx context.Context, rt *runtime, args *Args, notifier *sdnotify.Notifier) int {
	ctx = logger.ContextWithComponent(ctx, "mainloop")
	// Create cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	wd := bootstrapWatchdog(ctx, rt)

	// Bootstrap systemd integration
	reloadChan := bootstrapSystemd(ctx, rt.env, rt.state, cancel, notifier)

	// Start watchdog in daemon mode (not for forced configs)
	if !args.HasForcedConfigs() {
		wd.Start(ctx)
		defer wd.Stop(ctx)
	}

	mainLoopTrigger := &MainLoopActionTrigger{
		ReloadChan: reloadChan,
		State:      rt.state,
	}

	if args.HasForcedConfigs() {
		// Watchdog was never started in forced config mode, deferred wd.Stop() will not run
		return runForcedMode(ctx, args, rt)
	}

	return runDaemonLoop(ctx, &daemonLoopDeps{
		env:            rt.env,
		cfg:            rt.cfg,
		appState:       rt.state,
		configManager:  rt.cm,
		serviceManager: rt.sm,
		wd:             wd,
		args:           args,
		notifier:       notifier,
		trigger:        mainLoopTrigger,
		reloadChan:     reloadChan,
		validate:       rt.validateHooks,
	})
}

// sectionDependencies maps each section to the services that depend on it
// (its DEPENDS list): the table the service manager cascades restarts through.
func sectionDependencies(sections map[string]*config.MtaConfigSection) map[string][]string {
	deps := make(map[string][]string)

	for name, section := range sections {
		if len(section.Depends) > 0 {
			deps[name] = slices.Sorted(maps.Keys(section.Depends))
		}
	}

	return deps
}

// buildServiceDependencies extracts dependencies from MTA config sections and sets them in the service manager.
func buildServiceDependencies(ctx context.Context, serviceMgr services.Manager, appState *state.State) {
	ctx = logger.ContextWithComponent(ctx, "mainloop")

	sections := appState.GetMtaConfig().Sections
	if sections == nil {
		return
	}

	deps := sectionDependencies(sections)

	serviceMgr.SetDependencies(ctx, deps)
	logger.DebugContext(ctx, "Set service dependencies",
		"section_count", len(deps))
}

// updateWatchdogServices updates the watchdog with currently tracked services.
// This is called on first run to notify watchdog about services that should be monitored.
func updateWatchdogServices(
	ctx context.Context, wd *watchdog.Watchdog, tracked map[string]string, mainCfg *config.Config,
) {
	ctx = logger.ContextWithComponent(ctx, "mainloop")
	// Get watchdog service list from config (defaults to ["antivirus"])
	watchdogServices := mainCfg.WdList
	if len(watchdogServices) == 0 {
		watchdogServices = []string{componentAntivirus}
	}

	// Enable monitoring for configured watchdog services
	wd.UpdateServiceList(ctx, watchdogServices)

	// For services that are currently tracked and in the watchdog list, mark them as available
	for service := range tracked {
		// Check if service is in watchdog list
		isInWatchdogList := slices.Contains(watchdogServices, service)

		if isInWatchdogList {
			// Mark service as available for watchdog
			wd.AddService(ctx, service)
		}
	}
}
