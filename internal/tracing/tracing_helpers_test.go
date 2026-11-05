// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package tracing

import (
	"sync"
	"time"
)

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

// stepClockInterval is how far stepClock advances on every reading.
const stepClockInterval = time.Millisecond

// stepClock is a deterministic clock: each reading is stepClockInterval after
// the previous one, so tests need no sleeps to get ordered, non-zero durations.
type stepClock struct {
	mu  sync.Mutex
	cur time.Time
}

func newStepClock() *stepClock {
	return &stepClock{cur: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cur = c.cur.Add(stepClockInterval)

	return c.cur
}
