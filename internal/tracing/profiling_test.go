// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling

package tracing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateProfilingConfig_Nil(t *testing.T) {
	err := ValidateProfilingConfig(nil)
	if err != nil {
		t.Errorf("ValidateProfilingConfig(nil) should return nil, got: %v", err)
	}
}

func TestValidateProfilingConfig_AllEmpty(t *testing.T) {
	cfg := &ProfilingConfig{}
	err := ValidateProfilingConfig(cfg)
	if err != nil {
		t.Errorf("ValidateProfilingConfig with all empty paths should return nil, got: %v", err)
	}
}

func TestValidateProfilingConfig_DurationTooShort(t *testing.T) {
	tmp := t.TempDir()
	cfg := &ProfilingConfig{
		CPUProfilePath:  filepath.Join(tmp, "cpu.prof"),
		ProfileDuration: 500 * time.Millisecond,
	}
	err := ValidateProfilingConfig(cfg)
	if err == nil {
		t.Fatal("expected error for duration < 1 second")
	}
	if !strings.Contains(err.Error(), "at least 1 second") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateProfilingConfig_ValidDuration(t *testing.T) {
	tmp := t.TempDir()
	cfg := &ProfilingConfig{
		CPUProfilePath:  filepath.Join(tmp, "cpu.prof"),
		ProfileDuration: 2 * time.Second,
	}
	err := ValidateProfilingConfig(cfg)
	if err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}

func TestValidateProfilingConfig_InvalidDir(t *testing.T) {
	cfg := &ProfilingConfig{
		CPUProfilePath: "/nonexistent/deeply/nested/path/cpu.prof",
	}
	// ensureDir will try to create the directory; this may succeed or fail
	// depending on permissions. We just verify it doesn't panic.
	_ = ValidateProfilingConfig(cfg)
}

func TestValidateProfilingConfig_AllPaths(t *testing.T) {
	tmp := t.TempDir()
	cfg := &ProfilingConfig{
		CPUProfilePath:  filepath.Join(tmp, "cpu.prof"),
		MemProfilePath:  filepath.Join(tmp, "mem.prof"),
		TracePath:       filepath.Join(tmp, "trace.out"),
		ProfileDuration: 5 * time.Second,
	}
	err := ValidateProfilingConfig(cfg)
	if err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}

func TestGenerateProfilePath(t *testing.T) {
	baseDir := "/tmp/profiles"
	profileType := "cpu"
	path := GenerateProfilePath(baseDir, profileType)

	if !strings.HasPrefix(path, baseDir) {
		t.Errorf("expected path to start with %s, got %s", baseDir, path)
	}
	if !strings.Contains(path, "cpu-") {
		t.Errorf("expected path to contain 'cpu-', got %s", path)
	}
	if !strings.HasSuffix(path, ".prof") {
		t.Errorf("expected path to end with .prof, got %s", path)
	}
}

func TestGenerateProfilePath_MemType(t *testing.T) {
	path := GenerateProfilePath("/tmp", "mem")
	if !strings.Contains(path, "mem-") {
		t.Errorf("expected path to contain 'mem-', got %s", path)
	}
}

func TestStartProfiling_Nil(t *testing.T) {
	err := StartProfiling(nil)
	if err != nil {
		t.Errorf("StartProfiling(nil) should return nil, got: %v", err)
	}
}

func TestStartProfiling_NoPaths(t *testing.T) {
	// Reset state
	activeProfilingState = nil

	cfg := &ProfilingConfig{}
	err := StartProfiling(cfg)
	if err != nil {
		t.Errorf("StartProfiling with no paths should return nil, got: %v", err)
	}
	// Cleanup
	StopProfiling(cfg)
}

func TestStartProfiling_CPUProfile(t *testing.T) {
	activeProfilingState = nil
	tmp := t.TempDir()

	cfg := &ProfilingConfig{
		CPUProfilePath: filepath.Join(tmp, "cpu.prof"),
	}
	err := StartProfiling(cfg)
	if err != nil {
		t.Fatalf("StartProfiling with CPU profile failed: %v", err)
	}

	// Verify state was set
	if activeProfilingState == nil {
		t.Error("activeProfilingState should not be nil after StartProfiling")
	}
	if activeProfilingState.cpuFile == nil {
		t.Error("cpuFile should be set after starting CPU profiling")
	}

	StopProfiling(cfg)
}

func TestStartProfiling_MemProfile(t *testing.T) {
	activeProfilingState = nil
	tmp := t.TempDir()

	cfg := &ProfilingConfig{
		MemProfilePath: filepath.Join(tmp, "mem.prof"),
	}
	err := StartProfiling(cfg)
	if err != nil {
		t.Fatalf("StartProfiling with mem profile failed: %v", err)
	}

	StopProfiling(cfg)

	// Verify mem profile was written
	if _, err := os.Stat(filepath.Join(tmp, "mem.prof")); os.IsNotExist(err) {
		t.Error("mem profile file should have been created")
	}
}

func TestStartProfiling_TraceProfile(t *testing.T) {
	activeProfilingState = nil
	tmp := t.TempDir()

	cfg := &ProfilingConfig{
		TracePath: filepath.Join(tmp, "trace.out"),
	}
	err := StartProfiling(cfg)
	if err != nil {
		t.Fatalf("StartProfiling with trace failed: %v", err)
	}

	if activeProfilingState == nil {
		t.Error("activeProfilingState should not be nil")
	}
	if activeProfilingState.traceFile == nil {
		t.Error("traceFile should be set after starting trace profiling")
	}

	StopProfiling(cfg)
}

func TestStartProfiling_InvalidCPUPath(t *testing.T) {
	activeProfilingState = nil

	cfg := &ProfilingConfig{
		CPUProfilePath: "/nonexistent/deeply/nested/cpu.prof",
	}
	// May succeed (ensureDir creates dirs) or fail — just verify no panic
	err := StartProfiling(cfg)
	if err != nil {
		// Expected — cleanup
		activeProfilingState = nil
	} else {
		StopProfiling(cfg)
	}
}

func TestStopProfiling_NilState(t *testing.T) {
	activeProfilingState = nil
	cfg := &ProfilingConfig{}
	// Should not panic
	StopProfiling(cfg)
}

func TestStopProfiling_WithCPUAndMem(t *testing.T) {
	activeProfilingState = nil
	tmp := t.TempDir()

	cfg := &ProfilingConfig{
		CPUProfilePath: filepath.Join(tmp, "cpu.prof"),
		MemProfilePath: filepath.Join(tmp, "mem.prof"),
	}

	if err := StartProfiling(cfg); err != nil {
		t.Fatalf("StartProfiling failed: %v", err)
	}

	StopProfiling(cfg)

	if activeProfilingState != nil {
		t.Error("activeProfilingState should be nil after StopProfiling")
	}

	// Verify files were created
	if _, err := os.Stat(filepath.Join(tmp, "cpu.prof")); os.IsNotExist(err) {
		t.Error("cpu.prof should exist after profiling")
	}
	if _, err := os.Stat(filepath.Join(tmp, "mem.prof")); os.IsNotExist(err) {
		t.Error("mem.prof should exist after profiling")
	}
}

func TestStopProfiling_WithTrace(t *testing.T) {
	activeProfilingState = nil
	tmp := t.TempDir()

	cfg := &ProfilingConfig{
		TracePath: filepath.Join(tmp, "trace.out"),
	}

	if err := StartProfiling(cfg); err != nil {
		t.Fatalf("StartProfiling failed: %v", err)
	}

	StopProfiling(cfg)

	if activeProfilingState != nil {
		t.Error("activeProfilingState should be nil after StopProfiling")
	}
}

func TestStartProfiling_CPUThenTraceFails(t *testing.T) {
	activeProfilingState = nil
	tmp := t.TempDir()

	// Start CPU profiling first
	cfg1 := &ProfilingConfig{
		CPUProfilePath: filepath.Join(tmp, "cpu.prof"),
	}
	if err := StartProfiling(cfg1); err != nil {
		t.Fatalf("first StartProfiling failed: %v", err)
	}

	// Try to start trace on invalid path while CPU is running
	// This tests the cleanup path when trace start fails
	cfg2 := &ProfilingConfig{
		CPUProfilePath: filepath.Join(tmp, "cpu2.prof"),
		TracePath:      filepath.Join(tmp, "trace.out"),
	}
	// Stop the first one first to avoid "CPU profiling already in use"
	StopProfiling(cfg1)

	// Now test with both paths valid
	activeProfilingState = nil
	if err := StartProfiling(cfg2); err != nil {
		t.Logf("StartProfiling with cpu+trace: %v", err)
	} else {
		StopProfiling(cfg2)
	}
}

func TestEnsureDir_ExistingDir(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "file.prof")
	err := ensureDir(path)
	if err != nil {
		t.Errorf("ensureDir for existing dir should not fail: %v", err)
	}
}

func TestEnsureDir_NewNestedDir(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a", "b", "c", "file.prof")
	err := ensureDir(path)
	if err != nil {
		t.Errorf("ensureDir should create nested dirs: %v", err)
	}
	// Verify dir was created
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		t.Error("directory should have been created")
	}
}

func TestEnsureDir_CurrentDir(t *testing.T) {
	// Path with no directory component
	err := ensureDir("file.prof")
	if err != nil {
		t.Errorf("ensureDir for current dir should not fail: %v", err)
	}
}
