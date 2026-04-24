// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/zextras/carbonio-configd/internal/intern"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// ServiceManager implements the Manager interface for service control operations.
type ServiceManager struct {
	// CommandMap maps service names to their control commands (zm*ctl scripts)
	CommandMap map[string]string
	// UseSystemd indicates whether to use systemd for service control
	// When true: Priority 1: systemctl, Priority 2: zm*ctl fallback
	// When false: Only zm*ctl scripts are used
	UseSystemd bool
	// SystemdMap maps service names to systemd unit names
	SystemdMap map[string]string
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
		CommandMap:        getDefaultCommandMap(),
		UseSystemd:        false, // Default to traditional commands (ctls)
		SystemdMap:        getDefaultSystemdMap(),
		RestartQueue:      make(map[string]bool),
		MaxFailedRestarts: 3,
		StartOrder:        getDefaultStartOrder(),
		Dependencies:      make(map[string][]string),
		DisableRestarts:   false, // Default: restarts enabled
	}
}

// getDefaultCommandMap returns the default service command mappings.
// All paths use basePath as prefix so they can be overridden in tests.
func getDefaultCommandMap() map[string]string {
	return map[string]string{
		"amavis":    binPath + "/zmamavisdctl",
		"antispam":  binPath + "/zmantispamctl",
		"antivirus": binPath + "/zmclamdctl",
		"cbpolicyd": binPath + "/zmcbpolicydctl",
		"clamd":     binPath + "/zmclamdctl",
		"ldap":      binPath + "/ldap",
		"mailbox":   binPath + "/zmstorectl",
		"mailboxd":  binPath + "/zmmailboxdctl",
		"memcached": binPath + "/zmmemcachedctl",
		"mta":       binPath + "/zmmtactl",
		"opendkim":  binPath + "/zmopendkimctl",
		"proxy":     binPath + "/zmproxyctl",
		"sasl":      binPath + "/zmsaslauthdctl",
		"service":   binPath + "/zmmailboxdctl",
		"stats":     binPath + "/zmstatctl",
	}
}

// getDefaultSystemdMap returns the default systemd unit name mappings.
func getDefaultSystemdMap() map[string]string {
	return map[string]string{
		"amavis":    "carbonio-mailthreat.service", // Mail Threat = amavis equivalent
		"antispam":  "carbonio-antispam.service",
		"antivirus": "carbonio-antivirus.service",
		"cbpolicyd": "carbonio-policyd.service",
		"clamd":     "carbonio-antivirus.service",
		"ldap":      "carbonio-openldap.service",
		"mailbox":   "carbonio-appserver.service", // Carbonio Appserver = mailbox service
		"memcached": "carbonio-memcached.service",
		"milter":    "carbonio-milter.service",
		"mta":       "carbonio-postfix.service",
		"opendkim":  "carbonio-opendkim.service",
		"proxy":     "carbonio-nginx.service",
		"sasl":      "carbonio-saslauthd.service",
		"stats":     "carbonio-stats.service",
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
		"ldap":             0,
		"configd":          10,
		"service-discover": 20,
		"mailbox":          50,
		"memcached":        60,
		"proxy":            70,
		"amavis":           75,
		"antispam":         80,
		"antivirus":        90,
		"freshclam":        92,
		"opendkim":         100,
		"cbpolicyd":        120,
		"saslauthd":        130,
		"milter":           140,
		"mta":              150,
		"stats":            160,
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

// ControlProcess performs an action on a service.
func (sm *ServiceManager) ControlProcess(ctx context.Context, service string, action ServiceAction) error {
	ctx = logger.ContextWithComponentOnce(ctx, "services")
	service = strings.ToLower(service)

	// Validate action
	if action < ActionRestart || action > ActionStatus {
		return fmt.Errorf("invalid action %d for service %s", action, service)
	}

	// Check if service command is defined
	cmd, exists := sm.CommandMap[service]
	if !exists {
		return fmt.Errorf("command not defined for service %s", service)
	}

	actionStr := action.String()

	// Special case: MTA restart should be converted to reload for graceful handling
	if service == serviceMTA && action == ActionRestart {
		actionStr = actionReload
	}

	logger.DebugContext(ctx, "CONTROL service",
		"service", service,
		"command", cmd,
		"action", actionStr)

	// Execute the command
	if sm.UseSystemd {
		return sm.executeSystemdCommand(ctx, service, actionStr)
	}

	return sm.executeCommand(ctx, cmd, actionStr)
}

// executeSystemdCommand executes service control using the following priority:
// 1. systemctl (preferred - uses polkit permissions for zextras user)
// 2. zm*ctl scripts (traditional Carbonio fallback)
//
// With polkit policy in place, zextras user can execute systemctl commands directly,
// making this the preferred method in systemd-enabled environments.
func (sm *ServiceManager) executeSystemdCommand(ctx context.Context, service, action string) error {
	unitName, exists := sm.SystemdMap[service]
	if !exists {
		return fmt.Errorf("systemd unit not defined for service %s", service)
	}

	// Priority 1: Try systemctl first (works with polkit policy for zextras user)
	logger.DebugContext(ctx, "Attempting systemctl",
		"action", action,
		"service", service)

	args := []string{action, unitName}
	cmd := exec.CommandContext(ctx, "systemctl", args...)

	output, err := cmd.CombinedOutput()
	if err == nil {
		logger.DebugContext(ctx, "Systemctl succeeded",
			"action", action,
			"unit", unitName)

		return nil
	}

	// systemctl failed, log and try fallback
	logger.WarnContext(ctx, "Systemctl failed, trying zm*ctl fallback",
		"action", action,
		"unit", unitName,
		"error", err,
		"output", string(output))

	// Priority 2: Try zm*ctl scripts as fallback
	zmCmd, exists := sm.CommandMap[service]
	if exists {
		logger.DebugContext(ctx, "Falling back to zm*ctl script",
			"service", service,
			"command", zmCmd,
			"action", action)

		return sm.executeCommand(ctx, zmCmd, action)
	}

	// No fallback available
	return fmt.Errorf("systemctl failed and no zm*ctl fallback available for service %s: %w", service, err)
}

// executeCommand executes a traditional service control command.
func (sm *ServiceManager) executeCommand(ctx context.Context, cmdPath, action string) error {
	args := []string{action}
	cmd := exec.CommandContext(ctx, cmdPath, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.ErrorContext(ctx, "command failed",
			"command", cmdPath,
			"action", action,
			"error", err,
			"output", string(output))

		return fmt.Errorf("zimbra command %s %s failed: %w", cmdPath, action, err)
	}

	logger.DebugContext(ctx, "command succeeded",
		"command", cmdPath,
		"action", action)

	return nil
}

// IsRunning checks if a service is currently running.
func (sm *ServiceManager) IsRunning(ctx context.Context, service string) (bool, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "services")
	err := sm.ControlProcess(ctx, service, ActionStatus)

	return err == nil, nil
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

// HasCommand checks if a service has a control command defined.
func (sm *ServiceManager) HasCommand(service string) bool {
	service = strings.ToLower(service)
	_, exists := sm.CommandMap[service]

	return exists
}

// SetUseSystemd enables or disables systemctl for service control.
func (sm *ServiceManager) SetUseSystemd(enabled bool) {
	sm.UseSystemd = enabled
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
	if depService == "amavis" {
		sm.addRestartLogged(ctx, depService, "amavis (special case)")
		return
	}

	serviceKey := "SERVICE_" + strings.ToUpper(depService)
	serviceStatus := configLookup(serviceKey)

	if isTruthy(serviceStatus) || serviceStatus == serviceStatusEnabled {
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
