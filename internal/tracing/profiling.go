// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling

package tracing

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sync"
	"time"
)

// ProfilingConfig holds profiling configuration.
type ProfilingConfig struct {
	CPUProfilePath  string
	MemProfilePath  string
	TracePath       string
	ProfileDuration time.Duration
}

// profilingState holds active profiling state.
type profilingState struct {
	cpuFile   *os.File
	traceFile *os.File
	startTime time.Time
	// cancel is closed when the session stops so the auto-stop timer
	// goroutine exits instead of lingering for ProfileDuration.
	cancel chan struct{}
}

// profilingMu guards activeProfilingState and serializes Start/Stop so the
// auto-stop timer cannot race an explicit StopProfiling at shutdown.
var (
	profilingMu          sync.Mutex
	activeProfilingState *profilingState
)

// StartProfiling initializes profiling based on configuration.
// Returns an error if profiling cannot be started.
func StartProfiling(config *ProfilingConfig) error {
	if config == nil {
		return nil
	}

	profilingMu.Lock()
	defer profilingMu.Unlock()

	activeProfilingState = &profilingState{
		startTime: time.Now(),
		cancel:    make(chan struct{}),
	}

	// Start CPU profiling if requested
	if config.CPUProfilePath != "" {
		if err := startCPUProfile(config.CPUProfilePath); err != nil {
			return fmt.Errorf("failed to start CPU profiling: %w", err)
		}
	}

	// Start trace profiling if requested
	if config.TracePath != "" {
		if err := startTrace(config.TracePath); err != nil {
			// Stop CPU profiling if it was started
			if activeProfilingState.cpuFile != nil {
				pprof.StopCPUProfile()
				activeProfilingState.cpuFile.Close()
			}
			return fmt.Errorf("failed to start trace profiling: %w", err)
		}
	}

	// Schedule automatic stop if ProfileDuration is set
	if config.ProfileDuration > 0 {
		go autoStop(activeProfilingState, config)
	}

	return nil
}

// autoStop stops the given profiling session after config.ProfileDuration
// unless the session is stopped earlier. It never touches a newer session.
func autoStop(state *profilingState, config *ProfilingConfig) {
	timer := time.NewTimer(config.ProfileDuration)
	defer timer.Stop()

	select {
	case <-state.cancel:
	case <-timer.C:
		profilingMu.Lock()
		defer profilingMu.Unlock()

		if activeProfilingState == state {
			stopLocked(config)
		}
	}
}

// StopProfiling stops all active profiling and writes profiles to disk.
// It is idempotent and safe to call concurrently.
func StopProfiling(config *ProfilingConfig) {
	profilingMu.Lock()
	defer profilingMu.Unlock()

	stopLocked(config)
}

// stopLocked implements StopProfiling; profilingMu must be held.
func stopLocked(config *ProfilingConfig) {
	state := activeProfilingState
	if state == nil {
		return
	}

	activeProfilingState = nil

	if state.cancel != nil {
		close(state.cancel)
	}

	// Stop CPU profiling
	if state.cpuFile != nil {
		pprof.StopCPUProfile()
		state.cpuFile.Close()
	}

	// Stop trace profiling
	if state.traceFile != nil {
		trace.Stop()
		state.traceFile.Close()
	}

	// Write memory profile if requested
	if config != nil && config.MemProfilePath != "" {
		_ = writeMemProfile(config.MemProfilePath)
	}
}

// ValidateProfilingConfig validates profiling configuration.
func ValidateProfilingConfig(config *ProfilingConfig) error {
	if config == nil {
		return nil
	}

	if config.CPUProfilePath == "" && config.MemProfilePath == "" && config.TracePath == "" {
		return nil
	}

	if config.ProfileDuration > 0 && config.ProfileDuration < time.Second {
		return fmt.Errorf("profile duration must be at least 1 second")
	}

	paths := []string{config.CPUProfilePath, config.MemProfilePath, config.TracePath}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := ensureDir(path); err != nil {
			return fmt.Errorf("cannot write to profile directory: %w", err)
		}
	}

	return nil
}

func startCPUProfile(path string) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return fmt.Errorf("could not start CPU profile: %w", err)
	}
	activeProfilingState.cpuFile = f
	return nil
}

func startTrace(path string) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create trace file: %w", err)
	}
	if err := trace.Start(f); err != nil {
		f.Close()
		return fmt.Errorf("could not start trace: %w", err)
	}
	activeProfilingState.traceFile = f
	return nil
}

func writeMemProfile(path string) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create memory profile: %w", err)
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		return fmt.Errorf("could not write memory profile: %w", err)
	}
	return nil
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("could not create directory %s: %w", dir, err)
		}
	}
	return nil
}
