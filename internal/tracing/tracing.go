// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

// Package tracing provides lightweight execution tracing for performance analysis.
// This is a simple span-based tracing system designed for <5% overhead.
// This file is only compiled when the 'tracing' build tag is specified.
package tracing

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// Span represents a single traced operation.
type Span struct {
	Name      string            `json:"name"`
	StartTime time.Time         `json:"start_time"`
	EndTime   time.Time         `json:"end_time"`
	Duration  time.Duration     `json:"duration"`
	ParentID  string            `json:"parent_id,omitempty"`
	SpanID    string            `json:"span_id"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Tracer manages span collection and export.
type Tracer struct {
	mu       sync.RWMutex
	spans    []*Span
	enabled  bool
	spanSeq  int
	disabled bool // Master disable flag
}

var (
	// DefaultTracer is the global tracer instance
	DefaultTracer = &Tracer{
		spans:   make([]*Span, 0, 1000),
		enabled: false,
	}
)

// Enable enables tracing.
func (t *Tracer) Enable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = true
}

// Disable disables tracing.
func (t *Tracer) Disable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
}

// IsEnabled returns true if tracing is enabled.
func (t *Tracer) IsEnabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.enabled && !t.disabled
}

// StartSpan creates and starts a new span.
func (t *Tracer) StartSpan(name string) *Span {
	if !t.IsEnabled() {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.spanSeq++
	span := &Span{
		Name:      name,
		StartTime: time.Now(),
		SpanID:    fmt.Sprintf("span-%d", t.spanSeq),
		Metadata:  make(map[string]string),
	}

	return span
}

// EndSpan marks a span as complete and records it.
func (t *Tracer) EndSpan(span *Span) {
	if span == nil || !t.IsEnabled() {
		return
	}

	span.EndTime = time.Now()
	span.Duration = span.EndTime.Sub(span.StartTime)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.spans = append(t.spans, span)
}

// AddMetadata adds metadata to a span.
func (span *Span) AddMetadata(key, value string) {
	if span != nil && span.Metadata != nil {
		span.Metadata[key] = value
	}
}

// SetParent sets the parent span ID.
func (span *Span) SetParent(parentID string) {
	if span != nil {
		span.ParentID = parentID
	}
}

// GetSpans returns all recorded spans (sorted by start time).
func (t *Tracer) GetSpans() []*Span {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Create a copy and sort by start time
	spans := make([]*Span, len(t.spans))
	copy(spans, t.spans)

	sort.Slice(spans, func(i, j int) bool {
		return spans[i].StartTime.Before(spans[j].StartTime)
	})

	return spans
}

// Clear removes all recorded spans.
func (t *Tracer) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.spans = make([]*Span, 0, 1000)
	t.spanSeq = 0
}

// ExportJSON exports spans to JSON format.
func (t *Tracer) ExportJSON(w io.Writer) error {
	spans := t.GetSpans()
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(spans)
}

// ExportTimeline exports spans as a human-readable timeline.
func (t *Tracer) ExportTimeline(w io.Writer) error {
	spans := t.GetSpans()

	if len(spans) == 0 {
		fmt.Fprintln(w, "No spans recorded")
		return nil
	}

	// Find the earliest start time as reference
	refTime := spans[0].StartTime

	fmt.Fprintln(w, "Execution Timeline")
	fmt.Fprintln(w, "==================")
	fmt.Fprintln(w)

	for _, span := range spans {
		offset := span.StartTime.Sub(refTime)
		indent := ""
		if span.ParentID != "" {
			indent = "  "
		}

		fmt.Fprintf(w, "[+%8.3fms] %s%-40s  %8.3fms",
			float64(offset.Microseconds())/1000,
			indent,
			span.Name,
			float64(span.Duration.Microseconds())/1000,
		)

		if len(span.Metadata) > 0 {
			fmt.Fprintf(w, "  %v", span.Metadata)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Summary")
	fmt.Fprintln(w, "-------")

	// Calculate total time
	lastSpan := spans[len(spans)-1]
	totalTime := lastSpan.EndTime.Sub(refTime)
	fmt.Fprintf(w, "Total time: %.3fms\n", float64(totalTime.Microseconds())/1000)

	// Group by operation name and calculate stats
	stats := make(map[string]struct {
		count    int
		totalDur time.Duration
		minDur   time.Duration
		maxDur   time.Duration
	})

	for _, span := range spans {
		s := stats[span.Name]
		s.count++
		s.totalDur += span.Duration
		if s.minDur == 0 || span.Duration < s.minDur {
			s.minDur = span.Duration
		}
		if span.Duration > s.maxDur {
			s.maxDur = span.Duration
		}
		stats[span.Name] = s
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-40s  %6s  %10s  %10s  %10s  %10s\n",
		"Operation", "Count", "Total", "Avg", "Min", "Max")
	fmt.Fprintln(w, "--------------------------------------------------------------------------------")

	// Sort by total duration (descending)
	type statEntry struct {
		name string
		stat struct {
			count    int
			totalDur time.Duration
			minDur   time.Duration
			maxDur   time.Duration
		}
	}
	entries := make([]statEntry, 0, len(stats))
	for name, stat := range stats {
		entries = append(entries, statEntry{name: name, stat: stat})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stat.totalDur > entries[j].stat.totalDur
	})

	for _, entry := range entries {
		avgDur := entry.stat.totalDur / time.Duration(entry.stat.count)
		fmt.Fprintf(w, "%-40s  %6d  %8.3fms  %8.3fms  %8.3fms  %8.3fms\n",
			entry.name,
			entry.stat.count,
			float64(entry.stat.totalDur.Microseconds())/1000,
			float64(avgDur.Microseconds())/1000,
			float64(entry.stat.minDur.Microseconds())/1000,
			float64(entry.stat.maxDur.Microseconds())/1000,
		)
	}

	return nil
}

// ExportToFile exports spans to a file in the specified format.
func (t *Tracer) ExportToFile(filename string, format string) error {
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create trace file: %w", err)
	}
	defer f.Close()

	switch format {
	case "json":
		return t.ExportJSON(f)
	case "timeline", "txt":
		return t.ExportTimeline(f)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

// Global convenience functions

// Enable enables the default tracer.
func Enable() {
	DefaultTracer.Enable()
}

// Disable disables the default tracer.
func Disable() {
	DefaultTracer.Disable()
}

// IsEnabled returns true if the default tracer is enabled.
func IsEnabled() bool {
	return DefaultTracer.IsEnabled()
}

// StartSpan starts a new span using the default tracer.
func StartSpan(name string) *Span {
	return DefaultTracer.StartSpan(name)
}

// EndSpan ends a span using the default tracer.
func EndSpan(span *Span) {
	DefaultTracer.EndSpan(span)
}

// GetSpans returns all spans from the default tracer.
func GetSpans() []*Span {
	return DefaultTracer.GetSpans()
}

// Clear clears all spans from the default tracer.
func Clear() {
	DefaultTracer.Clear()
}

// ExportJSON exports spans to JSON using the default tracer.
func ExportJSON(w io.Writer) error {
	return DefaultTracer.ExportJSON(w)
}

// ExportTimeline exports spans as timeline using the default tracer.
func ExportTimeline(w io.Writer) error {
	return DefaultTracer.ExportTimeline(w)
}

// ExportToFile exports spans to file using the default tracer.
func ExportToFile(filename string, format string) error {
	return DefaultTracer.ExportToFile(filename, format)
}
