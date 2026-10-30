// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

// Package tracing provides no-op stubs when tracing is not enabled.
// This file is compiled by default (without build tags) for zero overhead.
package tracing

import "time"

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

// AddMetadata does nothing (no-op).
func (span *Span) AddMetadata(key, value string) { // no-op: tracing disabled at build time
}

// StartSpan returns nil (no-op).
func StartSpan(name string) *Span {
	return nil
}

// EndSpan does nothing (no-op).
func EndSpan(span *Span) { // no-op: tracing disabled at build time
}
