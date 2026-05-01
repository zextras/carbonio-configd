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
}

var activeProfilingState *profilingState

// StartProfiling initializes profiling based on configuration.
// Returns an error if profiling cannot be started.
func StartProfiling(config *ProfilingConfig) error {
	if config == nil {
		return nil
	}

	activeProfilingState = &profilingState{
		startTime: time.Now(),
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
		go func() {
			time.Sleep(config.ProfileDuration)
			StopProfiling(config)
		}()
	}

	return nil
}

// StopProfiling stops all active profiling and writes profiles to disk.
func StopProfiling(config *ProfilingConfig) {
	if activeProfilingState == nil {
		return
	}

	// Stop CPU profiling
	if activeProfilingState.cpuFile != nil {
		pprof.StopCPUProfile()
		activeProfilingState.cpuFile.Close()
	}

	// Stop trace profiling
	if activeProfilingState.traceFile != nil {
		trace.Stop()
		activeProfilingState.traceFile.Close()
	}

	// Write memory profile if requested
	if config.MemProfilePath != "" {
		_ = writeMemProfile(config.MemProfilePath)
	}

	activeProfilingState = nil
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

// GenerateProfilePath generates a profile file path with timestamp.
func GenerateProfilePath(baseDir, profileType string) string {
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-%s.prof", profileType, timestamp)
	return filepath.Join(baseDir, filename)
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
