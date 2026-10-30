// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systest

import (
	"context"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// Epoch is the instant a fresh Clock starts at.
var Epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// Clock is a manual sys.Clock: time only moves when the test calls Advance,
// at which point due timers, tickers, sleepers and After channels fire in
// time order. Sleep and After never block the test on real time.
type Clock struct {
	rec *Recorder

	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
}

type waiter struct {
	at       time.Time
	interval time.Duration // >0 for a ticker
	ch       chan time.Time
	stopped  bool
}

// NewClock returns a Clock at Epoch.
func NewClock(rec *Recorder) *Clock { return &Clock{rec: rec, now: Epoch} }

// Now implements sys.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *Clock) add(d, interval time.Duration) *waiter {
	c.mu.Lock()
	defer c.mu.Unlock()

	w := &waiter{at: c.now.Add(d), interval: interval, ch: make(chan time.Time, 1)}
	c.waiters = append(c.waiters, w)

	return w
}

// After implements sys.Clock.
func (c *Clock) After(d time.Duration) <-chan time.Time { return c.add(d, 0).ch }

// NewTimer implements sys.Clock.
func (c *Clock) NewTimer(d time.Duration) sys.Timer { return &timer{c: c, w: c.add(d, 0)} }

// NewTicker implements sys.Clock.
func (c *Clock) NewTicker(d time.Duration) sys.Ticker {
	return &ticker{c: c, w: c.add(d, d)}
}

// Sleep implements sys.Clock: it returns once the test has advanced the clock
// by d, or with ctx.Err() when ctx is done first.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	w := c.add(d, 0)

	select {
	case <-ctx.Done():
		c.stop(w)

		return ctx.Err() //nolint:wrapcheck // bare context error
	case <-w.ch:
		return nil
	}
}

func (c *Clock) stop(w *waiter) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	was := !w.stopped

	w.stopped = true

	return was
}

// Waiters reports how many timers, tickers or sleepers are armed — a test can
// poll it to know the code under test is parked on the clock.
func (c *Clock) Waiters() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	n := 0

	for _, w := range c.waiters {
		if !w.stopped {
			n++
		}
	}

	return n
}

// Advance moves time forward by d, firing everything that comes due on the
// way in chronological order (a ticker fires once per elapsed interval; a tick
// is dropped when its channel is full, like time.Ticker).
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()

	for {
		c.mu.Lock()

		var next *waiter

		for _, w := range c.waiters {
			if !w.stopped && !w.at.After(target) && (next == nil || w.at.Before(next.at)) {
				next = w
			}
		}

		if next == nil {
			c.now = target
			c.mu.Unlock()

			return
		}

		if next.at.After(c.now) {
			c.now = next.at
		}

		fire := c.now

		if next.interval > 0 {
			next.at = next.at.Add(next.interval)
		} else {
			next.stopped = true
		}

		c.mu.Unlock()

		select {
		case next.ch <- fire:
		default:
		}
	}
}

type timer struct {
	c *Clock
	w *waiter
}

func (t *timer) C() <-chan time.Time { return t.w.ch }
func (t *timer) Stop() bool          { return t.c.stop(t.w) }

type ticker struct {
	c *Clock
	w *waiter
}

func (t *ticker) C() <-chan time.Time { return t.w.ch }
func (t *ticker) Stop()               { t.c.stop(t.w) }
