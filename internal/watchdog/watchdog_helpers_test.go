// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package watchdog

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// IsEnabled returns whether the watchdog is currently active.
func (w *Watchdog) IsEnabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.enabled
}

// SetServiceEnabled sets whether a specific service should be monitored by watchdog.
func (w *Watchdog) SetServiceEnabled(ctx context.Context, service string, enabled bool) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.mu.Lock()
	defer w.mu.Unlock()

	w.ServiceEnabled[service] = enabled
	logger.DebugContext(ctx, "Watchdog monitoring status changed",
		"service", service,
		"enabled", enabled)
}

// harness drives a Watchdog on a manual clock. One cycle is one tick of the
// fake clock followed by a barrier until the run loop has finished the check
// it triggered, so a test asserts on a settled state without sleeping.
type harness struct {
	t       *testing.T
	wd      *Watchdog
	clock   *systest.Clock
	started chan struct{}
}

// newHarness builds a watchdog whose ticker is the harness' fake clock and
// whose per-tick snapshot hook announces every cycle start.
func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()

	fakes := systest.New()
	h := &harness{t: t, clock: fakes.Clock, started: make(chan struct{}, 64)}
	cfg.Clock = fakes.Clock
	cfg.Snapshots = h
	h.wd = NewWatchdog(cfg)

	return h
}

// WithProcessSnapshot implements SnapshotProvider: checkServices calls it
// once per cycle after deciding to run, which marks the cycle start.
func (h *harness) WithProcessSnapshot(ctx context.Context) context.Context {
	select {
	case h.started <- struct{}{}:
	default:
	}

	return ctx
}

// start starts the watchdog and waits until its ticker is armed on the clock.
func (h *harness) start() {
	h.t.Helper()

	h.wd.Start(context.Background())
	h.awaitArmed()
}

// stop stops the watchdog, waiting for an in-flight cycle to finish.
func (h *harness) stop() {
	h.wd.Stop(context.Background())
}

func (h *harness) awaitArmed() {
	h.t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for h.clock.Waiters() == 0 {
		if time.Now().After(deadline) {
			h.t.Fatal("watchdog never armed its ticker on the clock")
		}

		runtime.Gosched()
	}
}

// cycle fires one check tick and returns once the resulting check completed.
// Stop only returns after the run loop has left its current cycle, so the
// stop/start pair is the end-of-cycle barrier.
func (h *harness) cycle() {
	h.t.Helper()

	h.clock.Advance(h.wd.CheckInterval)

	select {
	case <-h.started:
	case <-time.After(5 * time.Second):
		h.t.Fatal("watchdog did not run a check cycle after the tick")
	}

	h.stop()
	h.start()
}
