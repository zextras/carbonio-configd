// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling && tracing

package main

import (
	"flag"
	"os"
	"testing"
)

// TestParseArgs_CPUProfile tests the -cpuprofile flag (requires profiling build tag)
func TestParseArgs_CPUProfile(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"configd", "-cpuprofile=cpu.prof"}

	args := ParseArgs()

	if args.CPUProfile != "cpu.prof" {
		t.Errorf("Expected CPUProfile='cpu.prof', got %s", args.CPUProfile)
	}
}

// TestParseArgs_MemProfile tests the -memprofile flag (requires profiling build tag)
func TestParseArgs_MemProfile(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"configd", "-memprofile=mem.prof"}

	args := ParseArgs()

	if args.MemProfile != "mem.prof" {
		t.Errorf("Expected MemProfile='mem.prof', got %s", args.MemProfile)
	}
}

// TestParseArgs_Trace tests the -trace flag (requires profiling build tag)
func TestParseArgs_Trace(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"configd", "-trace=trace.out"}

	args := ParseArgs()

	if args.Trace != "trace.out" {
		t.Errorf("Expected Trace='trace.out', got %s", args.Trace)
	}
}

// TestParseArgs_ProfileDuration tests the -profile-duration flag (requires profiling build tag)
func TestParseArgs_ProfileDuration(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"configd", "-profile-duration=30"}

	args := ParseArgs()

	if args.ProfileDuration != 30 {
		t.Errorf("Expected ProfileDuration=30, got %d", args.ProfileDuration)
	}
}

// TestParseArgs_EnableTracing tests the -enable-tracing flag (requires tracing build tag)
func TestParseArgs_EnableTracing(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"configd", "-enable-tracing"}

	args := ParseArgs()

	if !args.EnableTracing {
		t.Error("EnableTracing should be true")
	}
}

// TestParseArgs_TracingOutput tests the -tracing-output flag (requires tracing build tag)
func TestParseArgs_TracingOutput(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"configd", "-tracing-output=custom-trace.json"}

	args := ParseArgs()

	if args.TracingOutput != "custom-trace.json" {
		t.Errorf("Expected TracingOutput='custom-trace.json', got %s", args.TracingOutput)
	}
}

// TestParseArgs_MultipleFlags_Profiling tests multiple flags including profiling/tracing flags
func TestParseArgs_MultipleFlags_Profiling(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{
		"configd",
		"-cpuprofile=cpu.prof",
		"-memprofile=mem.prof",
		"-profile-duration=60",
		"-enable-tracing",
		"-tracing-output=trace.json",
		"-disable-restarts",
		"mta",
		"proxy",
	}

	args := ParseArgs()

	if args.CPUProfile != "cpu.prof" {
		t.Errorf("Expected CPUProfile='cpu.prof', got %s", args.CPUProfile)
	}

	if args.MemProfile != "mem.prof" {
		t.Errorf("Expected MemProfile='mem.prof', got %s", args.MemProfile)
	}

	if args.ProfileDuration != 60 {
		t.Errorf("Expected ProfileDuration=60, got %d", args.ProfileDuration)
	}

	if !args.EnableTracing {
		t.Error("EnableTracing should be true")
	}

	if args.TracingOutput != "trace.json" {
		t.Errorf("Expected TracingOutput='trace.json', got %s", args.TracingOutput)
	}

	if !args.DisableRestarts {
		t.Error("DisableRestarts should be true")
	}

	expectedConfigs := []string{"mta", "proxy"}
	if len(args.ForcedConfigs) != len(expectedConfigs) {
		t.Errorf("Expected %d configs, got %d", len(expectedConfigs), len(args.ForcedConfigs))
	}

	for i, expected := range expectedConfigs {
		if args.ForcedConfigs[i] != expected {
			t.Errorf("Config at %d: expected %s, got %s", i, expected, args.ForcedConfigs[i])
		}
	}
}

// TestParseArgs_Defaults_Profiling tests default values when profiling/tracing are enabled
func TestParseArgs_Defaults_Profiling(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{"configd"}

	args := ParseArgs()

	if args.CPUProfile != "" {
		t.Errorf("CPUProfile should be empty, got %s", args.CPUProfile)
	}

	if args.MemProfile != "" {
		t.Errorf("MemProfile should be empty, got %s", args.MemProfile)
	}

	if args.Trace != "" {
		t.Errorf("Trace should be empty, got %s", args.Trace)
	}

	if args.ProfileDuration != 0 {
		t.Errorf("ProfileDuration should be 0, got %d", args.ProfileDuration)
	}

	if args.EnableTracing {
		t.Error("EnableTracing should be false by default")
	}

	if args.TracingOutput != "trace-spans.json" {
		t.Errorf("TracingOutput should default to 'trace-spans.json', got %s", args.TracingOutput)
	}
}
