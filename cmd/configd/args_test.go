// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
)

func TestArgs_HasForcedConfigs(t *testing.T) {
	tests := []struct {
		name     string
		args     *Args
		expected bool
	}{
		{
			name: "no forced configs",
			args: &Args{
				ForcedConfigs: []string{},
			},
			expected: false,
		},
		{
			name: "nil forced configs",
			args: &Args{
				ForcedConfigs: nil,
			},
			expected: false,
		},
		{
			name: "single forced config",
			args: &Args{
				ForcedConfigs: []string{"proxy"},
			},
			expected: true,
		},
		{
			name: "multiple forced configs",
			args: &Args{
				ForcedConfigs: []string{"proxy", "mta"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.args.HasForcedConfigs()
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestArgs_Fields(t *testing.T) {
	args := &Args{
		ForcedConfigs:   []string{"proxy", "mta"},
		CPUProfile:      "/tmp/cpu.prof",
		MemProfile:      "/tmp/mem.prof",
		Trace:           "/tmp/trace.out",
		ProfileDuration: 30,
		EnableTracing:   true,
		TracingOutput:   "/tmp/spans.json",
		DisableRestarts: true,
		Once:            true,
	}

	if len(args.ForcedConfigs) != 2 {
		t.Errorf("expected 2 forced configs, got %d", len(args.ForcedConfigs))
	}
	if args.CPUProfile != "/tmp/cpu.prof" {
		t.Errorf("expected CPUProfile /tmp/cpu.prof, got %s", args.CPUProfile)
	}
	if args.MemProfile != "/tmp/mem.prof" {
		t.Errorf("expected MemProfile /tmp/mem.prof, got %s", args.MemProfile)
	}
	if args.Trace != "/tmp/trace.out" {
		t.Errorf("expected Trace /tmp/trace.out, got %s", args.Trace)
	}
	if args.ProfileDuration != 30 {
		t.Errorf("expected ProfileDuration 30, got %d", args.ProfileDuration)
	}
	if !args.EnableTracing {
		t.Error("expected EnableTracing true")
	}
	if args.TracingOutput != "/tmp/spans.json" {
		t.Errorf("expected TracingOutput /tmp/spans.json, got %s", args.TracingOutput)
	}
	if !args.DisableRestarts {
		t.Error("expected DisableRestarts true")
	}
	if !args.Once {
		t.Error("expected Once true")
	}
}

func TestArgs_DefaultValues(t *testing.T) {
	args := &Args{}

	if args.HasForcedConfigs() {
		t.Error("expected no forced configs by default")
	}
	if args.CPUProfile != "" {
		t.Errorf("expected empty CPUProfile, got %s", args.CPUProfile)
	}
	if args.MemProfile != "" {
		t.Errorf("expected empty MemProfile, got %s", args.MemProfile)
	}
	if args.Trace != "" {
		t.Errorf("expected empty Trace, got %s", args.Trace)
	}
	if args.ProfileDuration != 0 {
		t.Errorf("expected ProfileDuration 0, got %d", args.ProfileDuration)
	}
	if args.EnableTracing {
		t.Error("expected EnableTracing false by default")
	}
	if args.TracingOutput != "" {
		t.Errorf("expected empty TracingOutput, got %s", args.TracingOutput)
	}
	if args.DisableRestarts {
		t.Error("expected DisableRestarts false by default")
	}
	if args.Once {
		t.Error("expected Once false by default")
	}
}
