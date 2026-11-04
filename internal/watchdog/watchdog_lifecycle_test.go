// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package watchdog

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/state"
)

// overlapProbe is a service manager whose first status probe blocks until
// release is closed and which records how many probes ran at the same time.
type overlapProbe struct {
	*MockServiceManager

	entered  chan struct{}
	release  chan struct{}
	inflight atomic.Int32
	maxSeen  atomic.Int32
	calls    atomic.Int32
}

func (o *overlapProbe) IsRunning(ctx context.Context, service string) (bool, error) {
	n := o.inflight.Add(1)
	defer o.inflight.Add(-1)

	for {
		old := o.maxSeen.Load()
		if n <= old || o.maxSeen.CompareAndSwap(old, n) {
			break
		}
	}

	if o.calls.Add(1) == 1 {
		o.entered <- struct{}{}

		<-o.release
	}

	return o.MockServiceManager.IsRunning(ctx, service)
}

// TestWatchdog_RestartedLoopWaitsForTheOldOne pins that Start after a Stop
// that gave up waiting (its context was done while a cycle was still running)
// does not run a second loop next to the old one: no two check cycles overlap,
// and the new loop takes over once the old one is gone.
func TestWatchdog_RestartedLoopWaitsForTheOldOne(t *testing.T) {
	t.Parallel()

	probe := &overlapProbe{
		MockServiceManager: NewMockServiceManager(),
		entered:            make(chan struct{}, 1),
		release:            make(chan struct{}),
	}
	probe.setRunning("amavis", true)

	h := newHarness(t, Config{
		ServiceManager: probe,
		State:          state.NewState(),
		CheckInterval:  time.Second,
	})
	h.wd.AddService(context.Background(), "amavis")
	h.wd.SetServiceEnabled(context.Background(), "amavis", true)

	h.start()
	h.clock.Advance(h.wd.checkInterval)

	select {
	case <-probe.entered:
	case <-time.After(5 * time.Second):
		close(probe.release)
		t.Fatal("watchdog never probed the service")
	}

	// The old loop is stuck in its cycle: Stop gives up, Start replaces it.
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	h.wd.Stop(stopCtx)
	h.wd.Start(context.Background())

	// The new loop must not arm its ticker while the old loop is alive; give a
	// loop that ignores this a chance to show it.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) && h.clock.Waiters() < 2 {
		time.Sleep(time.Millisecond)
	}

	h.clock.Advance(h.wd.checkInterval)

	for deadline = time.Now().Add(100 * time.Millisecond); time.Now().Before(deadline) && probe.calls.Load() < 2; {
		time.Sleep(time.Millisecond)
	}

	close(probe.release)

	// Once the old loop is gone the new one runs cycles of its own: keep
	// ticking until it has armed its ticker and probed.
	deadline = time.Now().Add(5 * time.Second)
	for probe.calls.Load() < 2 && time.Now().Before(deadline) {
		h.clock.Advance(h.wd.checkInterval)
		time.Sleep(time.Millisecond)
	}

	if got := probe.calls.Load(); got < 2 {
		t.Fatalf("the new loop never ran a cycle, probes = %d", got)
	}

	h.stop()

	if got := probe.maxSeen.Load(); got != 1 {
		t.Errorf("check cycles overlapped: %d probes ran at once", got)
	}
}

// TestCheckServices_WatchesEveryConfiguredService pins that the monitored
// list is the configured one (zmconfigd_watchdog_services), as in legacy
// configd's watchdog(): a service the old built-in list did not know is
// restarted when it is configured, tracked and down.
func TestCheckServices_WatchesEveryConfiguredService(t *testing.T) {
	t.Parallel()

	mockSM := NewMockServiceManager()
	wd := newEnabledWatchdog(t, mockSM, "milter")
	wd.UpdateServiceList(context.Background(), []string{"milter"})
	wd.AddService(context.Background(), "milter")
	mockSM.setRunning("milter", false)

	wd.checkServices(context.Background())

	if n := mockSM.getRestartCount("milter"); n != 1 {
		t.Fatalf("a configured, tracked, down service must be restarted once, got %d", n)
	}
}

// TestCheckServices_IgnoresUnconfiguredServices pins the other side: a
// tracked service that is not in the configured list is left alone.
func TestCheckServices_IgnoresUnconfiguredServices(t *testing.T) {
	t.Parallel()

	mockSM := NewMockServiceManager()
	wd := newEnabledWatchdog(t, mockSM, "ldap")
	wd.AddService(context.Background(), "ldap")
	mockSM.setRunning("ldap", false)

	wd.checkServices(context.Background())

	if n := mockSM.getRestartCount("ldap") + mockSM.getProbeCount("ldap"); n != 0 {
		t.Fatalf("an unconfigured service must not be probed or restarted, got %d calls", n)
	}
}
