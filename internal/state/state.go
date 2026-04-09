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
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
)

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

// ResetForcedConfig clears the forced configuration map after processing.
func (s *State) ResetForcedConfig(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.ForcedConfig)

	logger.DebugContext(ctx, "Reset forced config")
}

// ResetRequestedConfig clears the requested configuration map after processing.
func (s *State) ResetRequestedConfig(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.RequestedConfig)

	logger.DebugContext(ctx, "Reset requested config")
}

// SetForcedConfig adds a section to the forced configuration map.
// Used for command-line --force-rewrite or similar operations.
func (s *State) SetForcedConfig(ctx context.Context, section string) {
	ctx = logger.ContextWithComponentOnce(ctx, "state")

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

// mapsEqual reports whether two maps have identical keys and values.
func mapsEqual[V comparable](a, b map[string]V) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}

	return true
}
