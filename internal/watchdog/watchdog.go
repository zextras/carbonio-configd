// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package watchdog provides automatic service monitoring and restart functionality.
// It periodically checks tracked services and attempts automatic recovery when
// services fail, with configurable check intervals and service lists.
package watchdog

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/metrics"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/systemd"
)

// SnapshotProvider scopes one process-table snapshot to a context so every
// status probe of a watchdog tick answers from the same view. It is satisfied
// by *services.Controller.
type SnapshotProvider interface {
	// WithProcessSnapshot returns ctx carrying a process-table snapshot.
	WithProcessSnapshot(ctx context.Context) context.Context
}

// Watchdog monitors services and automatically restarts them on failure.
type Watchdog struct {
	mu sync.Mutex

	// State reference for watchdog process tracking
	state *state.State

	// ServiceManager for controlling services
	serviceManager services.Manager

	// checkInterval is the interval between health checks; fixed at
	// construction (Config.CheckInterval), so it needs no lock.
	checkInterval time.Duration

	// enabled controls whether watchdog is active
	enabled bool

	// stopChan signals the current run loop to stop. Recreated by every Start
	// so the watchdog can be restarted after Stop.
	stopChan chan struct{}

	// runningChan is closed by the current run loop when it exits. Recreated
	// by every Start together with stopChan.
	runningChan chan struct{}

	// serviceEnabled is the set of services the watchdog monitors
	// (zmconfigd_watchdog_services); guarded by mu.
	serviceEnabled map[string]bool

	// prevServices records each monitored service's last observed run state
	// ("running"/"stopped") so status flips can be logged per cycle, mirroring
	// jylibs configd.py prevServices tracking.
	prevServices map[string]string

	// configLookup function for checking service configuration
	configLookup func(string) string

	// clock drives the check ticker.
	clock sys.Clock

	// snapshots scopes a process-table snapshot to each tick; nil probes live.
	snapshots SnapshotProvider

	// metrics counts watchdog restarts; nil records nothing.
	metrics *metrics.Registry

	// systemd makes the watchdog a no-op when it reports a systemd host: the
	// units' Restart= policy recovers crashed services. Nil: never.
	systemd systemd.Mode
}

// Config holds configuration for the Watchdog.
type Config struct {
	// CheckInterval is how often to check service health
	CheckInterval time.Duration

	// ServiceManager is the service controller
	ServiceManager services.Manager

	// State is the global state object
	State *state.State

	// ConfigLookup function for checking service configuration
	ConfigLookup func(string) string

	// Clock paces the health-check ticker (sys.Real().Clock in production).
	// Required: NewWatchdog does not default it.
	Clock sys.Clock

	// Snapshots gives each tick one shared process-table snapshot (usually the
	// *services.Controller behind ServiceManager). Nil makes every probe read
	// the live process table.
	Snapshots SnapshotProvider

	// Systemd tells whether the host's services are managed through systemd
	// units (the daemon's service manager answers). The watchdog then does
	// nothing (Start and Stop are no-ops): the units' Restart= handles
	// crashes, as legacy zmconfigd relied on it. Nil is a legacy host.
	Systemd systemd.Mode
}

// NewWatchdog creates a new Watchdog instance.
func NewWatchdog(cfg Config) *Watchdog { //nolint:gocritic // hugeParam: construction-time value object
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 60 * time.Second // Default: 60 seconds
	}

	return &Watchdog{
		state:          cfg.State,
		serviceManager: cfg.ServiceManager,
		checkInterval:  cfg.CheckInterval,
		enabled:        false,
		serviceEnabled: make(map[string]bool),
		prevServices:   make(map[string]string),
		configLookup:   cfg.ConfigLookup,
		clock:          cfg.Clock,
		snapshots:      cfg.Snapshots,
		systemd:        cfg.Systemd,
	}
}

// SetMetrics makes the watchdog count the restarts it attempts in reg. Call it
// before Start; nil (the default) records nothing.
func (w *Watchdog) SetMetrics(reg *metrics.Registry) {
	w.metrics = reg
}

// Start begins the watchdog monitoring loop. In systemd mode it only logs that
// the watchdog is off.
func (w *Watchdog) Start(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	if systemd.Managed(w.systemd) {
		logger.InfoContext(ctx, "Watchdog disabled in systemd mode: unit Restart= policies recover crashed services")

		return
	}

	w.mu.Lock()

	if w.enabled {
		w.mu.Unlock()
		logger.WarnContext(ctx, "Watchdog already running")

		return
	}

	// A previous loop whose Stop gave up waiting (ctx done) may still be in
	// its last cycle: the new loop waits for it so two never check at once.
	previous := w.runningChan

	w.enabled = true
	w.stopChan = make(chan struct{})
	w.runningChan = make(chan struct{})
	stopChan, runningChan := w.stopChan, w.runningChan
	w.mu.Unlock()

	logger.DebugContext(ctx, "Starting watchdog",
		"check_interval", w.checkInterval)

	go w.run(ctx, stopChan, runningChan, previous)
}

// Stop halts the watchdog monitoring loop and waits for it to finish, or
// until ctx is done, whichever comes first. The loop still exits on its own
// once its current cycle completes. Start may be called again afterwards.
func (w *Watchdog) Stop(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	if systemd.Managed(w.systemd) {
		return
	}

	w.mu.Lock()

	if !w.enabled {
		w.mu.Unlock()
		logger.WarnContext(ctx, "Watchdog not running")

		return
	}

	w.enabled = false
	stopChan, runningChan := w.stopChan, w.runningChan
	w.mu.Unlock()

	logger.DebugContext(ctx, "Stopping watchdog")
	close(stopChan)

	select {
	case <-runningChan:
	case <-ctx.Done():
		logger.WarnContext(ctx, "Watchdog stop interrupted before the monitoring loop exited",
			"error", ctx.Err())
	}
}

// IsServiceEnabled returns whether a service is monitored by watchdog.
func (w *Watchdog) IsServiceEnabled(service string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.serviceEnabled[service]
}

// AddService adds a service to watchdog tracking.
// This is called after a service is successfully started.
func (w *Watchdog) AddService(ctx context.Context, service string) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.state.SetWatchdog(service, true)
	logger.DebugContext(ctx, "Service now available for watchdog",
		"service", service)
}

// RemoveService removes a service from watchdog tracking.
// This is called when a service is explicitly stopped or restarted to prevent restart loops.
func (w *Watchdog) RemoveService(ctx context.Context, service string) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.state.DelWatchdog(service)
	logger.DebugContext(ctx, "Removed service from watchdog tracking",
		"service", service)
}

// IsServiceTracked returns whether a service is currently tracked by watchdog.
func (w *Watchdog) IsServiceTracked(service string) bool {
	status := w.state.GetWatchdog(service)
	return status != nil && *status
}

// run is the main watchdog monitoring loop. previous is the running channel
// of the loop this one replaces (nil for the first): run does not begin before
// that loop has exited.
func (w *Watchdog) run(
	ctx context.Context, stopChan <-chan struct{}, runningChan chan<- struct{}, previous <-chan struct{},
) {
	defer close(runningChan)

	if previous != nil {
		select {
		case <-previous:
		case <-stopChan:
			return
		case <-ctx.Done():
			return
		}
	}

	ticker := w.clock.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			logger.DebugContext(ctx, "Watchdog stopped")

			return
		case <-ticker.C():
			// A stop wins over a tick that fired at the same time: a loop
			// being stopped must not start another cycle.
			select {
			case <-stopChan:
				logger.DebugContext(ctx, "Watchdog stopped")

				return
			default:
			}

			w.checkServices(ctx)
		}
	}
}

// checkServices iterates through tracked services and checks their health.
func (w *Watchdog) checkServices(ctx context.Context) {
	w.mu.Lock()

	if !w.enabled {
		w.mu.Unlock()
		return
	}

	w.mu.Unlock()

	logger.DebugContext(ctx, "Checking service health")

	// Every status probe of this tick shares one process-table snapshot, so all
	// restart decisions come from the same view and /proc is walked once.
	// Restarts (which re-check status before starting) keep the caller's ctx
	// and see live process state.
	probeCtx := ctx
	if w.snapshots != nil {
		probeCtx = w.snapshots.WithProcessSnapshot(ctx)
	}

	observed := w.logServiceStatusChanges(probeCtx)

	for _, service := range w.getTrackedServices() {
		w.checkOneService(ctx, probeCtx, service, observed)
	}

	w.rearmRunningServices(ctx, observed)
}

// rearmRunningServices starts watching again every monitored service this tick
// saw running that is not tracked. A service leaves the tracking when it is
// restarted on purpose (the service manager's BeforeRestart); as in legacy
// configd, the next status check that sees it running makes it eligible for
// restarts again. A service seen down is left alone: whoever stopped it owns it.
func (w *Watchdog) rearmRunningServices(ctx context.Context, observed map[string]bool) {
	for _, service := range slices.Sorted(maps.Keys(observed)) {
		if observed[service] && !w.IsServiceTracked(service) {
			w.AddService(ctx, service)
		}
	}
}

// logServiceStatusChanges emits a diagnostic line whenever a monitored
// service's running state flips between checks, mirroring the
// "Service status change" log in jylibs configd.py watchdog(). It is purely
// observational: it never queues restarts. prevServices is seeded on the first
// observation so only genuine transitions (not the initial reading) are logged.
//
// Only monitored services enabled on this node are probed, as legacy configd
// iterates curServices(): probing e.g. antivirus on a node without it is
// useless work. The returned map holds each probed service's running state so
// checkOneService can reuse it instead of probing again in the same cycle.
// ctx is the tick's probe context (services.WithProcessSnapshot).
func (w *Watchdog) logServiceStatusChanges(ctx context.Context) map[string]bool {
	w.mu.Lock()

	monitored := slices.Sorted(maps.Keys(w.serviceEnabled))
	w.mu.Unlock()

	hostname := ""
	if w.configLookup != nil {
		hostname = w.configLookup("zimbra_server_hostname")
	}

	observed := make(map[string]bool, len(monitored))

	for _, service := range monitored {
		if !w.isEnabledOnNode(service) {
			continue
		}

		running, err := w.serviceManager.IsRunning(ctx, service)
		if err != nil {
			// Don't record a status when the probe itself failed.
			continue
		}

		observed[service] = running

		curStatus := "stopped"
		if running {
			curStatus = "running"
		}

		w.mu.Lock()
		prevStatus, seen := w.prevServices[service]
		w.prevServices[service] = curStatus
		w.mu.Unlock()

		if seen && prevStatus != curStatus {
			logger.InfoContext(ctx, "Service status change",
				"hostname", hostname,
				"service", service,
				"from", prevStatus,
				"to", curStatus)
		}
	}

	return observed
}

// isEnabledOnNode reports whether service is in this node's
// zimbraServiceEnabled, read lock-free from the latest published snapshot.
func (w *Watchdog) isEnabledOnNode(service string) bool {
	if w.state == nil {
		return false
	}

	return w.state.Snapshot().ServiceEnabled(service)
}

// checkOneService verifies a single service and queues a restart when it is not running.
// observed holds running states already probed in this cycle; a missing entry
// triggers a probe through probeCtx, the tick's snapshot context, so it
// answers from the same process-table view as the rest of the cycle; ctx is
// the caller's live context, used for restarts.
func (w *Watchdog) checkOneService(ctx, probeCtx context.Context, service string, observed map[string]bool) {
	if !w.IsServiceEnabled(service) {
		logger.DebugContext(ctx, "Skipping disabled service", "service", service)

		return
	}

	if !w.IsServiceTracked(service) {
		logger.DebugContext(ctx, "Skipping service not yet available for restarts", "service", service)

		return
	}

	isRunning, probed := observed[service]
	if !probed {
		var err error

		isRunning, err = w.serviceManager.IsRunning(probeCtx, service)
		if err != nil {
			logger.WarnContext(ctx, "Failed to check service", "service", service, "error", err)

			return
		}
	}

	if isRunning {
		logger.DebugContext(ctx, "Service verified running", "service", service)

		return
	}

	// A config-driven restart may have taken the service over since the
	// probe; the service manager untracked it then.
	if !w.IsServiceTracked(service) {
		logger.DebugContext(ctx, "Service is being restarted elsewhere, skipping", "service", service)

		return
	}

	w.restartDownService(ctx, service)
}

// restartDownService removes the service from watchdog tracking, queues a restart
// and watches the service again once the attempt is over, whether it succeeded
// or not: a service that is still down after a failed restart must stay
// eligible so a later tick retries it (the untracking only keeps the watchdog
// from queueing a second restart while this one runs). A shutdown interrupting
// the attempt leaves the service untracked; nothing runs afterwards.
func (w *Watchdog) restartDownService(ctx context.Context, service string) {
	logger.WarnContext(ctx, "Service not running, attempting restart", "service", service)

	w.RemoveService(ctx, service)

	if err := w.serviceManager.AddRestart(ctx, service); err != nil {
		logger.ErrorContext(ctx, "Failed to queue restart", "service", service, "error", err)
		w.metrics.WatchdogRestart(service, false)
		w.AddService(ctx, service)

		return
	}

	if err := w.serviceManager.ProcessRestarts(ctx, w.configLookup); err != nil {
		// Interrupted by a shutdown (already logged by the manager): neither
		// an error nor a failed restart.
		if ctx.Err() != nil {
			return
		}

		logger.ErrorContext(ctx, "Failed to restart service", "service", service, "error", err)
		w.metrics.WatchdogRestart(service, false)
		w.AddService(ctx, service)

		return
	}

	w.metrics.WatchdogRestart(service, true)
	logger.InfoContext(ctx, "Successfully restarted service", "service", service)
	w.AddService(ctx, service)
}

// getTrackedServices returns the monitored services that are currently tracked
// by the watchdog, in name order. The monitored list is the one configured in
// zmconfigd_watchdog_services (UpdateServiceList), as in legacy configd's
// watchdog(), which walks wd_list.
func (w *Watchdog) getTrackedServices() []string {
	w.mu.Lock()
	monitored := slices.Sorted(maps.Keys(w.serviceEnabled))
	w.mu.Unlock()

	// State methods handle their own locking.
	return slices.DeleteFunc(monitored, func(service string) bool { return !w.IsServiceTracked(service) })
}

// UpdateServiceList updates the list of services that should be monitored.
// This should be called after configuration changes.
func (w *Watchdog) UpdateServiceList(ctx context.Context, enabledServices []string) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.mu.Lock()
	defer w.mu.Unlock()

	logger.DebugContext(ctx, "Updating watchdog service list",
		"service_count", len(enabledServices))

	// Reset enabled map
	w.serviceEnabled = make(map[string]bool, len(enabledServices))

	// Enable all provided services
	for _, service := range enabledServices {
		w.serviceEnabled[service] = true
	}
}
