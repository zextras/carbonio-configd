// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

package tracing

import (
	"testing"
)

func TestNoop_ValidateTracingConfig_ReturnsNil(t *testing.T) {
	cfg := &Config{
		OutputPath: "",
		Format:     "invalid",
	}
	err := ValidateTracingConfig(cfg)
	if err != nil {
		t.Errorf("noop ValidateTracingConfig should return nil, got: %v", err)
	}
}

func TestNoop_ValidateTracingConfig_NilConfig(t *testing.T) {
	// Even with nil, noop should not panic
	err := ValidateTracingConfig(nil)
	if err != nil {
		t.Errorf("noop ValidateTracingConfig(nil) should return nil, got: %v", err)
	}
}

func TestNoop_StartTracing_ReturnsNil(t *testing.T) {
	cfg := &Config{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	err := StartTracing(cfg)
	if err != nil {
		t.Errorf("noop StartTracing should return nil, got: %v", err)
	}
}

func TestNoop_StartTracing_NilConfig(t *testing.T) {
	err := StartTracing(nil)
	if err != nil {
		t.Errorf("noop StartTracing(nil) should return nil, got: %v", err)
	}
}

func TestNoop_StopTracing_DoesNotPanic(t *testing.T) {
	cfg := &Config{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	// Should not panic
	StopTracing(cfg)
}

func TestNoop_StopTracing_NilConfig(t *testing.T) {
	// Should not panic with nil
	StopTracing(nil)
}
