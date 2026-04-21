// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected string
	}{
		{name: "milliseconds", input: 250 * time.Millisecond, expected: "250ms"},
		{name: "one second", input: time.Second, expected: "1.0s"},
		{name: "fractional seconds", input: 1500 * time.Millisecond, expected: "1.5s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.input); got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestParseSystemctlShow(t *testing.T) {
	input := "MainPID=123\nActiveEnterTimestamp=Mon 2026-04-13 10:00:00 UTC\nMemoryCurrent=1048576\nIgnoredLine\n"
	props := parseSystemctlShow(input)

	if props["MainPID"] != "123" {
		t.Fatalf("expected MainPID=123, got %q", props["MainPID"])
	}
	if props["ActiveEnterTimestamp"] != "Mon 2026-04-13 10:00:00 UTC" {
		t.Fatalf("unexpected ActiveEnterTimestamp: %q", props["ActiveEnterTimestamp"])
	}
	if props["MemoryCurrent"] != "1048576" {
		t.Fatalf("expected MemoryCurrent=1048576, got %q", props["MemoryCurrent"])
	}
	if _, ok := props["IgnoredLine"]; ok {
		t.Fatal("expected line without '=' to be ignored")
	}
}

func TestApplyFilters(t *testing.T) {
	config := map[string]string{
		"known":  "value",
		"custom": "override",
	}

	defaultFiltered := applyFilters(config, &localconfigOpts{showDefaults: true})
	if len(defaultFiltered) == 0 {
		t.Fatal("expected defaults to be returned")
	}

	changedFiltered := applyFilters(config, &localconfigOpts{showChanged: true})
	if changedFiltered["custom"] != "override" {
		t.Fatalf("expected changed config to preserve custom key, got %q", changedFiltered["custom"])
	}

	plain := applyFilters(config, &localconfigOpts{})
	if plain["known"] != "value" {
		t.Fatalf("expected plain config unchanged, got %q", plain["known"])
	}
}

func TestFilterKeys(t *testing.T) {
	config := map[string]string{
		"a": "1",
		"b": "2",
	}

	all := filterKeys(config, &localconfigOpts{})
	if len(all) != 2 {
		t.Fatalf("expected unfiltered config, got %d keys", len(all))
	}

	filtered := filterKeys(config, &localconfigOpts{keys: []string{"b", "missing"}, quiet: true})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered key, got %d", len(filtered))
	}
	if filtered["b"] != "2" {
		t.Fatalf("expected key b=2, got %q", filtered["b"])
	}
}

func TestNoopProfilingAndTracing(t *testing.T) {
	p := &ProfilingConfig{}
	if err := StartProfiling(p); err != nil {
		t.Fatalf("expected nil profiling start error, got %v", err)
	}
	if err := ValidateProfilingConfig(p); err != nil {
		t.Fatalf("expected nil profiling validation error, got %v", err)
	}
	StopProfiling(p)

	tr := &TracingConfig{}
	if err := ValidateTracingConfig(tr); err != nil {
		t.Fatalf("expected nil tracing validation error, got %v", err)
	}
	if err := StartTracing(tr); err != nil {
		t.Fatalf("expected nil tracing start error, got %v", err)
	}
	StopTracing(tr)
}

func TestCLIToArgs(t *testing.T) {
	cli := &CLI{DisableRestarts: true}
	args := cli.toArgs()
	if !args.DisableRestarts {
		t.Fatal("expected DisableRestarts to be propagated")
	}
}

func TestIsTTY_RegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if isTTY(f) {
		t.Error("expected regular file to not be a TTY")
	}
}

func TestIsTTY_NonFile(t *testing.T) {
	// bytes.Buffer is not an *os.File, must return false
	if isTTY(&bytes.Buffer{}) {
		t.Error("expected non-file writer to not be a TTY")
	}
}

func TestInitCLIColors_NoTTY(t *testing.T) {
	// Running in test (not a TTY), so colors should remain empty strings
	initCLIColors()
	// No assertion on values — just verify no panic and colors are not set
	// (stdout in tests is never a TTY)
}

func TestInitCLILogging_NoPanic(t *testing.T) {
	initCLILogging()
}

func TestCliStatus_Running(t *testing.T) {
	// Redirect stdout to capture output
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cliStatus("TestService", true, "")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "TestService") {
		t.Errorf("expected service name in output, got %q", out)
	}
}

func TestCliStatus_Stopped(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cliStatus("StoppedSvc", false, "")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "StoppedSvc") {
		t.Errorf("expected service name in output, got %q", out)
	}
}

func TestCliStatus_WithDetail(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cliStatus("DetailSvc", true, "(pid 1234, since yesterday)")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "DetailSvc") {
		t.Errorf("expected service name in output, got %q", out)
	}
	if !strings.Contains(out, "pid 1234") {
		t.Errorf("expected detail in output, got %q", out)
	}
}

func TestCliWarn(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cliWarn("disk space %dMB available", 42)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING in output, got %q", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("expected formatted value in output, got %q", out)
	}
}

func TestCliHeader(t *testing.T) {
	cliHeaderPrinted = false

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cliHeader()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "Host") {
		t.Errorf("expected 'Host' in output, got %q", out)
	}
}

func TestCliProgress_Success(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := cliProgress("Starting", "TestService")
	done(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "Starting") {
		t.Errorf("expected 'Starting' in output, got %q", out)
	}
	if !strings.Contains(out, "Done") {
		t.Errorf("expected 'Done.' in output, got %q", out)
	}
}

func TestCliProgress_Failure(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := cliProgress("Stopping", "BadService")
	done(fmt.Errorf("unit not found"))

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	if !strings.Contains(out, "Failed") {
		t.Errorf("expected 'Failed.' in output, got %q", out)
	}
	if !strings.Contains(out, "unit not found") {
		t.Errorf("expected error message in output, got %q", out)
	}
}

func TestFormatDuration_Zero(t *testing.T) {
	got := formatDuration(0)
	if got != "0ms" {
		t.Errorf("expected '0ms', got %q", got)
	}
}

func TestFormatDuration_Boundary(t *testing.T) {
	// Exactly 1 second should use seconds form
	got := formatDuration(time.Second)
	if got != "1.0s" {
		t.Errorf("expected '1.0s', got %q", got)
	}
}

func TestProfilingArgs_ApplyTo(t *testing.T) {
	p := &ProfilingArgs{}
	args := &Args{}
	p.applyTo(args) // no-op, must not panic
}

func TestTracingArgs_ApplyTo(t *testing.T) {
	tr := &TracingArgs{}
	args := &Args{}
	tr.applyTo(args) // no-op, must not panic
}

func TestStopProfiling_NilConfig(t *testing.T) {
	// StopProfiling with a nil config must not panic
	StopProfiling(nil)
}

func TestStopProfiling_EmptyConfig(t *testing.T) {
	p := &ProfilingConfig{}
	StopProfiling(p)
}

func TestStopTracing_NilConfig(t *testing.T) {
	StopTracing(nil)
}

func TestStopTracing_EmptyConfig(t *testing.T) {
	tr := &TracingConfig{}
	StopTracing(tr)
}

func TestSetupProfilingAndTracing_NoFlags(t *testing.T) {
	ctx := context.Background()
	args := &Args{}
	pc, tc := setupProfilingAndTracing(ctx, args)
	if pc != nil {
		t.Error("expected nil ProfilingConfig when no profiling flags set")
	}
	if tc != nil {
		t.Error("expected nil TracingConfig when no tracing flags set")
	}
}

func TestInitCLILogging_WithEnvVar(t *testing.T) {
	os.Setenv("CONFIGD_LOG_LEVEL", "debug")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	initCLILogging()
}
