// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !profiling

package tracing

import (
	"testing"
	"time"
)

func TestNoop_StartProfiling_ReturnsNil(t *testing.T) {
	cfg := &ProfilingConfig{
		CPUProfilePath: "/tmp/cpu.prof",
	}
	err := StartProfiling(cfg)
	if err != nil {
		t.Errorf("noop StartProfiling should return nil, got: %v", err)
	}
}

func TestNoop_StartProfiling_NilConfig(t *testing.T) {
	err := StartProfiling(nil)
	if err != nil {
		t.Errorf("noop StartProfiling(nil) should return nil, got: %v", err)
	}
}

func TestNoop_StopProfiling_DoesNotPanic(t *testing.T) {
	cfg := &ProfilingConfig{
		CPUProfilePath: "/tmp/cpu.prof",
	}
	// Should not panic
	StopProfiling(cfg)
}

func TestNoop_StopProfiling_NilConfig(t *testing.T) {
	// Should not panic
	StopProfiling(nil)
}

func TestNoop_ValidateProfilingConfig_ReturnsNil(t *testing.T) {
	cfg := &ProfilingConfig{
		CPUProfilePath:  "/tmp/cpu.prof",
		ProfileDuration: 100 * time.Millisecond, // would fail in real impl
	}
	err := ValidateProfilingConfig(cfg)
	if err != nil {
		t.Errorf("noop ValidateProfilingConfig should return nil, got: %v", err)
	}
}

func TestNoop_ValidateProfilingConfig_NilConfig(t *testing.T) {
	err := ValidateProfilingConfig(nil)
	if err != nil {
		t.Errorf("noop ValidateProfilingConfig(nil) should return nil, got: %v", err)
	}
}

func TestNoop_GenerateProfilePath_ReturnsEmpty(t *testing.T) {
	path := GenerateProfilePath("/tmp", "cpu")
	if path != "" {
		t.Errorf("noop GenerateProfilePath should return empty string, got: %s", path)
	}
}

func TestNoop_GenerateProfilePath_AnyArgs(t *testing.T) {
	path := GenerateProfilePath("", "")
	if path != "" {
		t.Errorf("noop GenerateProfilePath should return empty string, got: %s", path)
	}
}
