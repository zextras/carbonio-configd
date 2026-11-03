// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package watchdog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/state"
)

// TestCheckServices_FailedRestartIsRetriedNextTick: a restart that failed
// leaves the service watched, so the next tick tries again (GCR-08).
func TestCheckServices_FailedRestartIsRetriedNextTick(t *testing.T) {
	t.Parallel()

	mockSM := NewMockServiceManager()
	mockSM.processRestartsFunc = func(func(string) string) error { return errors.New("start failed") }

	wd := newEnabledWatchdog(t, mockSM, "antivirus")
	wd.AddService(context.Background(), "antivirus")
	mockSM.setRunning("antivirus", false)

	wd.checkServices(context.Background())

	if !wd.IsServiceTracked("antivirus") {
		t.Fatal("service must stay watched after a failed restart")
	}

	// The service manager recovers: the next tick restarts it.
	mockSM.mu.Lock()
	mockSM.processRestartsFunc = nil
	mockSM.mu.Unlock()

	wd.checkServices(context.Background())

	if n := mockSM.getProbeCount("antivirus"); n != 2 {
		t.Errorf("want one probe per tick (2), got %d", n)
	}

	if !mockSM.isRunning("antivirus") || !wd.IsServiceTracked("antivirus") {
		t.Error("second tick must restart the service and keep it watched")
	}
}

// TestCheckServices_UntrackedRunningServiceIsRearmed: a service untracked by a
// config-driven restart is watched again as soon as a status check sees it
// running (legacy configd.py), and only then eligible for restarts.
func TestCheckServices_UntrackedRunningServiceIsRearmed(t *testing.T) {
	t.Parallel()

	mockSM := NewMockServiceManager()
	wd := newEnabledWatchdog(t, mockSM, "antivirus")
	wd.AddService(context.Background(), "antivirus")

	// A config restart is under way: the service manager untracked it and the
	// process is down. The watchdog must not queue a second restart.
	wd.RemoveService(context.Background(), "antivirus")
	mockSM.setRunning("antivirus", false)

	wd.checkServices(context.Background())

	if n := mockSM.getRestartCount("antivirus"); n != 0 {
		t.Fatalf("untracked service must not be restarted by the watchdog, got %d restarts", n)
	}

	if wd.IsServiceTracked("antivirus") {
		t.Fatal("a service seen down must stay untracked")
	}

	// The config restart finished: the next tick sees it running and re-arms.
	mockSM.setRunning("antivirus", true)

	wd.checkServices(context.Background())

	if !wd.IsServiceTracked("antivirus") {
		t.Fatal("a service seen running must be watched again")
	}

	// And a later crash is now handled.
	mockSM.setRunning("antivirus", false)

	wd.checkServices(context.Background())

	if n := mockSM.getRestartCount("antivirus"); n != 1 {
		t.Errorf("re-armed service must be restarted after a crash, got %d restarts", n)
	}
}

// TestSystemdMode_WatchdogIsANoOp: on a systemd host the units' Restart=
// recovers crashes, so the watchdog never runs nor restarts anything.
func TestSystemdMode_WatchdogIsANoOp(t *testing.T) {
	t.Parallel()

	mockSM := NewMockServiceManager()
	h := newHarness(t, Config{
		ServiceManager: mockSM,
		State:          state.NewState(),
		CheckInterval:  50 * time.Millisecond,
		SystemdMode:    true,
	})
	h.wd.AddService(context.Background(), "antivirus")
	h.wd.SetServiceEnabled(context.Background(), "antivirus", true)
	mockSM.setRunning("antivirus", false)

	h.wd.Start(context.Background())

	if h.wd.IsEnabled() {
		t.Fatal("systemd-mode watchdog must not start its loop")
	}

	if h.clock.Waiters() != 0 {
		t.Fatal("systemd-mode watchdog must not arm a ticker")
	}

	h.clock.Advance(time.Hour)
	h.wd.Stop(context.Background()) // must be a silent no-op

	if n := mockSM.getRestartCount("antivirus") + mockSM.getProbeCount("antivirus"); n != 0 {
		t.Errorf("systemd-mode watchdog must not probe or restart, got %d calls", n)
	}
}
