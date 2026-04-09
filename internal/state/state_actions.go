// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"context"
	"maps"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// CompileActionsSnapshot captures the slice of State needed to compile the
// next round of rewrite actions. Returned by SnapshotCompileActions so
// callers can operate on a stable copy without holding the State lock.
type CompileActionsSnapshot struct {
	RequestedConfig map[string]string
	MtaSections     map[string]*config.MtaConfigSection
	ForcedConfig    map[string]string
	FirstRun        bool
	ServiceConfig   map[string]string
}

// SnapshotCompileActions returns a point-in-time copy of the compile-relevant
// state and atomically clears RequestedConfig so subsequent requests accumulate
// into a fresh batch. The snapshot is safe to read without additional locking.
func (s *State) SnapshotCompileActions() CompileActionsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	requestedConfig := make(map[string]string, len(s.RequestedConfig))
	maps.Copy(requestedConfig, s.RequestedConfig)
	clear(s.RequestedConfig)

	mtaSections := make(map[string]*config.MtaConfigSection, len(s.MtaConfig.Sections))
	maps.Copy(mtaSections, s.MtaConfig.Sections)

	forcedConfig := make(map[string]string, len(s.ForcedConfig))
	maps.Copy(forcedConfig, s.ForcedConfig)

	serviceConfig := make(map[string]string, len(s.ServerConfig.ServiceConfig))
	maps.Copy(serviceConfig, s.ServerConfig.ServiceConfig)

	return CompileActionsSnapshot{
		RequestedConfig: requestedConfig,
		MtaSections:     mtaSections,
		ForcedConfig:    forcedConfig,
		FirstRun:        s.FirstRun,
		ServiceConfig:   serviceConfig,
	}
}

// --- Methods for managing current actions/states ---

// CurRewrites adds or retrieves a rewrite entry for the given service.
// If entry is non-nil, it is added to the current rewrites map.
// Returns the current rewrite entry for the service.
func (s *State) CurRewrites(ctx context.Context, service string, entry *config.RewriteEntry) config.RewriteEntry {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	if entry != nil {
		logger.DebugContext(ctx, "Adding rewrite",
			"service", service)
		s.CurrentActions.Rewrites[service] = *entry
	}

	return s.CurrentActions.Rewrites[service]
}

// DelRewrite removes a rewrite entry for the given service.
func (s *State) DelRewrite(service string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.CurrentActions.Rewrites, service)
}

// CurRestarts sets the restart action value for a service.
// The actionValue typically represents restart count or action type (-1, 0, 1, 2).
func (s *State) CurRestarts(service string, actionValue int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CurrentActions.Restarts[service] = actionValue
}

// DelRestart removes a restart entry for the given service.
func (s *State) DelRestart(service string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.CurrentActions.Restarts, service)
}

// CurLdap adds or retrieves an LDAP configuration change.
// If val is non-empty, it is added to the current LDAP changes map.
// Returns the current value for the given key.
func (s *State) CurLdap(ctx context.Context, key string, val string) string {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	if val != "" { // Assuming empty string means no change
		logger.DebugContext(ctx, "Adding ldap",
			"key", key,
			"value", val)
		s.CurrentActions.Ldap[key] = val
	}

	return s.CurrentActions.Ldap[key]
}

// DelLdap removes an LDAP configuration change for the given key.
func (s *State) DelLdap(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.CurrentActions.Ldap, key)
}

// addOrRetrieveConfig is a generic helper for adding or retrieving configuration changes.
// If val is non-empty, it is added to the config map with newlines converted to spaces.
// Returns the current value for the given key.
func (s *State) addOrRetrieveConfig(
	ctx context.Context,
	configMap map[string]string,
	key string,
	val string,
	configType string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if val != "" {
		logger.DebugContext(ctx, "Adding "+configType,
			"key", key,
			"value", val)
		configMap[key] = strings.ReplaceAll(val, "\n", " ")
	}

	return configMap[key]
}

// CurPostconf adds or retrieves a postconf configuration change.
// If val is non-empty, it is added to the current postconf map with newlines converted to spaces.
// Returns the current value for the given key.
func (s *State) CurPostconf(ctx context.Context, key string, val string) string {
	ctx = logger.ContextWithComponentOnce(ctx, "state")
	return s.addOrRetrieveConfig(ctx, s.CurrentActions.Postconf, key, val, "postconf")
}

// ClearPostconf clears all pending postconf changes.
func (s *State) ClearPostconf() {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.CurrentActions.Postconf)
}

// CurPostconfd adds or retrieves a postconfd configuration change.
// If val is non-empty, it is added to the current postconfd map with newlines converted to spaces.
// Returns the current value for the given key.
func (s *State) CurPostconfd(ctx context.Context, key string, val string) string {
	ctx = logger.ContextWithComponentOnce(ctx, "state")
	return s.addOrRetrieveConfig(ctx, s.CurrentActions.Postconfd, key, val, "postconfd")
}

// ClearPostconfd clears all pending postconfd changes.
func (s *State) ClearPostconfd() {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.CurrentActions.Postconfd)
}

// CurServices sets or retrieves the current status for a service.
// If status is non-empty, it is set for the given service.
// Returns the current status for the service.
func (s *State) CurServices(service string, status string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status != "" {
		s.CurrentActions.Services[service] = status
	}

	return s.CurrentActions.Services[service]
}

// PrevServices sets or retrieves the previous status for a service.
// If status is non-empty, it is set for the given service.
// Returns the previous status for the service.
func (s *State) PrevServices(service string, status string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status != "" {
		s.PreviousActions.Services[service] = status
	}

	return s.PreviousActions.Services[service]
}

// Proxygen sets and returns the current proxy generation flag.
// Used to trigger nginx proxy configuration regeneration.
func (s *State) Proxygen(val bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CurrentActions.Proxygen = val

	return s.CurrentActions.Proxygen
}

// GetWatchdog returns the watchdog status for a service.
func (s *State) GetWatchdog(service string) *bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if val, ok := s.WatchdogProcess[service]; ok {
		return &val
	}

	return nil
}

// SetWatchdog sets the watchdog status for a service.
func (s *State) SetWatchdog(service string, status bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.WatchdogProcess[service] = status
}

// DelWatchdog removes a service from watchdog tracking.
func (s *State) DelWatchdog(service string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.WatchdogProcess, service)
}

// CompareActions compares current and previous actions to determine what changed.
// Returns true if any actions differ between current and previous state.
func (s *State) CompareActions() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return !mapsEqual(s.CurrentActions.Rewrites, s.PreviousActions.Rewrites) ||
		!mapsEqual(s.CurrentActions.Postconf, s.PreviousActions.Postconf) ||
		!mapsEqual(s.CurrentActions.Postconfd, s.PreviousActions.Postconfd) ||
		!mapsEqual(s.CurrentActions.Services, s.PreviousActions.Services) ||
		!mapsEqual(s.CurrentActions.Ldap, s.PreviousActions.Ldap) ||
		s.CurrentActions.Proxygen != s.PreviousActions.Proxygen
}

// SaveCurrentToPrevious copies current actions to previous actions.
// This is called after successfully executing all actions in the main loop.
func (s *State) SaveCurrentToPrevious(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	logger.DebugContext(ctx, "Saved current actions to previous")

	// Copy rewrites
	clear(s.PreviousActions.Rewrites)
	maps.Copy(s.PreviousActions.Rewrites, s.CurrentActions.Rewrites)

	// Copy postconf
	clear(s.PreviousActions.Postconf)
	maps.Copy(s.PreviousActions.Postconf, s.CurrentActions.Postconf)

	// Copy postconfd
	clear(s.PreviousActions.Postconfd)
	maps.Copy(s.PreviousActions.Postconfd, s.CurrentActions.Postconfd)

	// Copy services
	clear(s.PreviousActions.Services)
	maps.Copy(s.PreviousActions.Services, s.CurrentActions.Services)

	// Copy LDAP
	clear(s.PreviousActions.Ldap)
	maps.Copy(s.PreviousActions.Ldap, s.CurrentActions.Ldap)

	// Copy proxygen
	s.PreviousActions.Proxygen = s.CurrentActions.Proxygen
}

// CompileDependencyRestarts compiles restarts for dependent services.
// This function will need to interact with the service manager and potentially the main loop.
func (s *State) CompileDependencyRestarts(ctx context.Context, serviceName string,
	lookupConfig func(cfgType, key string) (string, error),
	curRestarts func(service string, actionValue int)) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	section := s.MtaConfig.Sections[serviceName]
	if section != nil {
		for depend := range section.Depends {
			// Check if the dependent service is enabled
			// This requires a way to look up service status, which will come from ConfigManager/ServiceManager
			// For now, simulate with lookupConfig
			isServiceEnabled, err := lookupConfig("SERVICE", depend)
			if err != nil {
				logger.ErrorContext(ctx, "Error checking service for dependency",
					"service", depend,
					"error", err)

				continue
			}

			if IsTrueValue(isServiceEnabled) || depend == "amavis" { // Special case for amavis from Jython
				logger.DebugContext(ctx, "Adding restart for dependency",
					"dependency", depend)
				curRestarts(depend, -1) // -1 typically means restart
			}
		}
	}
}
