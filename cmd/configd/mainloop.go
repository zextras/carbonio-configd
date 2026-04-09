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

// RunMainLoop contains the core logic of the configd daemon.
//
//nolint:gocyclo,cyclop // Main event loop requires high complexity for state management
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

	// Initialize ConfigManager
	configManager := configmgr.NewConfigManager(ctx, mainCfg, appState, ldapClient, cacheInstance)

	// Initialize Service Manager
	serviceManager := services.NewServiceManager()

	// Detect if systemd is enabled by checking Carbonio targets
	systemdManager := systemd.NewManager()
	isSystemdEnabled := systemdManager.IsSystemdEnabled(ctx)

	if isSystemdEnabled {
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

	// Apply disable-restarts flag if provided
	serviceManager.DisableRestarts = args.DisableRestarts

	// Initialize Watchdog with configurable interval
	watchdogInterval := time.Duration(mainCfg.WatchdogInterval) * time.Second
	if watchdogInterval == 0 {
		watchdogInterval = 120 * time.Second // Default to 2 minutes
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

	// Setup signal handling channels
	reloadChan := make(chan struct{}, 1) // Channel to signal config reload
	SetupSignalHandler(appState, cancel, reloadChan, notifier)

	// Variable to hold the network server for graceful shutdown
	var server *network.ThreadedStreamServer

	defer func() {
		if server != nil {
			server.Shutdown(ctx)
		}
	}()

	// Start watchdog in daemon mode (not for forced configs)
	if !args.HasForcedConfigs() {
		wd.Start(ctx)
		// Note: wd.Stop() is called explicitly before os.Exit() in forced config path
		// and will be called automatically when function returns in normal daemon mode
		defer wd.Stop(ctx)
	}

	// Start systemd watchdog keep-alive goroutine if WATCHDOG_USEC is set.
	// Pings at half the interval so we stay well within the WatchdogSec deadline.
	if wdInterval, ok := sdnotify.WatchdogEnabled(); ok {
		pingInterval := wdInterval / 2 //nolint:mnd // half of WatchdogSec is the recommended ping interval
		logger.InfoContext(ctx, "Starting systemd watchdog keep-alive",
			"watchdog_usec", wdInterval,
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
						logger.ErrorContext(ctx, "Failed to send watchdog ping",
							"error", err)
					}
				}
			}
		}()
	}

	// Initialize MainLoopActionTrigger
	mainLoopTrigger := &MainLoopActionTrigger{
		ReloadChan:   reloadChan,
		State:        appState,
		EventCounter: 0,
		Ctx:          ctx,
	}

	// If forced, process rewrites as standalone process
	if args.HasForcedConfigs() {
		logger.InfoContext(ctx, "Processing forced rewrites as standalone process")

		for _, arg := range args.ForcedConfigs {
			appState.Forced++

			logger.InfoContext(ctx, "Adding forced config", "config", arg)
			appState.ForcedConfig[arg] = arg
		}

		// Perform a single run for forced configs
		err := configManager.LoadAllConfigs(ctx)
		if err != nil {
			logger.FatalContext(ctx, "Failed to load configs for forced run", "error", err)
		}
		// Load MTA config after all other configs are loaded
		err = configManager.ParseMtaConfig(ctx, mainCfg.ConfigFile)
		if err != nil {
			logger.FatalContext(ctx, "Failed to parse MTA config for forced run", "error", err)
		}

		// Build dependency map for service manager from MTA config
		buildServiceDependencies(ctx, serviceManager, appState)

		configManager.CompileActions(ctx)

		if err := configManager.DoConfigRewrites(ctx); err != nil {
			logger.ErrorContext(ctx, "Error during forced config rewrites", "error", err)
		}
		// No restarts on forced rewrites in Jython, so skipping DoRestarts here.

		logger.InfoContext(ctx, "Completed forced run", "program", mainCfg.Progname)
		// Watchdog was never started in forced config mode (line 91-94), no defer to worry about
		os.Exit(0) //nolint:gocritic // exitAfterDefer false positive - wd.Stop() defer is in mutually exclusive if block
	}

	// Main daemon loop
	lastEventCount := 0
	loopCount := 0
	reloadSignaled := false // Set when SIGHUP/SIGUSR2 triggers a reload via SleepWithContext

	for {
		// Check for shutdown signal at start of loop
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Context cancelled, exiting main loop", "reason", "shutdown_signal")
			return
		case <-reloadChan:
			logger.InfoContext(ctx, "Received reload signal, re-evaluating configurations")

			reloadSignaled = true
		default:
			// Continue with normal operation
		}

		// Skip config reload if idle polling is enabled and no events were received.
		// Never skip when a reload signal (SIGHUP) was received — systemd expects
		// READY=1 after RELOADING=1 to transition back from "reloading" state.
		if mainCfg.SkipIdlePolls && !appState.FirstRun && !reloadSignaled && mainLoopTrigger.EventCounter == lastEventCount {
			logger.DebugContext(ctx, "Skipping idle config poll",
				"reason", "no_events_since_last_check")
			logger.DebugContext(ctx, "Sleeping", "interval_seconds", mainCfg.Interval)

			if SleepWithContext(ctx, time.Duration(mainCfg.Interval)*time.Second, reloadChan) {
				reloadSignaled = true // May have been woken by SIGHUP; re-check at loop top
				continue
			}

			continue
		}

		lastEventCount = mainLoopTrigger.EventCounter

		t1 := time.Now()

		// Read all the configs
		phaseStart := time.Now()
		err := configManager.LoadAllConfigs(ctx)
		loadConfigDuration := time.Since(phaseStart)
		logger.DebugContext(ctx, "Timing: LoadAllConfigs completed",
			"duration_seconds", loadConfigDuration.Seconds(),
			"operation", "load_configs")

		if err != nil {
			logger.ErrorContext(ctx, "Key lookup failed, sleeping",
				"error", err,
				"sleep_seconds", 60)

			if SleepWithContext(ctx, 60*time.Second, reloadChan) {
				continue // Re-check shutdown/reload signal
			}

			continue
		}

		// Check for shutdown after config load (which can take many seconds)
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Shutdown detected after config load. Exiting main loop.",
				"reason", "shutdown_signal")

			return
		default:
		}

		// Start the network listener on first run after initial config load
		// This makes the service responsive earlier, before rewrites complete
		// Skip listener in single-run mode (--once flag)
		if appState.FirstRun && appState.Forced == 0 && server == nil && !args.Once {
			listenerPort, _ := strconv.Atoi(appState.LocalConfig.Data["zmconfigd_listen_port"])
			listenerAddr := "127.0.0.1"
			ipv6 := false

			if appState.ServerConfig.Data["zimbraIPMode"] == "ipv6" {
				listenerAddr = "::1"
				ipv6 = true
			}

			// Create a ConfigdRequestHandler and pass the MainLoopActionTrigger
			configdRequestHandler := &network.ConfigdRequestHandler{
				ActionTrigger: mainLoopTrigger,
			}
			server = network.NewThreadedStreamServer(listenerAddr, listenerPort, ipv6, configdRequestHandler)

			err := server.ServeForever(ctx)
			if err != nil {
				logger.FatalContext(ctx, "Failed to start listener",
					"error", err,
					"listener_addr", listenerAddr,
					"listener_port", listenerPort)
			}

			logger.InfoContext(ctx, "Network listener started (async)",
				"listener_addr", listenerAddr,
				"listener_port", listenerPort)
		}

		// Load MTA config after all other configs are loaded
		phaseStart = time.Now()
		err = configManager.ParseMtaConfig(ctx, mainCfg.ConfigFile)
		parseMtaDuration := time.Since(phaseStart)
		logger.DebugContext(ctx, "Timing: ParseMtaConfig completed",
			"duration_seconds", parseMtaDuration.Seconds(),
			"operation", "parse_mta_config")

		if err != nil {
			logger.ErrorContext(ctx, "Failed to parse MTA config (sleeping 60s)",
				"error", err)

			if SleepWithContext(ctx, 60*time.Second, reloadChan) {
				continue // Re-check shutdown/reload signal
			}

			continue
		}

		// Check for shutdown after MTA config parse
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Shutdown detected after MTA config parse. Exiting main loop.",
				"reason", "shutdown_signal")

			return
		default:
		}

		// Build dependency map for service manager from MTA config
		phaseStart = time.Now()

		buildServiceDependencies(ctx, serviceManager, appState)

		buildDepsDuration := time.Since(phaseStart)
		logger.DebugContext(ctx, "Timing: buildServiceDependencies completed",
			"duration_seconds", buildDepsDuration.Seconds(),
			"operation", "build_service_dependencies")

		// Watchdog runs automatically in background goroutine
		// No need to call it explicitly here

		// Check for config changes
		phaseStart = time.Now()
		err = configManager.CompareKeys(ctx)
		compareKeysDuration := time.Since(phaseStart)
		logger.DebugContext(ctx, "Timing: CompareKeys completed",
			"duration_seconds", compareKeysDuration.Seconds(),
			"operation", "compare_keys")

		if err != nil {
			logger.ErrorContext(ctx, "Configuration inconsistency detected (sleeping 60s)",
				"error", err)

			if SleepWithContext(ctx, 60*time.Second, reloadChan) {
				continue // Re-check shutdown/reload signal
			}

			continue
		}

		// On first run, notify watchdog about tracked services
		if appState.FirstRun {
			phaseStart = time.Now()

			updateWatchdogServices(ctx, wd, appState, mainCfg)

			updateWatchdogDuration := time.Since(phaseStart)
			logger.DebugContext(ctx, "Timing: updateWatchdogServices completed",
				"duration_seconds", updateWatchdogDuration.Seconds(),
				"operation", "update_watchdog_services")
		}

		// Compile actions (rewrites, restarts, etc.)
		phaseStart = time.Now()

		configManager.CompileActions(ctx)

		compileActionsDuration := time.Since(phaseStart)
		logger.DebugContext(ctx, "Timing: CompileActions completed",
			"duration_seconds", compileActionsDuration.Seconds(),
			"operation", "compile_actions")

		// Execute rewrites/postconf/restarts
		phaseStart = time.Now()

		if err := configManager.DoConfigRewrites(ctx); err != nil {
			logger.ErrorContext(ctx, "Error during config rewrites",
				"error", err)
		}

		rewritesDuration := time.Since(phaseStart)
		logger.DebugContext(ctx, "Timing: DoConfigRewrites completed",
			"duration_seconds", rewritesDuration.Seconds(),
			"operation", "do_config_rewrites")

		// Check for shutdown after rewrites (which can take many seconds)
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Shutdown detected after config rewrites. Exiting main loop.",
				"reason", "shutdown_signal")

			return
		default:
		}

		restartsDuration := time.Duration(0)

		if mainCfg.RestartConfig {
			phaseStart = time.Now()

			configManager.DoRestarts(ctx)

			restartsDuration = time.Since(phaseStart)
			logger.DebugContext(ctx, "Timing: DoRestarts completed",
				"duration_seconds", restartsDuration.Seconds(),
				"operation", "do_restarts")
		}

		appState.SetFirstRun(false)

		lt := time.Since(t1)

		// Send READY=1 to systemd after every loop completion.
		// On first loop this transitions from "activating" to "active".
		// After a SIGHUP reload this transitions from "reloading" back to "active".
		// When already "active" this is an idempotent no-op per sd_notify spec.
		if err := notifier.Ready(); err != nil {
			logger.ErrorContext(ctx, "Failed to send sd_notify READY",
				"error", err)
		} else if notifier.Enabled() && loopCount == 0 {
			logger.InfoContext(ctx, "Sent sd_notify READY=1 to systemd")
		}

		reloadSignaled = false

		loopCount++

		// Update systemd status with loop timing (visible in `systemctl status`)
		_ = notifier.Status("loop %d completed in %.1fs, next in %ds",
			loopCount, lt.Seconds(), mainCfg.Interval)

		logger.DebugContext(ctx, "Timing: Loop timing breakdown",
			"load_configs_seconds", loadConfigDuration.Seconds(),
			"parse_mta_seconds", parseMtaDuration.Seconds(),
			"build_deps_seconds", buildDepsDuration.Seconds(),
			"compare_keys_seconds", compareKeysDuration.Seconds(),
			"compile_actions_seconds", compileActionsDuration.Seconds(),
			"rewrites_seconds", rewritesDuration.Seconds(),
			"restarts_seconds", restartsDuration.Seconds())
		logger.InfoContext(ctx, "Loop completed",
			"total_duration_seconds", lt.Seconds())

		// Exit after one iteration if --once flag is set
		if args.Once {
			logger.InfoContext(ctx, "Single-run mode: Exiting after one loop completion",
				"total_duration_seconds", lt.Seconds())

			return
		}

		logger.DebugContext(ctx, "Sleeping for interval",
			"interval_seconds", mainCfg.Interval)

		// Use context-aware sleep that responds to shutdown/reload signals immediately
		if SleepWithContext(ctx, time.Duration(mainCfg.Interval)*time.Second, reloadChan) {
			reloadSignaled = true // May have been woken by SIGHUP; re-check at loop top
			continue
		}
	}
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
