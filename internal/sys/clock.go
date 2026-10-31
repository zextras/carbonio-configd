// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys

import (
	"context"
	"time"
)

// Timer is a one-shot timer.
type Timer interface {
	// C delivers the fire time once.
	C() <-chan time.Time
	// Stop prevents the timer from firing; it reports whether it was active.
	Stop() bool
}

// Ticker delivers ticks at a fixed interval.
type Ticker interface {
	// C delivers the ticks.
	C() <-chan time.Time
	// Stop turns the ticker off.
	Stop()
}

// Clock is the source of time and of waiting. Only time that drives
// behaviour goes through it (deadlines, polling, retry back-off, tickers);
// durations measured purely to be logged keep using the time package.
type Clock interface {
	Now() time.Time
	// After waits for d to elapse and then delivers the current time.
	After(d time.Duration) <-chan time.Time
	NewTimer(d time.Duration) Timer
	NewTicker(d time.Duration) Ticker
	// Sleep blocks for d, or until ctx is done, in which case it returns
	// ctx.Err().
	Sleep(ctx context.Context, d time.Duration) error
}

// RealClock is the system clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NewTimer implements Clock.
func (RealClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

// NewTicker implements Clock.
func (RealClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

// Sleep implements Clock.
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // callers want the bare context error
	case <-t.C:
		return nil
	}
}

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
