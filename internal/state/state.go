// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package state manages the runtime state of configd daemon.
// It tracks configuration changes, service restart queues, watchdog status,
// and MD5 fingerprints for change detection. All operations are thread-safe
// using mutex-based synchronization.
package state

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 used for non-cryptographic checksumming only
	"encoding/hex"
	"io"
	"maps"
	"os"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// MAPPEDFILES mirrors the MAPPEDFILES in jylibs/state.py
var MAPPEDFILES = map[string]string{
	"zimbraSSLDHParam": "conf/dhparam.pem",
}

// State manages the current and previous configurations and actions.
type State struct {
	mu sync.Mutex // Protects access to state variables

	// Configuration objects (references to those in ConfigManager)
	LocalConfig  *config.LocalConfig
	GlobalConfig *config.GlobalConfig
	MiscConfig   *config.MiscConfig
	ServerConfig *config.ServerConfig
	MtaConfig    *config.MtaConfig

	// Tracking changes
	ChangedKeys map[string][]string                     // sectionName -> list of changed keys
	LastVals    map[string]map[string]map[string]string // sectionName -> type -> key -> value

	// Current and previous actions/states
	ForcedConfig    map[string]string
	RequestedConfig map[string]string
	FileCache       map[string]string // For FILE type lookups: key -> content
	FileMD5Cache    map[string]string // For generated config files: filepath -> MD5 hash

	WatchdogProcess map[string]bool // service -> bool (true if available for watchdog)

	CurrentActions struct {
		Rewrites  map[string]config.RewriteEntry
		Restarts  map[string]int // service -> num_failed_restarts or action_value (-1, 0, 1, 2)
		Postconf  map[string]string
		Postconfd map[string]string
		Services  map[string]string // service -> status (running, stopped, started)
		Ldap      map[string]string
		Proxygen  bool
	}

	PreviousActions struct {
		Rewrites  map[string]config.RewriteEntry
		Config    map[string]string // This seems to be a generic config map in Jython, need to clarify
		Restarts  map[string]int    // Not used in Jython's previous, but good to have for consistency
		Postconf  map[string]string
		Postconfd map[string]string
		Services  map[string]string
		Ldap      map[string]string
		Proxygen  bool
	}

	FirstRun          bool
	Forced            int
	MaxFailedRestarts int
	SleepTimer        float64 // For signal handling workaround
}

// SetSleepTimer atomically sets the sleep timer value.
func (s *State) SetSleepTimer(val float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.SleepTimer = val
}

// GetSleepTimer atomically gets the sleep timer value.
func (s *State) GetSleepTimer() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.SleepTimer
}

// NewState initializes a new State object.
func NewState() *State {
	s := &State{
		ChangedKeys:       make(map[string][]string),
		LastVals:          make(map[string]map[string]map[string]string),
		ForcedConfig:      make(map[string]string),
		RequestedConfig:   make(map[string]string),
		FileCache:         make(map[string]string),
		FileMD5Cache:      make(map[string]string),
		WatchdogProcess:   make(map[string]bool),
		FirstRun:          true,
		MaxFailedRestarts: 3, // Default value from Jython
		LocalConfig:       &config.LocalConfig{Data: make(map[string]string)},
		GlobalConfig:      &config.GlobalConfig{Data: make(map[string]string)},
		MiscConfig:        &config.MiscConfig{Data: make(map[string]string)},
		ServerConfig:      &config.ServerConfig{Data: make(map[string]string), ServiceConfig: make(map[string]string)},
		MtaConfig:         &config.MtaConfig{Sections: make(map[string]*config.MtaConfigSection)},
	}

	s.CurrentActions.Rewrites = make(map[string]config.RewriteEntry)
	s.CurrentActions.Restarts = make(map[string]int)
	s.CurrentActions.Postconf = make(map[string]string)
	s.CurrentActions.Postconfd = make(map[string]string)
	s.CurrentActions.Services = make(map[string]string)
	s.CurrentActions.Ldap = make(map[string]string)

	s.PreviousActions.Rewrites = make(map[string]config.RewriteEntry)
	s.PreviousActions.Config = make(map[string]string)
	s.PreviousActions.Restarts = make(map[string]int)
	s.PreviousActions.Postconf = make(map[string]string)
	s.PreviousActions.Postconfd = make(map[string]string)
	s.PreviousActions.Services = make(map[string]string)
	s.PreviousActions.Ldap = make(map[string]string)

	return s
}

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

// AddRequestedConfigs marks the given sections as needing a rewrite on the
// next compile cycle. Safe to call concurrently.
func (s *State) AddRequestedConfigs(ctx context.Context, sections []string) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, section := range sections {
		s.RequestedConfig[section] = section
		logger.DebugContext(ctx, "Requested rewrite for section",
			"section", section)
	}
}

// SetFirstRun toggles the first-run flag under the State lock.
func (s *State) SetFirstRun(val bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.FirstRun = val
}

// GetCurrentRewriteKeys returns a copy of the keys for files queued for
// rewrite by the last compile cycle. Safe to call concurrently.
func (s *State) GetCurrentRewriteKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]string, 0, len(s.CurrentActions.Rewrites))
	for key := range s.CurrentActions.Rewrites {
		keys = append(keys, key)
	}

	return keys
}

// SetConfigs sets the references to the configuration objects.
func (s *State) SetConfigs(lc *config.LocalConfig,
	gc *config.GlobalConfig,
	mc *config.MiscConfig,
	sc *config.ServerConfig,
	mtac *config.MtaConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.LocalConfig = lc
	s.GlobalConfig = gc
	s.MiscConfig = mc
	s.ServerConfig = sc
	s.MtaConfig = mtac
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

// --- Methods for tracking changed keys and last values ---

// ResetChangedKeys clears the list of changed keys for a given section.
func (s *State) ResetChangedKeys(section string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ChangedKeys[section] = []string{}
}

// ChangedKeysForSection adds or retrieves changed keys for a section.
// If key is non-empty, it is added to the changed keys list for the section.
// Returns the current list of changed keys for the section.
func (s *State) ChangedKeysForSection(section string, key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.ChangedKeys[section]; !ok {
		s.ChangedKeys[section] = []string{}
	}

	if key != "" {
		s.ChangedKeys[section] = append(s.ChangedKeys[section], key)
	}

	return s.ChangedKeys[section]
}

// LastVal stores or retrieves the last value of a configuration key.
// If val is non-empty, it is stored as the last value for the given section/type/key.
// Returns the last stored value or empty string if not found.
func (s *State) LastVal(ctx context.Context, section, cfgType, key string, val string) string {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	if logger.IsDebug(ctx) {
		logger.DebugContext(ctx, "Entering lastVal",
			"section", section,
			"config_type", cfgType,
			"key", key,
			"value", val)
	}

	if _, ok := s.LastVals[section]; !ok {
		s.LastVals[section] = make(map[string]map[string]string)
	}

	if _, ok := s.LastVals[section][cfgType]; !ok {
		s.LastVals[section][cfgType] = make(map[string]string)
	}

	if val != "" { // Assuming empty string means no update
		s.LastVals[section][cfgType][key] = val
	}

	if v, ok := s.LastVals[section][cfgType][key]; ok {
		if logger.IsDebug(ctx) {
			logger.DebugContext(ctx, "Returning lastVal",
				"section", section,
				"config_type", cfgType,
				"key", key,
				"value", v)
		}

		return v
	}

	return "" // Return empty string if not found
}

// DelVal removes the last value entry for a given section/type/key.
func (s *State) DelVal(section, cfgType, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.LastVals[section]; ok {
		if _, ok := s.LastVals[section][cfgType]; ok {
			delete(s.LastVals[section][cfgType], key)
		}
	}
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

// IsFalseValue checks if a string represents a false value.
// Matches Python: re.match(r"no|false|0+",str(val),re.I)
// This means: empty string, "no" (case-insensitive), "false" (case-insensitive), or one or more zeros
func IsFalseValue(val string) bool {
	if val == "" {
		return true
	}

	lowerVal := strings.ToLower(val)
	if lowerVal == "no" || lowerVal == "false" {
		return true
	}
	// Check if the value consists only of zeros (matching 0+ regex)
	for _, c := range val {
		if c != '0' {
			return false
		}
	}

	return val != "" // If we got here and not empty, it's all zeros
}

// IsTrueValue checks if a string represents a true value.
func IsTrueValue(val string) bool {
	return !IsFalseValue(val)
}

// CompileDependencyRestarts compiles restarts for dependent services.
// This function will need to interact with the service manager and potentially the main loop.
func (s *State) CompileDependencyRestarts(ctx context.Context, serviceName string,
	lookupConfig func(cfgType, key string) (string, error),
	curRestarts func(service string, actionValue int)) {
	ctx = logger.ContextWithComponent(ctx, "state")

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

// --- MD5 Hashing and Change Detection Methods ---

// ComputeFileMD5 computes the MD5 hash of a file's contents.
// Returns the hex-encoded MD5 hash string or an error if file cannot be read.
func ComputeFileMD5(ctx context.Context, filepath string) (string, error) {
	ctx = logger.ContextWithComponent(ctx, "state")
	//nolint:gosec // G304: File path comes from trusted configuration
	file, err := os.Open(filepath)
	if err != nil {
		return "", err
	}

	defer func() {
		if cerr := file.Close(); cerr != nil {
			logger.WarnContext(ctx, "Failed to close file",
				"filepath", filepath,
				"error", cerr)
		}
	}()

	hash := md5.New() //nolint:gosec // MD5 used for non-cryptographic checksumming only
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ComputeStringMD5 computes the MD5 hash of a string.
// Returns the hex-encoded MD5 hash string.
func ComputeStringMD5(content string) string {
	hash := md5.New() //nolint:gosec // MD5 used for non-cryptographic checksumming only
	hash.Write([]byte(content))

	return hex.EncodeToString(hash.Sum(nil))
}

// GetFileMD5 retrieves the cached MD5 hash for a file path.
// Returns empty string if no cached hash exists.
func (s *State) GetFileMD5(filepath string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.FileMD5Cache[filepath]
}

// SetFileMD5 stores the MD5 hash for a file path in the cache.
func (s *State) SetFileMD5(filepath, md5hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.FileMD5Cache[filepath] = md5hash
}

// FileHasChanged checks if a file's current MD5 differs from cached MD5.
// If no cached MD5 exists, returns true (file is considered new/changed).
// If file doesn't exist or can't be read, returns true (triggering rewrite).
func (s *State) FileHasChanged(ctx context.Context, filepath string) bool {
	ctx = logger.ContextWithComponent(ctx, "state")
	cachedMD5 := s.GetFileMD5(filepath)

	currentMD5, err := ComputeFileMD5(ctx, filepath)
	if err != nil {
		// File doesn't exist or can't be read - consider it changed
		logger.DebugContext(ctx, "File cannot be read, treating as changed",
			"filepath", filepath,
			"error", err)

		return true
	}

	if cachedMD5 == "" {
		// No cached hash - file is new
		logger.DebugContext(ctx, "File has no cached MD5, treating as changed",
			"filepath", filepath)

		return true
	}

	changed := cachedMD5 != currentMD5
	if changed {
		logger.InfoContext(ctx, "File MD5 changed",
			"filepath", filepath,
			"old_md5", cachedMD5,
			"new_md5", currentMD5)
	}

	return changed
}

// UpdateFileMD5 recomputes and updates the cached MD5 for a file.
// Returns error if file cannot be read.
func (s *State) UpdateFileMD5(ctx context.Context, filepath string) error {
	ctx = logger.ContextWithComponent(ctx, "state")

	md5hash, err := ComputeFileMD5(ctx, filepath)
	if err != nil {
		return err
	}

	s.SetFileMD5(filepath, md5hash)

	logger.DebugContext(ctx, "Updated MD5 cache",
		"filepath", filepath,
		"md5", md5hash)

	return nil
}

// ShouldRewriteSection determines if a section should be rewritten based on:
// 1. FirstRun flag (always rewrite on first run)
// 2. Section.changed flag (configuration variables changed)
// 3. ForcedConfig map (section explicitly forced via command-line or network)
// 4. RequestedConfig map (section explicitly requested via network command)
func (s *State) ShouldRewriteSection(ctx context.Context, sectionName string, section *config.MtaConfigSection) bool {
	ctx = logger.ContextWithComponent(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.FirstRun {
		logger.DebugContext(ctx, "Section rewrite required (first run)",
			"section", sectionName)

		return true
	}

	if section != nil && section.Changed {
		logger.DebugContext(ctx, "Section rewrite required (configuration changed)",
			"section", sectionName)

		return true
	}

	if _, forced := s.ForcedConfig[sectionName]; forced {
		logger.DebugContext(ctx, "Section rewrite required (forced)",
			"section", sectionName)

		return true
	}

	if _, requested := s.RequestedConfig[sectionName]; requested {
		logger.DebugContext(ctx, "Section rewrite required (requested)",
			"section", sectionName)

		return true
	}

	logger.DebugContext(ctx, "Section no rewrite needed",
		"section", sectionName)

	return false
}

// ClearFileCache clears the FILE type lookup cache.
// This is called at the start of each configuration fetch cycle.
func (s *State) ClearFileCache(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.FileCache)

	logger.DebugContext(ctx, "Cleared FILE lookup cache")
}

// ResetForcedConfig clears the forced configuration map after processing.
func (s *State) ResetForcedConfig(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.ForcedConfig)

	logger.DebugContext(ctx, "Reset forced config")
}

// ResetRequestedConfig clears the requested configuration map after processing.
func (s *State) ResetRequestedConfig(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.RequestedConfig)

	logger.DebugContext(ctx, "Reset requested config")
}

// SetForcedConfig adds a section to the forced configuration map.
// Used for command-line --force-rewrite or similar operations.
func (s *State) SetForcedConfig(ctx context.Context, section string) {
	ctx = logger.ContextWithComponent(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	s.ForcedConfig[section] = "1"

	logger.DebugContext(ctx, "Forced rewrite for section",
		"section", section)
}

// SetRequestedConfig adds a section to the requested configuration map.
// Used for network REWRITE commands specifying specific services.
func (s *State) SetRequestedConfig(ctx context.Context, section string) {
	s.AddRequestedConfigs(ctx, []string{section})
}

// CompareActions compares current and previous actions to determine what changed.
// Returns true if any actions differ between current and previous state.
//
//nolint:gocyclo,cyclop // requires comparing multiple action types and states
func (s *State) CompareActions() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Compare rewrites
	if len(s.CurrentActions.Rewrites) != len(s.PreviousActions.Rewrites) {
		return true
	}

	for key, val := range s.CurrentActions.Rewrites {
		if prevVal, ok := s.PreviousActions.Rewrites[key]; !ok || prevVal != val {
			return true
		}
	}

	// Compare postconf
	if len(s.CurrentActions.Postconf) != len(s.PreviousActions.Postconf) {
		return true
	}

	for key, val := range s.CurrentActions.Postconf {
		if prevVal, ok := s.PreviousActions.Postconf[key]; !ok || prevVal != val {
			return true
		}
	}

	// Compare postconfd
	if len(s.CurrentActions.Postconfd) != len(s.PreviousActions.Postconfd) {
		return true
	}

	for key, val := range s.CurrentActions.Postconfd {
		if prevVal, ok := s.PreviousActions.Postconfd[key]; !ok || prevVal != val {
			return true
		}
	}

	// Compare services
	if len(s.CurrentActions.Services) != len(s.PreviousActions.Services) {
		return true
	}

	for key, val := range s.CurrentActions.Services {
		if prevVal, ok := s.PreviousActions.Services[key]; !ok || prevVal != val {
			return true
		}
	}

	// Compare LDAP
	if len(s.CurrentActions.Ldap) != len(s.PreviousActions.Ldap) {
		return true
	}

	for key, val := range s.CurrentActions.Ldap {
		if prevVal, ok := s.PreviousActions.Ldap[key]; !ok || prevVal != val {
			return true
		}
	}

	// Compare proxygen
	if s.CurrentActions.Proxygen != s.PreviousActions.Proxygen {
		return true
	}

	return false
}

// SaveCurrentToPrevious copies current actions to previous actions.
// This is called after successfully executing all actions in the main loop.
func (s *State) SaveCurrentToPrevious(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "state")

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
