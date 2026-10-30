// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package state manages the runtime state of the configd daemon: the
// immutable configuration snapshot of the current cycle (published through an
// atomic pointer), the forced/requested section sets, the FILE lookup cache and
// the watchdog flags. Mutable accessors are thread-safe; the snapshot is read
// lock-free.
package state

import (
	"context"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

// State holds the runtime state shared by the main loop, the listener and the
// watchdog.
type State struct {
	mu sync.Mutex // Protects the mutable fields below

	// snap is the immutable per-cycle configuration view; readers use Snapshot().
	snap atomic.Pointer[snapshot.Snapshot]

	MtaConfig *config.MtaConfig

	ForcedConfig    map[string]string
	RequestedConfig map[string]string
	FileCache       map[string]string // For FILE type lookups: key -> content
	FileMD5Cache    map[string]string // For generated config files: filepath -> MD5 hash

	WatchdogProcess map[string]bool // service -> bool (true if available for watchdog)

	FirstRun   bool
	Forced     int
	SleepTimer float64 // For signal handling workaround
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

// NewState initializes a new State object holding an empty snapshot.
func NewState() *State {
	s := &State{
		ForcedConfig:    make(map[string]string),
		RequestedConfig: make(map[string]string),
		FileCache:       make(map[string]string),
		FileMD5Cache:    make(map[string]string),
		WatchdogProcess: make(map[string]bool),
		FirstRun:        true,
		MtaConfig:       &config.MtaConfig{Sections: make(map[string]*config.MtaConfigSection)},
	}

	s.snap.Store(snapshot.New(snapshot.Parts{}))

	return s
}

// Snapshot returns the configuration snapshot of the latest completed load.
// It is never nil, immutable, and safe to read from any goroutine without
// locks.
func (s *State) Snapshot() *snapshot.Snapshot {
	if snap := s.snap.Load(); snap != nil {
		return snap
	}

	return snapshot.New(snapshot.Parts{})
}

// Publish makes snap the current snapshot.
func (s *State) Publish(snap *snapshot.Snapshot) {
	s.snap.Store(snap)
}

// UpdateSnapshot atomically replaces the snapshot with fn(current). fn may run
// more than once under contention and must be free of side effects.
func (s *State) UpdateSnapshot(fn func(*snapshot.Snapshot) *snapshot.Snapshot) {
	for {
		old := s.snap.Load()

		var next *snapshot.Snapshot
		if old == nil {
			next = fn(snapshot.New(snapshot.Parts{}))
		} else {
			next = fn(old)
		}

		if s.snap.CompareAndSwap(old, next) {
			return
		}
	}
}

// AddRequestedConfigs marks the given sections as needing a rewrite on the
// next cycle. Safe to call concurrently.
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

// TakeRequestedConfigs returns the sorted requested sections and clears the
// set, so new requests accumulate for the next cycle.
func (s *State) TakeRequestedConfigs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := slices.Sorted(maps.Keys(s.RequestedConfig))
	clear(s.RequestedConfig)

	return out
}

// ForcedSections returns the sorted forced sections.
func (s *State) ForcedSections() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Sorted(maps.Keys(s.ForcedConfig))
}

// SetFirstRun toggles the first-run flag under the State lock.
func (s *State) SetFirstRun(val bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.FirstRun = val
}

// SetMtaConfig publishes a freshly parsed MTA configuration under the State lock.
func (s *State) SetMtaConfig(mtac *config.MtaConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.MtaConfig = mtac
}

// GetMtaConfig returns the current MTA configuration reference under the State lock.
func (s *State) GetMtaConfig() *config.MtaConfig {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.MtaConfig
}

// IsFirstRun reports the first-run flag under the State lock.
func (s *State) IsFirstRun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.FirstRun
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
