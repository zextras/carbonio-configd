// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

// Package tracing provides noop tests compiled without the 'tracing' build tag.
package tracing

import (
	"context"
	"testing"
)

func TestNoop_Span_AddMetadata_NilReceiver(t *testing.T) {
	var span *Span
	// noop AddMetadata does not dereference receiver — should not panic
	span.AddMetadata("key", "value")
}

func TestNoop_Global_StartSpanContext(t *testing.T) {
	ctx := context.Background()

	got, span := StartSpanContext(ctx, "op")
	if span != nil {
		t.Error("noop global StartSpanContext() should return a nil span")
	}

	if got != ctx {
		t.Error("noop global StartSpanContext() should return ctx unchanged")
	}
}

func TestNoop_Global_EndSpan(t *testing.T) {
	// Should not panic with nil span
	EndSpan(nil)
}
