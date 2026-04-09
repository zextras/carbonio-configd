// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

// Package tracing provides no-op stubs when tracing is not enabled.
// This file is compiled by default (without build tags) for zero overhead.
package tracing

import (
	"io"
	"time"
)

// Span represents a single traced operation (no-op).
type Span struct {
	Name      string
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	ParentID  string
	SpanID    string
	Metadata  map[string]string
}

// Tracer manages span collection (no-op).
type Tracer struct{}

var (
	// DefaultTracer is the global tracer instance (no-op)
	DefaultTracer = &Tracer{}
)

// Enable does nothing (no-op).
func (t *Tracer) Enable() { // no-op: tracing disabled at build time
}

// Disable does nothing (no-op).
func (t *Tracer) Disable() { // no-op: tracing disabled at build time
}

// IsEnabled always returns false (no-op).
func (t *Tracer) IsEnabled() bool {
	return false
}

// StartSpan returns nil (no-op).
func (t *Tracer) StartSpan(name string) *Span {
	return nil
}

// EndSpan does nothing (no-op).
func (t *Tracer) EndSpan(span *Span) { // no-op: tracing disabled at build time
}

// AddMetadata does nothing (no-op).
func (span *Span) AddMetadata(key, value string) { // no-op: tracing disabled at build time
}

// SetParent does nothing (no-op).
func (span *Span) SetParent(parentID string) { // no-op: tracing disabled at build time
}

// GetSpans returns empty slice (no-op).
func (t *Tracer) GetSpans() []*Span {
	return nil
}

// Clear does nothing (no-op).
func (t *Tracer) Clear() { // no-op: tracing disabled at build time
}

// ExportJSON does nothing (no-op).
func (t *Tracer) ExportJSON(w io.Writer) error {
	return nil
}

// ExportTimeline does nothing (no-op).
func (t *Tracer) ExportTimeline(w io.Writer) error {
	return nil
}

// ExportToFile does nothing (no-op).
func (t *Tracer) ExportToFile(filename string, format string) error {
	return nil
}

// Global convenience functions (all no-op)

// Enable does nothing (no-op).
func Enable() { // no-op: tracing disabled at build time
}

// Disable does nothing (no-op).
func Disable() { // no-op: tracing disabled at build time
}

// IsEnabled always returns false (no-op).
func IsEnabled() bool {
	return false
}

// StartSpan returns nil (no-op).
func StartSpan(name string) *Span {
	return nil
}

// EndSpan does nothing (no-op).
func EndSpan(span *Span) { // no-op: tracing disabled at build time
}

// GetSpans returns empty slice (no-op).
func GetSpans() []*Span {
	return nil
}

// Clear does nothing (no-op).
func Clear() { // no-op: tracing disabled at build time
}

// ExportJSON does nothing (no-op).
func ExportJSON(w io.Writer) error {
	return nil
}

// ExportTimeline does nothing (no-op).
func ExportTimeline(w io.Writer) error {
	return nil
}

// ExportToFile does nothing (no-op).
func ExportToFile(filename string, format string) error {
	return nil
}
