// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

package main

import (
	"testing"
)

func TestTracingConfig_TypeAlias(t *testing.T) {
	// Verify TracingConfig is usable as a type alias
	cfg := &TracingConfig{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	if cfg.OutputPath != "/tmp/trace.json" {
		t.Errorf("unexpected OutputPath: %s", cfg.OutputPath)
	}
	if cfg.Format != "json" {
		t.Errorf("unexpected Format: %s", cfg.Format)
	}
}

func TestValidateTracingConfig_Delegation(t *testing.T) {
	cfg := &TracingConfig{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	// In noop mode, always returns nil
	err := ValidateTracingConfig(cfg)
	if err != nil {
		t.Errorf("noop ValidateTracingConfig delegation returned unexpected error: %v", err)
	}
}

func TestValidateTracingConfig_NilDelegation(t *testing.T) {
	err := ValidateTracingConfig(nil)
	if err != nil {
		t.Errorf("ValidateTracingConfig(nil) delegation returned unexpected error: %v", err)
	}
}

func TestStartTracing_Delegation(t *testing.T) {
	cfg := &TracingConfig{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	err := StartTracing(cfg)
	if err != nil {
		t.Errorf("StartTracing delegation returned unexpected error: %v", err)
	}
}

func TestStartTracing_NilDelegation(t *testing.T) {
	err := StartTracing(nil)
	if err != nil {
		t.Errorf("StartTracing(nil) delegation returned unexpected error: %v", err)
	}
}

func TestStopTracing_Delegation(t *testing.T) {
	cfg := &TracingConfig{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	// Should not panic
	StopTracing(cfg)
}

func TestStopTracing_NilDelegation(t *testing.T) {
	// Should not panic
	StopTracing(nil)
}
