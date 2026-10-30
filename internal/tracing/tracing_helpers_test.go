// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package tracing

// Clear removes all recorded spans.
func (t *Tracer) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.spans = make([]*Span, 0, 1000)
	t.spanSeq = 0
}

// Clear removes all spans from the default tracer.
func Clear() {
	DefaultTracer.Clear()
}
