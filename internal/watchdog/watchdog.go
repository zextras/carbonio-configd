// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package watchdog provides automatic service monitoring and restart functionality.
// It periodically checks tracked services and attempts automatic recovery when
// services fail, with configurable check intervals and service lists.
package watchdog

import (
	"context"
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

	// stopChan signals the watchdog to stop
	stopChan chan struct{}

	// runningChan indicates watchdog is running
	runningChan chan struct{}

	// ServiceEnabled maps service names to their enabled status
	ServiceEnabled map[string]bool

	// configLookup function for checking service configuration
	configLookup func(string) string
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
		stopChan:       make(chan struct{}),
		runningChan:    make(chan struct{}),
		ServiceEnabled: make(map[string]bool),
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
	w.mu.Unlock()

	logger.DebugContext(ctx, "Starting watchdog",
		"check_interval", w.CheckInterval)

	go w.run(ctx)
}

// Stop halts the watchdog monitoring loop.
func (w *Watchdog) Stop(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.mu.Lock()

	if !w.enabled {
		w.mu.Unlock()
		logger.WarnContext(ctx, "Watchdog not running")

		return
	}

	w.enabled = false
	w.mu.Unlock()

	logger.DebugContext(ctx, "Stopping watchdog")
	close(w.stopChan)
	<-w.runningChan // Wait for run() to finish
}

// IsEnabled returns whether the watchdog is currently active.
func (w *Watchdog) IsEnabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.enabled
}

// SetServiceEnabled sets whether a specific service should be monitored by watchdog.
func (w *Watchdog) SetServiceEnabled(ctx context.Context, service string, enabled bool) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.mu.Lock()
	defer w.mu.Unlock()

	w.ServiceEnabled[service] = enabled
	logger.DebugContext(ctx, "Watchdog monitoring status changed",
		"service", service,
		"enabled", enabled)
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
func (w *Watchdog) run(ctx context.Context) {
	defer close(w.runningChan)

	ticker := time.NewTicker(w.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
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

	for _, service := range w.getTrackedServices() {
		w.checkOneService(ctx, service)
	}
}

// checkOneService verifies a single service and queues a restart when it is not running.
func (w *Watchdog) checkOneService(ctx context.Context, service string) {
	if !w.IsServiceEnabled(service) {
		logger.DebugContext(ctx, "Skipping disabled service", "service", service)

		return
	}

	if !w.IsServiceTracked(service) {
		logger.DebugContext(ctx, "Skipping service not yet available for restarts", "service", service)

		return
	}

	isRunning, err := w.serviceManager.IsRunning(ctx, service)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to check service", "service", service, "error", err)

		return
	}

	if isRunning {
		logger.DebugContext(ctx, "Service verified running", "service", service)

		return
	}

	w.restartDownService(ctx, service)
}

// restartDownService removes the service from watchdog tracking, queues a restart,
// and re-adds it on success.
func (w *Watchdog) restartDownService(ctx context.Context, service string) {
	logger.WarnContext(ctx, "Service not running, attempting restart", "service", service)

	w.RemoveService(ctx, service)

	if err := w.serviceManager.AddRestart(ctx, service); err != nil {
		logger.ErrorContext(ctx, "Failed to queue restart", "service", service, "error", err)

		return
	}

	if err := w.serviceManager.ProcessRestarts(ctx, w.configLookup); err != nil {
		logger.ErrorContext(ctx, "Failed to restart service", "service", service, "error", err)
	} else {
		logger.InfoContext(ctx, "Successfully restarted service", "service", service)
		w.AddService(ctx, service)
	}
}

// getTrackedServices returns a list of all services currently tracked by watchdog.
func (w *Watchdog) getTrackedServices() []string {
	// Collect all services that are tracked
	// Note: State methods handle their own locking
	commonServices := []string{
		"ldap", "mailbox", "mailboxd", "memcached", "proxy",
		"antispam", "antivirus", "cbpolicyd", "amavis", "opendkim",
		"mta", "sasl", "stats",
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
