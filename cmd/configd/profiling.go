// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling

package main

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/logger"
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

// ProfilingState holds active profiling state.
type ProfilingState struct {
	cpuFile   *os.File
	traceFile *os.File
	startTime time.Time
}

var profilingState *ProfilingState

// StartProfiling initializes profiling based on configuration.
// Returns an error if profiling cannot be started.
func StartProfiling(config *ProfilingConfig) error {
	if config == nil {
		return nil
	}

	profilingState = &ProfilingState{
		startTime: time.Now(),
	}

	ctx := context.Background()
	ctx = logger.ContextWithComponent(ctx, "profiling")

	// Start CPU profiling if requested
	if config.CPUProfilePath != "" {
		if err := startCPUProfile(config.CPUProfilePath); err != nil {
			return fmt.Errorf("failed to start CPU profiling: %w", err)
		}
		logger.InfoContext(ctx, "CPU profiling started",
			"path", config.CPUProfilePath)
	}

	// Start trace profiling if requested
	if config.TracePath != "" {
		if err := startTrace(config.TracePath); err != nil {
			// Stop CPU profiling if it was started
			if profilingState.cpuFile != nil {
				pprof.StopCPUProfile()
				profilingState.cpuFile.Close()
			}
			return fmt.Errorf("failed to start trace profiling: %w", err)
		}
		logger.InfoContext(ctx, "Trace profiling started",
			"path", config.TracePath)
	}

	// Schedule automatic stop if ProfileDuration is set
	if config.ProfileDuration > 0 {
		logger.InfoContext(ctx, "Profiling will stop automatically",
			"duration_seconds", config.ProfileDuration.Seconds())
		go func() {
			time.Sleep(config.ProfileDuration)
			logger.InfoContext(ctx, "Profile duration reached, stopping profiling")
			StopProfiling(config)
		}()
	}

	return nil
}

// StopProfiling stops all active profiling and writes profiles to disk.
func StopProfiling(config *ProfilingConfig) {
	if profilingState == nil {
		return
	}

	ctx := context.Background()
	ctx = logger.ContextWithComponent(ctx, "profiling")
	duration := time.Since(profilingState.startTime)

	// Stop CPU profiling
	if profilingState.cpuFile != nil {
		pprof.StopCPUProfile()
		profilingState.cpuFile.Close()
		logger.InfoContext(ctx, "CPU profile written",
			"path", config.CPUProfilePath,
			"duration_seconds", duration.Seconds())
	}

	// Stop trace profiling
	if profilingState.traceFile != nil {
		trace.Stop()
		profilingState.traceFile.Close()
		logger.InfoContext(ctx, "Trace profile written",
			"path", config.TracePath,
			"duration_seconds", duration.Seconds())
	}

	// Write memory profile if requested
	if config.MemProfilePath != "" {
		if err := writeMemProfile(config.MemProfilePath); err != nil {
			logger.ErrorContext(ctx, "Failed to write memory profile",
				"error", err)
		} else {
			logger.InfoContext(ctx, "Memory profile written",
				"path", config.MemProfilePath)
		}
	}

	profilingState = nil
}

// startCPUProfile starts CPU profiling to the specified file.
func startCPUProfile(path string) error {
	// Ensure directory exists
	if err := ensureDir(path); err != nil {
		return err
	}

	// Create profile file
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create CPU profile: %w", err)
	}

	// Start profiling
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return fmt.Errorf("could not start CPU profile: %w", err)
	}

	profilingState.cpuFile = f
	return nil
}

// startTrace starts execution tracing to the specified file.
func startTrace(path string) error {
	// Ensure directory exists
	if err := ensureDir(path); err != nil {
		return err
	}

	// Create trace file
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create trace file: %w", err)
	}

	// Start tracing
	if err := trace.Start(f); err != nil {
		f.Close()
		return fmt.Errorf("could not start trace: %w", err)
	}

	profilingState.traceFile = f
	return nil
}

// writeMemProfile writes a heap profile to the specified file.
func writeMemProfile(path string) error {
	// Ensure directory exists
	if err := ensureDir(path); err != nil {
		return err
	}

	// Create profile file
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create memory profile: %w", err)
	}
	defer f.Close()

	// Force garbage collection to get accurate memory profile
	runtime.GC()

	// Write heap profile
	if err := pprof.WriteHeapProfile(f); err != nil {
		return fmt.Errorf("could not write memory profile: %w", err)
	}

	return nil
}

// ensureDir ensures the directory for the given file path exists.
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}

	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// Create directory with appropriate permissions
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("could not create directory %s: %w", dir, err)
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

// ValidateProfilingConfig validates profiling configuration.
func ValidateProfilingConfig(config *ProfilingConfig) error {
	if config == nil {
		return nil
	}

	// Check if any profiling is enabled
	if config.CPUProfilePath == "" && config.MemProfilePath == "" && config.TracePath == "" {
		return nil
	}

	// Validate profile duration if set
	if config.ProfileDuration > 0 && config.ProfileDuration < time.Second {
		return fmt.Errorf("profile duration must be at least 1 second")
	}

	// Check write permissions for profile paths
	paths := []string{config.CPUProfilePath, config.MemProfilePath, config.TracePath}
	for _, path := range paths {
		if path == "" {
			continue
		}

		dir := filepath.Dir(path)
		if dir == "" || dir == "." {
			dir = "."
		}

		// Check if directory is writable by trying to create it
		if err := ensureDir(path); err != nil {
			return fmt.Errorf("cannot write to profile directory %s: %w", dir, err)
		}
	}

	return nil
}
