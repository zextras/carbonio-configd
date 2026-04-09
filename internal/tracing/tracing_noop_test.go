// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package tracing provides noop tests compiled without the 'tracing' build tag.
// No build tag here — this file compiles with the noop implementation by default.
package tracing

import (
	"bytes"
	"os"
	"testing"
)

// --- Tracer method tests ---

func TestNoop_Tracer_Enable(t *testing.T) {
	tr := &Tracer{}
	// Should not panic
	tr.Enable()
}

func TestNoop_Tracer_Disable(t *testing.T) {
	tr := &Tracer{}
	// Should not panic
	tr.Disable()
}

func TestNoop_Tracer_IsEnabled(t *testing.T) {
	tr := &Tracer{}
	if tr.IsEnabled() {
		t.Error("noop IsEnabled() should always return false")
	}
}

func TestNoop_Tracer_StartSpan(t *testing.T) {
	tr := &Tracer{}
	span := tr.StartSpan("op")
	if span != nil {
		t.Error("noop StartSpan() should return nil")
	}
}

func TestNoop_Tracer_EndSpan_Nil(t *testing.T) {
	tr := &Tracer{}
	// Should not panic with nil span
	tr.EndSpan(nil)
}

func TestNoop_Tracer_EndSpan_NilFromStartSpan(t *testing.T) {
	tr := &Tracer{}
	span := tr.StartSpan("op") // returns nil
	// Should not panic
	tr.EndSpan(span)
}

func TestNoop_Span_AddMetadata_NilReceiver(t *testing.T) {
	var span *Span
	// noop AddMetadata does not dereference receiver — should not panic
	span.AddMetadata("key", "value")
}

func TestNoop_Span_SetParent_NilReceiver(t *testing.T) {
	var span *Span
	// noop SetParent does not dereference receiver — should not panic
	span.SetParent("span-1")
}

func TestNoop_Tracer_GetSpans(t *testing.T) {
	tr := &Tracer{}
	spans := tr.GetSpans()
	if spans != nil {
		t.Error("noop GetSpans() should return nil")
	}
}

func TestNoop_Tracer_Clear(t *testing.T) {
	tr := &Tracer{}
	// Should not panic
	tr.Clear()
}

func TestNoop_Tracer_ExportJSON(t *testing.T) {
	tr := &Tracer{}
	var buf bytes.Buffer
	err := tr.ExportJSON(&buf)
	if err != nil {
		t.Errorf("noop ExportJSON() should return nil error, got: %v", err)
	}
}

func TestNoop_Tracer_ExportTimeline(t *testing.T) {
	tr := &Tracer{}
	var buf bytes.Buffer
	err := tr.ExportTimeline(&buf)
	if err != nil {
		t.Errorf("noop ExportTimeline() should return nil error, got: %v", err)
	}
}

func TestNoop_Tracer_ExportToFile(t *testing.T) {
	tr := &Tracer{}
	tmpDir := t.TempDir()
	filename := tmpDir + "/trace.json"

	err := tr.ExportToFile(filename, "json")
	if err != nil {
		t.Errorf("noop ExportToFile() should return nil error, got: %v", err)
	}

	// Noop should NOT create the file
	if _, statErr := os.Stat(filename); !os.IsNotExist(statErr) {
		t.Error("noop ExportToFile() should not create any file")
	}
}

// --- Global function tests ---

func TestNoop_Global_Enable(t *testing.T) {
	// Should not panic
	Enable()
}

func TestNoop_Global_Disable(t *testing.T) {
	// Should not panic
	Disable()
}

func TestNoop_Global_IsEnabled(t *testing.T) {
	if IsEnabled() {
		t.Error("noop global IsEnabled() should always return false")
	}
}

func TestNoop_Global_StartSpan(t *testing.T) {
	span := StartSpan("op")
	if span != nil {
		t.Error("noop global StartSpan() should return nil")
	}
}

func TestNoop_Global_EndSpan(t *testing.T) {
	// Should not panic with nil span
	EndSpan(nil)
}

func TestNoop_Global_GetSpans(t *testing.T) {
	spans := GetSpans()
	if spans != nil {
		t.Error("noop global GetSpans() should return nil")
	}
}

func TestNoop_Global_Clear(t *testing.T) {
	// Should not panic
	Clear()
}

func TestNoop_Global_ExportJSON(t *testing.T) {
	var buf bytes.Buffer
	err := ExportJSON(&buf)
	if err != nil {
		t.Errorf("noop global ExportJSON() should return nil error, got: %v", err)
	}
}

func TestNoop_Global_ExportTimeline(t *testing.T) {
	var buf bytes.Buffer
	err := ExportTimeline(&buf)
	if err != nil {
		t.Errorf("noop global ExportTimeline() should return nil error, got: %v", err)
	}
}

func TestNoop_Global_ExportToFile(t *testing.T) {
	tmpDir := t.TempDir()
	filename := tmpDir + "/global-trace.txt"

	err := ExportToFile(filename, "timeline")
	if err != nil {
		t.Errorf("noop global ExportToFile() should return nil error, got: %v", err)
	}

	// Noop should NOT create the file
	if _, statErr := os.Stat(filename); !os.IsNotExist(statErr) {
		t.Error("noop global ExportToFile() should not create any file")
	}
}

// --- DefaultTracer tests ---

func TestNoop_DefaultTracer_NotNil(t *testing.T) {
	if DefaultTracer == nil {
		t.Fatal("DefaultTracer should not be nil")
	}
}

func TestNoop_DefaultTracer_IsDisabled(t *testing.T) {
	if DefaultTracer.IsEnabled() {
		t.Error("DefaultTracer should be disabled in noop mode")
	}
}
