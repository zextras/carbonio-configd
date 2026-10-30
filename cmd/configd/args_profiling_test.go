// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling && tracing

package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

// parseCLIArgs parses argv (without the program name) through the Kong CLI and
// returns the resulting CLI and its Args conversion. Callers pass a trivial
// subcommand so that Kong's required-command check is satisfied.
func parseCLIArgs(t *testing.T, argv ...string) (*CLI, *Args) {
	t.Helper()

	cli := &CLI{}

	parser, err := kong.New(cli, kong.Vars{"version": "test"})
	if err != nil {
		t.Fatalf("kong.New failed: %v", err)
	}

	if _, err := parser.Parse(argv); err != nil {
		t.Fatalf("Parse(%v) failed: %v", argv, err)
	}

	return cli, cli.toArgs()
}

func TestCLI_CPUProfile(t *testing.T) {
	_, args := parseCLIArgs(t, "--cpuprofile=cpu.prof", "release")
	if args.CPUProfile != "cpu.prof" {
		t.Errorf("Expected CPUProfile='cpu.prof', got %s", args.CPUProfile)
	}
}

func TestCLI_MemProfile(t *testing.T) {
	_, args := parseCLIArgs(t, "--memprofile=mem.prof", "release")
	if args.MemProfile != "mem.prof" {
		t.Errorf("Expected MemProfile='mem.prof', got %s", args.MemProfile)
	}
}

func TestCLI_Trace(t *testing.T) {
	_, args := parseCLIArgs(t, "--trace=trace.out", "release")
	if args.Trace != "trace.out" {
		t.Errorf("Expected Trace='trace.out', got %s", args.Trace)
	}
}

func TestCLI_ProfileDuration(t *testing.T) {
	_, args := parseCLIArgs(t, "--profile-duration=30", "release")
	if args.ProfileDuration != 30 {
		t.Errorf("Expected ProfileDuration=30, got %d", args.ProfileDuration)
	}
}

func TestCLI_Once(t *testing.T) {
	_, args := parseCLIArgs(t, "--once", "release")
	if !args.Once {
		t.Error("Once should be true")
	}
}

func TestCLI_EnableTracing(t *testing.T) {
	_, args := parseCLIArgs(t, "--enable-tracing", "release")
	if !args.EnableTracing {
		t.Error("EnableTracing should be true")
	}
}

func TestCLI_TracingOutput(t *testing.T) {
	_, args := parseCLIArgs(t, "--tracing-output=custom-trace.json", "release")
	if args.TracingOutput != "custom-trace.json" {
		t.Errorf("Expected TracingOutput='custom-trace.json', got %s", args.TracingOutput)
	}
}

func TestCLI_MultipleFlags_Profiling(t *testing.T) {
	_, args := parseCLIArgs(t,
		"--cpuprofile=cpu.prof",
		"--memprofile=mem.prof",
		"--profile-duration=60",
		"--enable-tracing",
		"--tracing-output=trace.json",
		"--disable-restarts",
		"release",
	)

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
}

func TestCLI_Defaults_Profiling(t *testing.T) {
	_, args := parseCLIArgs(t, "release")

	if args.CPUProfile != "" || args.MemProfile != "" || args.Trace != "" {
		t.Errorf("profile outputs should be empty, got cpu=%q mem=%q trace=%q",
			args.CPUProfile, args.MemProfile, args.Trace)
	}

	if args.ProfileDuration != 0 {
		t.Errorf("ProfileDuration should be 0, got %d", args.ProfileDuration)
	}

	if args.Once {
		t.Error("Once should be false by default")
	}

	if args.EnableTracing {
		t.Error("EnableTracing should be false by default")
	}

	if args.TracingOutput != "trace-spans.json" {
		t.Errorf("TracingOutput should default to 'trace-spans.json', got %s", args.TracingOutput)
	}
}
