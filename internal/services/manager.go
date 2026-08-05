// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/intern"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// ServiceManager implements the Manager interface for service control
// operations. ControlProcess and IsRunning delegate entirely to the
// Registry-backed lifecycle in cli.go (ServiceStart/ServiceStop/
// ServiceRestart/ServiceStatus) — the same control path the CLI uses,
// bifurcated internally on IsSystemdMode(). ServiceManager itself only owns
// the restart-queue bookkeeping consumed by watchdog and configmgr's
// restart cascade.
type ServiceManager struct {
	// RestartQueue holds services pending restart
	RestartQueue map[string]bool
	// MaxFailedRestarts is the maximum number of retry attempts for a failed restart
	MaxFailedRestarts int
	// StartOrder defines the order in which services should be started
	StartOrder map[string]int
	// Dependencies maps section names to their dependent services
	Dependencies map[string][]string
	// DisableRestarts globally disables all service restarts (dry-run mode)
	DisableRestarts bool
}

// NewServiceManager creates a new ServiceManager instance.
func NewServiceManager() *ServiceManager {
	return &ServiceManager{
		RestartQueue:      make(map[string]bool),
		MaxFailedRestarts: 3,
		StartOrder:        getDefaultStartOrder(),
		Dependencies:      make(map[string][]string),
		DisableRestarts:   false, // Default: restarts enabled
	}
}

// getDefaultStartOrder returns the default service start order, matching the
// legacy carbonio-core-utils control.pl %startorder hash. The numeric values
// preserve legacy spacing so out-of-tree dependencies that referenced specific
// slots remain wedge-able. MTA is intentionally near the end (150) because it
// depends on amavis/antispam/antivirus/opendkim/cbpolicyd content filters being
// up first. service-discover is a cluster orchestration target and runs early
// so other services can register themselves.
func getDefaultStartOrder() map[string]int {
	return map[string]int{
		svcLdap:            0,
		svcConfigd:         10,
		svcServiceDiscover: 20,
		svcMailbox:         50,
		svcMemcached:       60,
		svcProxy:           70,
		svcAmavis:          75,
		svcAntispam:        80,
		svcAntivirus:       90,
		svcFreshclam:       92,
		svcOpendkim:        100,
		svcCbpolicyd:       120,
		svcSaslauthd:       130,
		svcMilter:          140,
		serviceMTA:         150,
		svcStats:           160,
	}
}

const (
	// actionReload is the reload action string constant
	actionReload = "reload"
	// serviceMTA is the MTA service name constant
	serviceMTA = "mta"
	// serviceStatusEnabled is the LDAP value indicating a service is enabled.
	// This is distinct from boolean "TRUE"/"1" — it's an LDAP service status string.
	serviceStatusEnabled = "enabled"
)

// ControlProcess performs an action on a service by delegating to the
// Registry-backed lifecycle functions in cli.go — the single control path
// also used by the CLI. The MTA restart→reload conversion and the
// systemd/legacy bifurcation both live there (ServiceRestart, IsSystemdMode).
func (sm *ServiceManager) ControlProcess(ctx context.Context, service string, action ServiceAction) error {
	ctx = logger.ContextWithComponentOnce(ctx, "services")
	service = strings.ToLower(service)

	switch action {
	case ActionStart:
		return ServiceStart(ctx, service)
	case ActionStop:
		return ServiceStop(ctx, service)
	case ActionRestart:
		return ServiceRestart(ctx, service)
	case ActionStatus:
		running, err := ServiceStatus(ctx, service)
		if err != nil {
			return err
		}

		if !running {
			return fmt.Errorf("service %s is not running", service)
		}

		return nil
	default:
		return fmt.Errorf("invalid action %d for service %s", action, service)
	}
}

// IsRunning checks if a service is currently running.
func (sm *ServiceManager) IsRunning(ctx context.Context, service string) (bool, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "services")

	return ServiceStatus(ctx, strings.ToLower(service))
}

// AddRestart queues a service for restart.
func (sm *ServiceManager) AddRestart(ctx context.Context, service string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "services")
	service = intern.Service(strings.ToLower(service))
	sm.RestartQueue[service] = true
	logger.DebugContext(ctx, "Queued restart for service",
		"service", service)

	return nil
}

// ProcessRestarts processes all queued service restarts with dependency cascading.
// Handles dependency ordering and retry logic.
// The configLookup function is used to check if dependent services are enabled (SERVICE_* keys).
func (sm *ServiceManager) ProcessRestarts(ctx context.Context, configLookup func(string) string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "services")

	if sm.DisableRestarts {
		sm.logDryRunRestarts(ctx)
		sm.ClearRestarts(ctx)

		return nil
	}

	logger.DebugContext(ctx, "Processing service restarts")

	failedRestarts := make(map[string]int)
	processedThisRound := make(map[string]bool)

	for len(sm.RestartQueue) > 0 {
		madeProgress := sm.processRestartRound(ctx, failedRestarts, processedThisRound, configLookup)

		if madeProgress {
			processedThisRound = make(map[string]bool)
		} else {
			logger.WarnContext(ctx, "No progress made in restart processing, breaking loop")

			break
		}
	}

	logger.DebugContext(ctx, "All service restarts processed")

	return nil
}

// logDryRunRestarts logs what would be restarted in dry-run mode.
func (sm *ServiceManager) logDryRunRestarts(ctx context.Context) {
	logger.DebugContext(ctx, "Restart disabled (dry-run mode)",
		"queued_services", len(sm.RestartQueue))

	for service := range sm.RestartQueue {
		logger.DebugContext(ctx, "[DRY-RUN] Would restart service", "service", service)
	}
}

// processRestartRound iterates over sorted services and attempts one restart per service.
// Returns true when at least one service was handled (success or max retries exhausted).
func (sm *ServiceManager) processRestartRound(
	ctx context.Context,
	failedRestarts map[string]int,
	processedThisRound map[string]bool,
	configLookup func(string) string,
) bool {
	madeProgress := false

	for _, service := range sm.getSortedServices() {
		if !sm.RestartQueue[service] || processedThisRound[service] {
			continue
		}

		if sm.attemptServiceRestart(ctx, service, failedRestarts, configLookup) {
			processedThisRound[service] = true
			madeProgress = true
		}
	}

	return madeProgress
}

// attemptServiceRestart tries to restart a single service. Returns true when the service
// has been removed from the queue (either success or max retries exhausted).
func (sm *ServiceManager) attemptServiceRestart(
	ctx context.Context,
	service string,
	failedRestarts map[string]int,
	configLookup func(string) string,
) bool {
	logger.InfoContext(ctx, "Restarting service", "service", service)

	err := sm.ControlProcess(ctx, service, ActionRestart)
	if err == nil {
		delete(sm.RestartQueue, service)

		logger.InfoContext(ctx, "Successfully restarted service", "service", service)

		if configLookup != nil {
			sm.AddDependencyRestarts(ctx, service, configLookup)
		}

		return true
	}

	failedRestarts[service]++

	logger.WarnContext(ctx, "Failed to restart service", "service", service, "error", err)

	if failedRestarts[service] >= sm.MaxFailedRestarts {
		delete(sm.RestartQueue, service)

		logger.ErrorContext(ctx, "Removing service from restart queue after max failed attempts",
			"service", service,
			"max_attempts", sm.MaxFailedRestarts)

		return true
	}

	return false
}

// getSortedServices returns services from the restart queue sorted by start order.
func (sm *ServiceManager) getSortedServices() []string {
	services := make([]string, 0, len(sm.RestartQueue))
	for service := range sm.RestartQueue {
		services = append(services, service)
	}

	// Sort by start order
	sort.Slice(services, func(i, j int) bool {
		orderI, existsI := sm.StartOrder[services[i]]
		orderJ, existsJ := sm.StartOrder[services[j]]

		if !existsI {
			orderI = 1000 // Default for undefined services — sort after every known service
		}

		if !existsJ {
			orderJ = 1000
		}

		return orderI < orderJ
	})

	return services
}

// ClearRestarts clears all queued restarts.
func (sm *ServiceManager) ClearRestarts(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "services")
	sm.RestartQueue = make(map[string]bool)

	logger.DebugContext(ctx, "Cleared all pending restarts")
}

// GetPendingRestarts returns the list of services pending restart.
func (sm *ServiceManager) GetPendingRestarts() []string {
	services := make([]string, 0, len(sm.RestartQueue))
	for service := range sm.RestartQueue {
		services = append(services, service)
	}

	return services
}

// HasCommand reports whether a service is known to the Registry (i.e. can
// be resolved via LookupService). Retained on the Manager interface for
// configmgr's first-run tracking; "command" no longer means a legacy
// zm*ctl path — there isn't one anymore.
func (sm *ServiceManager) HasCommand(service string) bool {
	return LookupService(strings.ToLower(service)) != nil
}

// SetDependencies sets the dependency map for service restart cascading.
// The map key is a section name, and the value is a slice of service names that depend on it.
func (sm *ServiceManager) SetDependencies(ctx context.Context, deps map[string][]string) {
	ctx = logger.ContextWithComponentOnce(ctx, "services")
	sm.Dependencies = deps
	logger.DebugContext(ctx, "Set dependencies",
		"section_count", len(deps))
}

// AddDependencyRestarts queues dependent services for restart based on a section name.
// It checks if each dependent service is enabled via the configLookup function before queueing.
// Special case: "amavis" is always queued regardless of enabled status.
func (sm *ServiceManager) AddDependencyRestarts(
	ctx context.Context, sectionName string, configLookup func(string) string,
) {
	ctx = logger.ContextWithComponentOnce(ctx, "services")

	deps, exists := sm.Dependencies[sectionName]
	if !exists || len(deps) == 0 {
		return
	}

	logger.DebugContext(ctx, "Checking dependencies for section", "section", sectionName)

	for _, depService := range deps {
		sm.queueDependencyRestart(ctx, strings.ToLower(depService), configLookup)
	}
}

// queueDependencyRestart decides whether to queue a restart for a single dependency.
// "amavis" is always queued; other services are queued only when enabled.
func (sm *ServiceManager) queueDependencyRestart(
	ctx context.Context, depService string, configLookup func(string) string,
) {
	if depService == svcAmavis {
		sm.addRestartLogged(ctx, depService, svcAmavis+" (special case)")
		return
	}

	serviceKey := "SERVICE_" + strings.ToUpper(depService)
	serviceStatus := configLookup(serviceKey)

	if config.IsTruthy(serviceStatus) || serviceStatus == serviceStatusEnabled {
		sm.addRestartLogged(ctx, depService, depService)
	} else {
		logger.DebugContext(ctx, "Skipped dependency restart for disabled service", "service", depService)
	}
}

// addRestartLogged calls AddRestart and logs the outcome.
func (sm *ServiceManager) addRestartLogged(ctx context.Context, service, logLabel string) {
	if err := sm.AddRestart(ctx, service); err != nil {
		logger.WarnContext(ctx, "Failed to add restart for service",
			"service", logLabel,
			"error", err)
	} else {
		logger.DebugContext(ctx, "Added dependency restart for service", "service", logLabel)
	}
}
