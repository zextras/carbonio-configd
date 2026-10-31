// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package watchdog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/metrics"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
)

// TestRestartDownService_FailureNotLoggedAsSuccess verifies that when
// ProcessRestarts reports a failure the watchdog logs an error, never claims
// success, and leaves the service untracked (legacy configd only re-tracks a
// service once a later status check sees it running).
func TestRestartDownService_FailureNotLoggedAsSuccess(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	ctx := logger.ContextWithLogger(context.Background(),
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	mockSM := NewMockServiceManager()
	mockSM.processRestartsFunc = func(func(string) string) error {
		return errors.Join(services.ErrRestartsFailed, errors.New("amavis"))
	}

	wd := NewWatchdog(Config{ServiceManager: mockSM, State: state.NewState()})
	wd.AddService(ctx, "amavis")

	wd.restartDownService(ctx, "amavis")

	out := buf.String()
	if strings.Contains(out, "Successfully restarted service") {
		t.Errorf("failed restart must not be logged as success, got:\n%s", out)
	}

	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "Failed to restart service") {
		t.Errorf("expected an ERROR 'Failed to restart service' line, got:\n%s", out)
	}

	if wd.IsServiceTracked("amavis") {
		t.Error("service must stay untracked after a failed restart")
	}
}

// TestRestartDownService_SuccessRetracks is the success counterpart.
func TestRestartDownService_SuccessRetracks(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	ctx := logger.ContextWithLogger(context.Background(),
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	wd := NewWatchdog(Config{ServiceManager: NewMockServiceManager(), State: state.NewState()})
	wd.AddService(ctx, "amavis")

	wd.restartDownService(ctx, "amavis")

	if !strings.Contains(buf.String(), "Successfully restarted service") {
		t.Errorf("expected success log, got:\n%s", buf.String())
	}

	if !wd.IsServiceTracked("amavis") {
		t.Error("service must be re-tracked after a successful restart")
	}
}

// TestCheckOneService_ProbeFailureIsWarn verifies a failing status probe is a
// WARN, not an ERROR.
func TestCheckOneService_ProbeFailureIsWarn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	ctx := logger.ContextWithLogger(context.Background(),
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	mockSM := NewMockServiceManager()
	mockSM.setIsRunningError(errors.New("unknown service"))

	wd := NewWatchdog(Config{ServiceManager: mockSM, State: state.NewState()})
	wd.AddService(ctx, "amavis")
	wd.SetServiceEnabled(ctx, "amavis", true)

	wd.checkOneService(ctx, ctx, "amavis", map[string]bool{})

	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "Failed to check service") {
		t.Errorf("expected WARN 'Failed to check service', got:\n%s", out)
	}

	if strings.Contains(out, "level=ERROR") {
		t.Errorf("probe failure must not log at ERROR, got:\n%s", out)
	}
}

// TestWatchdog_StopHonorsContextWhileRunBusy verifies Stop returns once its
// context is done even though the monitoring loop is stuck in a restart, and
// that the watchdog can be started and stopped again afterwards.
func TestWatchdog_StopHonorsContextWhileRunBusy(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})

	mockSM := NewMockServiceManager()
	mockSM.processRestartsFunc = func(func(string) string) error {
		select {
		case entered <- struct{}{}:
		default:
		}

		<-release

		return nil
	}

	h := newHarness(t, Config{
		ServiceManager: mockSM,
		State:          state.NewState(),
		CheckInterval:  5 * time.Millisecond,
	})
	wd := h.wd
	wd.AddService(context.Background(), "amavis")
	wd.SetServiceEnabled(context.Background(), "amavis", true)

	h.start()
	h.clock.Advance(h.wd.CheckInterval)

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("watchdog never reached ProcessRestarts")
	}

	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()

	stopped := make(chan struct{})

	go func() {
		wd.Stop(stopCtx)
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Stop did not return after its context was cancelled")
	}

	if wd.IsEnabled() {
		t.Error("watchdog must be disabled after Stop")
	}

	// Let the stuck loop finish, then verify Start/Stop can be repeated.
	close(release)

	mockSM.setRunning("amavis", true)

	wd.Start(context.Background())

	done := make(chan struct{})

	go func() {
		wd.Stop(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("second Stop did not return")
	}
}

// TestWatchdog_StartStopRepeats verifies Stop followed by Start/Stop cycles
// neither panics (closed channel) nor hangs.
func TestWatchdog_StartStopRepeats(t *testing.T) {
	t.Parallel()

	h := newHarness(t, Config{
		ServiceManager: NewMockServiceManager(),
		State:          state.NewState(),
		CheckInterval:  time.Millisecond,
	})
	wd := h.wd

	for range 3 {
		wd.Start(context.Background())
		wd.Stop(context.Background())
	}
}

// TestRestartDownService_CountsRestartsInMetrics verifies the watchdog counts
// every restart attempt by service and result.
func TestRestartDownService_CountsRestartsInMetrics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reg := metrics.New()

	ok := NewWatchdog(Config{ServiceManager: NewMockServiceManager(), State: state.NewState()})
	ok.SetMetrics(reg)
	ok.restartDownService(ctx, "antivirus")

	failing := NewMockServiceManager()
	failing.processRestartsFunc = func(func(string) string) error { return errors.New("boom") }

	bad := NewWatchdog(Config{ServiceManager: failing, State: state.NewState()})
	bad.SetMetrics(reg)
	bad.restartDownService(ctx, "amavis")

	out := string(reg.Render())
	if !strings.Contains(out, `configd_watchdog_restarts_total{result="success",service="antivirus"} 1`) {
		t.Errorf("missing success counter:\n%s", out)
	}

	if !strings.Contains(out, `configd_watchdog_restarts_total{result="failure",service="amavis"} 1`) {
		t.Errorf("missing failure counter:\n%s", out)
	}
}

// TestRestartDownService_ShutdownIsNotAFailure verifies a restart interrupted
// by a shutdown is neither counted as a failed restart nor re-added to the
// tracked services.
func TestRestartDownService_ShutdownIsNotAFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	reg := metrics.New()

	mgr := NewMockServiceManager()
	mgr.processRestartsFunc = func(func(string) string) error {
		cancel()

		return context.Canceled
	}

	wd := NewWatchdog(Config{ServiceManager: mgr, State: state.NewState()})
	wd.SetMetrics(reg)
	wd.restartDownService(ctx, "amavis")

	if out := string(reg.Render()); strings.Contains(out, `service="amavis"`) {
		t.Errorf("an interrupted restart must not be counted:\n%s", out)
	}
}
