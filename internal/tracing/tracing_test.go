// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package tracing

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSpanBasics(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test-operation")
	if span == nil {
		t.Fatal("StartSpan returned nil")
	}

	if span.Name != "test-operation" {
		t.Errorf("Expected name 'test-operation', got %s", span.Name)
	}

	if span.StartTime.IsZero() {
		t.Error("Start time not set")
	}

	// Simulate some work
	time.Sleep(10 * time.Millisecond)

	span.AddMetadata("key", "value")
	tracer.EndSpan(span)

	if span.EndTime.IsZero() {
		t.Error("End time not set")
	}

	if span.Duration == 0 {
		t.Error("Duration not calculated")
	}

	if span.Metadata["key"] != "value" {
		t.Error("Metadata not set correctly")
	}

	spans := tracer.GetSpans()
	if len(spans) != 1 {
		t.Errorf("Expected 1 span, got %d", len(spans))
	}
}

func TestTracerEnableDisable(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: false,
	}

	// Should not record when disabled
	span := tracer.StartSpan("disabled")
	if span != nil {
		t.Error("StartSpan should return nil when disabled")
	}

	tracer.Enable()
	if !tracer.IsEnabled() {
		t.Error("Tracer should be enabled")
	}

	span = tracer.StartSpan("enabled")
	if span == nil {
		t.Error("StartSpan should not return nil when enabled")
	}
	tracer.EndSpan(span)

	spans := tracer.GetSpans()
	if len(spans) != 1 {
		t.Errorf("Expected 1 span, got %d", len(spans))
	}

	tracer.Disable()
	if tracer.IsEnabled() {
		t.Error("Tracer should be disabled")
	}
}

func TestParentSpan(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	parentSpan := tracer.StartSpan("parent")
	childSpan := tracer.StartSpan("child")
	childSpan.SetParent(parentSpan.SpanID)

	tracer.EndSpan(childSpan)
	tracer.EndSpan(parentSpan)

	spans := tracer.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("Expected 2 spans, got %d", len(spans))
	}

	// Find child span
	var child *Span
	for _, s := range spans {
		if s.Name == "child" {
			child = s
			break
		}
	}

	if child == nil {
		t.Fatal("Child span not found")
	}

	if child.ParentID != parentSpan.SpanID {
		t.Errorf("Expected parent ID %s, got %s", parentSpan.SpanID, child.ParentID)
	}
}

func TestExportJSON(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("json-test")
	span.AddMetadata("test", "data")
	tracer.EndSpan(span)

	var buf bytes.Buffer
	err := tracer.ExportJSON(&buf)
	if err != nil {
		t.Fatalf("ExportJSON failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "json-test") {
		t.Error("JSON output does not contain span name")
	}
	if !strings.Contains(output, "test") {
		t.Error("JSON output does not contain metadata")
	}
}

func TestExportTimeline(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span1 := tracer.StartSpan("operation-1")
	time.Sleep(5 * time.Millisecond)
	tracer.EndSpan(span1)

	span2 := tracer.StartSpan("operation-2")
	time.Sleep(5 * time.Millisecond)
	tracer.EndSpan(span2)

	var buf bytes.Buffer
	err := tracer.ExportTimeline(&buf)
	if err != nil {
		t.Fatalf("ExportTimeline failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "operation-1") {
		t.Error("Timeline output does not contain operation-1")
	}
	if !strings.Contains(output, "operation-2") {
		t.Error("Timeline output does not contain operation-2")
	}
	if !strings.Contains(output, "Summary") {
		t.Error("Timeline output does not contain summary")
	}
}

func TestClear(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("clear-test")
	tracer.EndSpan(span)

	if len(tracer.GetSpans()) != 1 {
		t.Error("Expected 1 span before clear")
	}

	tracer.Clear()

	if len(tracer.GetSpans()) != 0 {
		t.Error("Expected 0 spans after clear")
	}
}

func TestGlobalFunctions(t *testing.T) {
	// Test global convenience functions
	Enable()
	if !IsEnabled() {
		t.Error("Global Enable() did not enable tracing")
	}

	Clear()

	span := StartSpan("global-test")
	if span == nil {
		t.Fatal("Global StartSpan returned nil")
	}

	EndSpan(span)

	spans := GetSpans()
	if len(spans) != 1 {
		t.Errorf("Expected 1 span from global GetSpans, got %d", len(spans))
	}

	Disable()
	if IsEnabled() {
		t.Error("Global Disable() did not disable tracing")
	}

	// Re-enable for other tests
	Enable()
}

func TestTracer_IsEnabled_WithDisabledFlag(t *testing.T) {
	tracer := &Tracer{
		spans:    make([]*Span, 0),
		enabled:  true,
		disabled: true, // Master disable flag
	}

	if tracer.IsEnabled() {
		t.Error("Tracer should be disabled when disabled flag is true")
	}
}

func TestTracer_StartSpan_SequenceNumbers(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span1 := tracer.StartSpan("op1")
	span2 := tracer.StartSpan("op2")
	span3 := tracer.StartSpan("op3")

	if span1.SpanID != "span-1" {
		t.Errorf("Expected span-1, got %s", span1.SpanID)
	}
	if span2.SpanID != "span-2" {
		t.Errorf("Expected span-2, got %s", span2.SpanID)
	}
	if span3.SpanID != "span-3" {
		t.Errorf("Expected span-3, got %s", span3.SpanID)
	}
}

func TestTracer_EndSpan_NilSpan(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	// Should not panic
	tracer.EndSpan(nil)

	spans := tracer.GetSpans()
	if len(spans) != 0 {
		t.Errorf("Expected 0 spans after ending nil span, got %d", len(spans))
	}
}

func TestTracer_EndSpan_WhenDisabled(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test-operation")
	tracer.Disable()
	tracer.EndSpan(span)

	spans := tracer.GetSpans()
	if len(spans) != 0 {
		t.Errorf("Expected 0 spans when ending span after disabling, got %d", len(spans))
	}
}

func TestSpan_AddMetadata_NilSpan(t *testing.T) {
	var span *Span
	// Should not panic
	span.AddMetadata("key", "value")
}

func TestSpan_AddMetadata_NilMap(t *testing.T) {
	span := &Span{
		Name:     "test",
		Metadata: nil,
	}
	// Should not panic
	span.AddMetadata("key", "value")
}

func TestSpan_SetParent_NilSpan(t *testing.T) {
	var span *Span
	// Should not panic
	span.SetParent("span-1")
}

func TestTracer_GetSpans_ReturnsACopy(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test")
	tracer.EndSpan(span)

	spans1 := tracer.GetSpans()
	spans2 := tracer.GetSpans()

	// Should be different slices
	if &spans1[0] == &spans2[0] {
		t.Error("GetSpans should return a copy, not the same slice")
	}

	// But point to the same span objects
	if spans1[0] != spans2[0] {
		t.Error("GetSpans should return copies pointing to the same span objects")
	}
}

func TestTracer_Clear_ResetsSequence(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span1 := tracer.StartSpan("op1")
	span2 := tracer.StartSpan("op2")
	tracer.EndSpan(span1)
	tracer.EndSpan(span2)

	if tracer.spanSeq != 2 {
		t.Errorf("Expected spanSeq=2, got %d", tracer.spanSeq)
	}

	tracer.Clear()

	if tracer.spanSeq != 0 {
		t.Errorf("Expected spanSeq=0 after Clear(), got %d", tracer.spanSeq)
	}

	// Verify next span starts from 1 again
	span3 := tracer.StartSpan("op3")
	if span3.SpanID != "span-1" {
		t.Errorf("Expected span-1 after clear, got %s", span3.SpanID)
	}
}

func TestTracer_ExportJSON_ValidStructure(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test-operation")
	span.AddMetadata("key", "value")
	time.Sleep(1 * time.Millisecond)
	tracer.EndSpan(span)

	var buf bytes.Buffer
	err := tracer.ExportJSON(&buf)
	if err != nil {
		t.Fatalf("ExportJSON failed: %v", err)
	}

	output := buf.String()

	// Check for valid JSON structure elements
	if !strings.Contains(output, `"name"`) {
		t.Error("JSON should contain 'name' field")
	}
	if !strings.Contains(output, `"start_time"`) {
		t.Error("JSON should contain 'start_time' field")
	}
	if !strings.Contains(output, `"end_time"`) {
		t.Error("JSON should contain 'end_time' field")
	}
	if !strings.Contains(output, `"duration"`) {
		t.Error("JSON should contain 'duration' field")
	}
	if !strings.Contains(output, `"metadata"`) {
		t.Error("JSON should contain 'metadata' field")
	}
}

func TestTracer_ExportJSON_EmptySpans(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	var buf bytes.Buffer
	err := tracer.ExportJSON(&buf)
	if err != nil {
		t.Fatalf("ExportJSON failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	// Should export an empty array
	if !strings.Contains(output, "[]") {
		t.Errorf("Empty spans should export as empty array, got: %s", output)
	}
}

func TestTracer_ExportTimeline_Empty(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	var buf bytes.Buffer
	err := tracer.ExportTimeline(&buf)
	if err != nil {
		t.Fatalf("ExportTimeline failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No spans recorded") {
		t.Error("Empty timeline should show 'No spans recorded'")
	}
}

func TestTracer_ExportTimeline_WithParentIndentation(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	parent := tracer.StartSpan("parent")
	time.Sleep(5 * time.Millisecond)

	child := tracer.StartSpan("child")
	child.SetParent(parent.SpanID)
	time.Sleep(5 * time.Millisecond)
	tracer.EndSpan(child)

	tracer.EndSpan(parent)

	var buf bytes.Buffer
	err := tracer.ExportTimeline(&buf)
	if err != nil {
		t.Fatalf("ExportTimeline failed: %v", err)
	}

	output := buf.String()
	lines := strings.Split(output, "\n")

	// Find the child span line - it should be indented
	foundIndentedChild := false
	for _, line := range lines {
		if strings.Contains(line, "child") {
			// Child spans should have extra spaces for indentation
			if strings.Contains(line, "  child") {
				foundIndentedChild = true
			}
		}
	}

	if !foundIndentedChild {
		t.Error("Child span should be indented in timeline output")
	}
}

func TestTracer_ExportTimeline_Statistics(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	// Create multiple spans with the same name
	for i := 0; i < 3; i++ {
		span := tracer.StartSpan("repeated-op")
		time.Sleep(5 * time.Millisecond)
		tracer.EndSpan(span)
	}

	var buf bytes.Buffer
	err := tracer.ExportTimeline(&buf)
	if err != nil {
		t.Fatalf("ExportTimeline failed: %v", err)
	}

	output := buf.String()

	// Should show statistics table with headers
	if !strings.Contains(output, "Operation") {
		t.Error("Timeline should contain 'Operation' header")
	}
	if !strings.Contains(output, "Count") {
		t.Error("Timeline should contain 'Count' header")
	}
	if !strings.Contains(output, "Min") {
		t.Error("Timeline should contain 'Min' header")
	}
	if !strings.Contains(output, "Max") {
		t.Error("Timeline should contain 'Max' header")
	}
	if !strings.Contains(output, "Avg") {
		t.Error("Timeline should contain 'Avg' header")
	}

	// Should show count of 3
	if !strings.Contains(output, "3") {
		t.Error("Timeline should show count of 3 for repeated operation")
	}
}

func TestTracer_ExportToFile_JSON(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test-operation")
	time.Sleep(1 * time.Millisecond)
	tracer.EndSpan(span)

	tmpDir := t.TempDir()
	filename := tmpDir + "/trace.json"

	err := tracer.ExportToFile(filename, "json")
	if err != nil {
		t.Fatalf("ExportToFile(json) failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Fatalf("File %s was not created", filename)
	}
}

func TestTracer_ExportToFile_Timeline(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test-operation")
	time.Sleep(1 * time.Millisecond)
	tracer.EndSpan(span)

	tmpDir := t.TempDir()
	filename := tmpDir + "/trace.txt"

	err := tracer.ExportToFile(filename, "timeline")
	if err != nil {
		t.Fatalf("ExportToFile(timeline) failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Fatalf("File %s was not created", filename)
	}
}

func TestTracer_ExportToFile_TxtFormat(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test-operation")
	time.Sleep(1 * time.Millisecond)
	tracer.EndSpan(span)

	tmpDir := t.TempDir()
	filename := tmpDir + "/trace.txt"

	err := tracer.ExportToFile(filename, "txt")
	if err != nil {
		t.Fatalf("ExportToFile(txt) failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Fatalf("File %s was not created", filename)
	}
}

func TestTracer_ExportToFile_UnsupportedFormat(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("test-operation")
	tracer.EndSpan(span)

	tmpDir := t.TempDir()
	filename := tmpDir + "/trace.xml"

	err := tracer.ExportToFile(filename, "xml")
	if err == nil {
		t.Error("ExportToFile should return error for unsupported format")
	}

	if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("Expected 'unsupported format' error, got: %v", err)
	}
}

func TestGlobalExportJSON(t *testing.T) {
	Clear()
	Enable()

	span := StartSpan("export-test")
	time.Sleep(1 * time.Millisecond)
	EndSpan(span)

	var buf bytes.Buffer
	err := ExportJSON(&buf)
	if err != nil {
		t.Fatalf("ExportJSON failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "export-test") {
		t.Error("Exported JSON should contain span name")
	}

	Clear()
}

func TestGlobalExportTimeline(t *testing.T) {
	Clear()
	Enable()

	span := StartSpan("timeline-test")
	time.Sleep(1 * time.Millisecond)
	EndSpan(span)

	var buf bytes.Buffer
	err := ExportTimeline(&buf)
	if err != nil {
		t.Fatalf("ExportTimeline failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "timeline-test") {
		t.Error("Timeline should contain span name")
	}

	Clear()
}

func TestGlobalExportToFile(t *testing.T) {
	Clear()
	Enable()

	span := StartSpan("file-test")
	time.Sleep(1 * time.Millisecond)
	EndSpan(span)

	tmpDir := t.TempDir()
	filename := tmpDir + "/global-trace.json"

	err := ExportToFile(filename, "json")
	if err != nil {
		t.Fatalf("ExportToFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Fatalf("File %s was not created", filename)
	}

	Clear()
}

func TestDefaultTracer_Initialization(t *testing.T) {
	// DefaultTracer should be initialized
	if DefaultTracer == nil {
		t.Fatal("DefaultTracer should not be nil")
	}

	if DefaultTracer.spans == nil {
		t.Error("DefaultTracer.spans should be initialized")
	}

	// Note: We can't test that DefaultTracer is disabled by default
	// because other tests may have already enabled it.
	// Just verify it's not nil and has initialized spans.
}

func TestSpan_DurationCalculation(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	span := tracer.StartSpan("duration-test")
	sleepTime := 50 * time.Millisecond
	time.Sleep(sleepTime)
	tracer.EndSpan(span)

	// Duration should be at least the sleep time
	if span.Duration < sleepTime {
		t.Errorf("Expected duration >= %v, got %v", sleepTime, span.Duration)
	}

	// Duration should equal EndTime - StartTime
	calculatedDuration := span.EndTime.Sub(span.StartTime)
	if span.Duration != calculatedDuration {
		t.Errorf("Duration mismatch: span.Duration=%v, calculated=%v",
			span.Duration, calculatedDuration)
	}
}

func TestTracer_GetSpans_Sorting(t *testing.T) {
	tracer := &Tracer{
		spans:   make([]*Span, 0),
		enabled: true,
	}

	// Create spans with delays
	span1 := tracer.StartSpan("first")
	time.Sleep(10 * time.Millisecond)

	span2 := tracer.StartSpan("second")
	time.Sleep(10 * time.Millisecond)

	span3 := tracer.StartSpan("third")

	// End in reverse order
	tracer.EndSpan(span3)
	tracer.EndSpan(span1)
	tracer.EndSpan(span2)

	spans := tracer.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("Expected 3 spans, got %d", len(spans))
	}

	// Verify sorting by start time
	if !spans[0].StartTime.Before(spans[1].StartTime) {
		t.Error("Spans should be sorted by start time")
	}
	if !spans[1].StartTime.Before(spans[2].StartTime) {
		t.Error("Spans should be sorted by start time")
	}

	// Verify order
	if spans[0].Name != "first" || spans[1].Name != "second" || spans[2].Name != "third" {
		t.Errorf("Expected [first, second, third], got [%s, %s, %s]",
			spans[0].Name, spans[1].Name, spans[2].Name)
	}
}
