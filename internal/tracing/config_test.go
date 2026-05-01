// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package tracing

import (
	"testing"
)

func TestValidateTracingConfig_EmptyOutputPath(t *testing.T) {
	cfg := &Config{
		OutputPath: "",
		Format:     "json",
	}
	err := ValidateTracingConfig(cfg)
	if err == nil {
		t.Fatal("expected error for empty OutputPath")
	}
	if err.Error() != "tracing output path cannot be empty" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateTracingConfig_InvalidFormat(t *testing.T) {
	cfg := &Config{
		OutputPath: "/tmp/trace.json",
		Format:     "xml",
	}
	err := ValidateTracingConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestValidateTracingConfig_ValidJSON(t *testing.T) {
	cfg := &Config{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	err := ValidateTracingConfig(cfg)
	if err != nil {
		t.Errorf("unexpected error for valid json config: %v", err)
	}
}

func TestValidateTracingConfig_ValidTimeline(t *testing.T) {
	cfg := &Config{
		OutputPath: "/tmp/trace.txt",
		Format:     "timeline",
	}
	err := ValidateTracingConfig(cfg)
	if err != nil {
		t.Errorf("unexpected error for valid timeline config: %v", err)
	}
}

func TestStartTracing_EnablesTracing(t *testing.T) {
	// Ensure clean state
	Disable()
	Clear()

	cfg := &Config{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	err := StartTracing(cfg)
	if err != nil {
		t.Fatalf("StartTracing returned error: %v", err)
	}
	if !IsEnabled() {
		t.Error("StartTracing should enable tracing")
	}

	// Cleanup
	Disable()
	Clear()
}

func TestStopTracing_WhenNotEnabled(t *testing.T) {
	Disable()
	Clear()

	cfg := &Config{
		OutputPath: "/tmp/trace.json",
		Format:     "json",
	}
	// Should not panic when tracing is not enabled
	StopTracing(cfg)
}

func TestStopTracing_ExportsAndDisables(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := tmpDir + "/trace.json"

	cfg := &Config{
		OutputPath: outputPath,
		Format:     "json",
	}

	// Start tracing and record a span
	Enable()
	Clear()
	span := StartSpan("stop-tracing-test")
	EndSpan(span)

	// StopTracing should export and disable
	StopTracing(cfg)

	if IsEnabled() {
		t.Error("StopTracing should disable tracing")
	}
}

func TestStopTracing_InvalidOutputPath(t *testing.T) {
	cfg := &Config{
		OutputPath: "/nonexistent/dir/trace.json",
		Format:     "json",
	}

	Enable()
	Clear()
	span := StartSpan("test")
	EndSpan(span)

	// Should not panic even if export fails.
	// When export fails, StopTracing returns early without calling Disable().
	StopTracing(cfg)

	// Cleanup regardless of state
	Disable()
	Clear()
}
