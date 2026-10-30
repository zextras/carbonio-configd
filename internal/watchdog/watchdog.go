// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package watchdog provides automatic service monitoring and restart functionality.
// It periodically checks tracked services and attempts automatic recovery when
// services fail, with configurable check intervals and service lists.
package watchdog

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
)

// Watchdog monitors services and automatically restarts them on failure.
type Watchdog struct {
	mu sync.Mutex

	// State reference for watchdog process tracking
	state *state.State

	// ServiceManager for controlling services
	serviceManager services.Manager

	// CheckInterval is the interval between health checks
	CheckInterval time.Duration

	// enabled controls whether watchdog is active
	enabled bool

	// stopChan signals the current run loop to stop. Recreated by every Start
	// so the watchdog can be restarted after Stop.
	stopChan chan struct{}

	// runningChan is closed by the current run loop when it exits. Recreated
	// by every Start together with stopChan.
	runningChan chan struct{}

	// ServiceEnabled maps service names to their enabled status
	ServiceEnabled map[string]bool

	// prevServices records each monitored service's last observed run state
	// ("running"/"stopped") so status flips can be logged per cycle, mirroring
	// jylibs configd.py prevServices tracking.
	prevServices map[string]string

	// configLookup function for checking service configuration
	configLookup func(string) string
}

const (
	// serviceLDAP is the LDAP service name.
	serviceLDAP = "ldap"

	// serviceMailbox is the mailbox service name.
	serviceMailbox = "mailbox"

	// serviceMailboxd is the mailboxd service name.
	serviceMailboxd = "mailboxd"

	// serviceMemcached is the memcached service name.
	serviceMemcached = "memcached"

	// serviceProxy is the proxy service name.
	serviceProxy = "proxy"

	// serviceAntispam is the antispam service name.
	serviceAntispam = "antispam"

	// serviceAntivirus is the antivirus service name.
	serviceAntivirus = "antivirus"

	// serviceCbpolicyd is the cbpolicyd service name.
	serviceCbpolicyd = "cbpolicyd"

	// serviceAmavis is the amavis service name.
	serviceAmavis = "amavis"

	// serviceOpendkim is the opendkim service name.
	serviceOpendkim = "opendkim"

	// serviceMTA is the MTA service name.
	serviceMTA = "mta"

	// serviceSasl is the sasl service name.
	serviceSasl = "sasl"

	// serviceStats is the stats service name.
	serviceStats = "stats"
)

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
}

// NewWatchdog creates a new Watchdog instance.
func NewWatchdog(cfg Config) *Watchdog {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 60 * time.Second // Default: 60 seconds
	}

	return &Watchdog{
		state:          cfg.State,
		serviceManager: cfg.ServiceManager,
		CheckInterval:  cfg.CheckInterval,
		enabled:        false,
		ServiceEnabled: make(map[string]bool),
		prevServices:   make(map[string]string),
		configLookup:   cfg.ConfigLookup,
	}
}

// Start begins the watchdog monitoring loop.
func (w *Watchdog) Start(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.mu.Lock()

	if w.enabled {
		w.mu.Unlock()
		logger.WarnContext(ctx, "Watchdog already running")

		return
	}

	w.enabled = true
	w.stopChan = make(chan struct{})
	w.runningChan = make(chan struct{})
	stopChan, runningChan := w.stopChan, w.runningChan
	w.mu.Unlock()

	logger.DebugContext(ctx, "Starting watchdog",
		"check_interval", w.CheckInterval)

	go w.run(ctx, stopChan, runningChan)
}

// Stop halts the watchdog monitoring loop and waits for it to finish, or
// until ctx is done, whichever comes first. The loop still exits on its own
// once its current cycle completes. Start may be called again afterwards.
func (w *Watchdog) Stop(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

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

	return w.ServiceEnabled[service]
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

// run is the main watchdog monitoring loop.
func (w *Watchdog) run(ctx context.Context, stopChan <-chan struct{}, runningChan chan<- struct{}) {
	defer close(runningChan)

	ticker := time.NewTicker(w.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			logger.DebugContext(ctx, "Watchdog stopped")

			return
		case <-ticker.C:
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

	observed := w.logServiceStatusChanges(ctx)

	for _, service := range w.getTrackedServices() {
		w.checkOneService(ctx, service, observed)
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
func (w *Watchdog) logServiceStatusChanges(ctx context.Context) map[string]bool {
	w.mu.Lock()

	monitored := make([]string, 0, len(w.ServiceEnabled))
	for svc := range w.ServiceEnabled {
		monitored = append(monitored, svc)
	}
	w.mu.Unlock()

	sort.Strings(monitored)

	hostname := ""
	if w.configLookup != nil {
		hostname = w.configLookup("zimbra_server_hostname")
	}

	observed := make(map[string]bool, len(monitored))

	// All probes of this tick share one /proc walk. The snapshot context is
	// scoped to the probe loop: restarts (which re-check status before
	// starting) must keep the caller's ctx and see live process state.
	probeCtx := services.WithProcessSnapshot(ctx)

	for _, service := range monitored {
		if !w.isEnabledOnNode(service) {
			continue
		}

		running, err := w.serviceManager.IsRunning(probeCtx, service)
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
// zimbraServiceEnabled (ServerConfig.ServiceConfig).
func (w *Watchdog) isEnabledOnNode(service string) bool {
	if w.state == nil || w.state.ServerConfig == nil {
		return false
	}

	_, ok := w.state.ServerConfig.ServiceConfig.Get(service)

	return ok
}

// checkOneService verifies a single service and queues a restart when it is not running.
// observed holds running states already probed in this cycle; a missing entry
// triggers a fresh probe.
func (w *Watchdog) checkOneService(ctx context.Context, service string, observed map[string]bool) {
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

		isRunning, err = w.serviceManager.IsRunning(ctx, service)
		if err != nil {
			logger.WarnContext(ctx, "Failed to check service", "service", service, "error", err)

			return
		}
	}

	if isRunning {
		logger.DebugContext(ctx, "Service verified running", "service", service)

		return
	}

	w.restartDownService(ctx, service)
}

// restartDownService removes the service from watchdog tracking, queues a restart,
// and re-adds it on success. On failure the service stays untracked, as in legacy
// configd: controlProcess drops it from watchdogProcess on restart and it only
// becomes eligible again once a later status check sees it running.
func (w *Watchdog) restartDownService(ctx context.Context, service string) {
	logger.WarnContext(ctx, "Service not running, attempting restart", "service", service)

	w.RemoveService(ctx, service)

	if err := w.serviceManager.AddRestart(ctx, service); err != nil {
		logger.ErrorContext(ctx, "Failed to queue restart", "service", service, "error", err)

		return
	}

	if err := w.serviceManager.ProcessRestarts(ctx, w.configLookup); err != nil {
		logger.ErrorContext(ctx, "Failed to restart service", "service", service, "error", err)

		return
	}

	logger.InfoContext(ctx, "Successfully restarted service", "service", service)
	w.AddService(ctx, service)
}

// getTrackedServices returns a list of all services currently tracked by watchdog.
func (w *Watchdog) getTrackedServices() []string {
	// Collect all services that are tracked
	// Note: State methods handle their own locking
	commonServices := []string{
		serviceLDAP, serviceMailbox, serviceMailboxd, serviceMemcached, serviceProxy,
		serviceAntispam, serviceAntivirus, serviceCbpolicyd, serviceAmavis, serviceOpendkim,
		serviceMTA, serviceSasl, serviceStats,
	}

	serviceList := make([]string, 0, len(commonServices))

	for _, service := range commonServices {
		status := w.state.GetWatchdog(service)
		if status != nil && *status {
			serviceList = append(serviceList, service)
		}
	}

	return serviceList
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
	w.ServiceEnabled = make(map[string]bool)

	// Enable all provided services
	for _, service := range enabledServices {
		w.ServiceEnabled[service] = true
	}
}
