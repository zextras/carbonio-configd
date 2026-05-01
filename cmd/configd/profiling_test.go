// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !profiling

package main

import (
	"testing"
	"time"
)

func TestProfilingConfig_TypeAlias(t *testing.T) {
	// Verify ProfilingConfig is usable as a type alias
	cfg := &ProfilingConfig{
		CPUProfilePath:  "/tmp/cpu.prof",
		MemProfilePath:  "/tmp/mem.prof",
		TracePath:       "/tmp/trace.out",
		ProfileDuration: 5 * time.Second,
	}
	if cfg.CPUProfilePath != "/tmp/cpu.prof" {
		t.Errorf("unexpected CPUProfilePath: %s", cfg.CPUProfilePath)
	}
	if cfg.MemProfilePath != "/tmp/mem.prof" {
		t.Errorf("unexpected MemProfilePath: %s", cfg.MemProfilePath)
	}
	if cfg.TracePath != "/tmp/trace.out" {
		t.Errorf("unexpected TracePath: %s", cfg.TracePath)
	}
	if cfg.ProfileDuration != 5*time.Second {
		t.Errorf("unexpected ProfileDuration: %v", cfg.ProfileDuration)
	}
}

func TestStartProfiling_Delegation(t *testing.T) {
	cfg := &ProfilingConfig{}
	err := StartProfiling(cfg)
	if err != nil {
		t.Errorf("StartProfiling delegation returned unexpected error: %v", err)
	}
}

func TestStartProfiling_NilDelegation(t *testing.T) {
	err := StartProfiling(nil)
	if err != nil {
		t.Errorf("StartProfiling(nil) delegation returned unexpected error: %v", err)
	}
}

func TestStopProfiling_Delegation(t *testing.T) {
	cfg := &ProfilingConfig{}
	// Should not panic
	StopProfiling(cfg)
}

func TestValidateProfilingConfig_Delegation(t *testing.T) {
	cfg := &ProfilingConfig{}
	err := ValidateProfilingConfig(cfg)
	if err != nil {
		t.Errorf("ValidateProfilingConfig delegation returned unexpected error: %v", err)
	}
}
