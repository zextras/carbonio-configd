// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/state"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestSetupSignalHandler_SIGTERM(t *testing.T) {
	appState := &state.State{}
	ctx, cancel := context.WithCancel(context.Background())
	reloadChan := make(chan struct{}, 1)

	// Setup signal handler
	SetupSignalHandler(ctx, appState, cancel, reloadChan, nil)

	// Give handler time to start
	time.Sleep(50 * time.Millisecond)

	// Send SIGTERM to ourselves
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = p.Signal(syscall.SIGTERM)
	if err != nil {
		t.Fatalf("Failed to send SIGTERM: %v", err)
	}

	// Wait for context to be canceled
	select {
	case <-ctx.Done():
		// Success - context was canceled
	case <-time.After(1 * time.Second):
		t.Error("Context was not canceled after SIGTERM")
	}
}

func TestSetupSignalHandler_SIGHUP(t *testing.T) {
	appState := &state.State{
		SleepTimer: 100, // Set initial sleep timer
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)

	SetupSignalHandler(ctx, appState, cancel, reloadChan, nil)

	time.Sleep(50 * time.Millisecond)

	// Send SIGCHLD
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = p.Signal(syscall.SIGHUP)
	if err != nil {
		t.Fatalf("Failed to send SIGHUP: %v", err)
	}

	// Check that reload signal was sent
	select {
	case <-reloadChan:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("Reload signal not received after SIGHUP")
	}

	// Check that sleep timer was reset
	if appState.GetSleepTimer() != 0 {
		t.Errorf("Expected SleepTimer to be 0, got %f", appState.GetSleepTimer())
	}
}

func TestSetupSignalHandler_SIGUSR2(t *testing.T) {
	appState := &state.State{
		SleepTimer: 100,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)

	SetupSignalHandler(ctx, appState, cancel, reloadChan, nil)

	time.Sleep(50 * time.Millisecond)

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = p.Signal(syscall.SIGUSR2)
	if err != nil {
		t.Fatalf("Failed to send SIGUSR2: %v", err)
	}

	select {
	case <-reloadChan:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("Reload signal not received after SIGUSR2")
	}

	if appState.GetSleepTimer() != 0 {
		t.Errorf("Expected SleepTimer to be 0, got %f", appState.GetSleepTimer())
	}
}

func TestSetupSignalHandler_SIGALRM(t *testing.T) {
	appState := &state.State{
		SleepTimer: 100,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)

	SetupSignalHandler(ctx, appState, cancel, reloadChan, nil)

	time.Sleep(50 * time.Millisecond)

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = p.Signal(syscall.SIGALRM)
	if err != nil {
		t.Fatalf("Failed to send SIGALRM: %v", err)
	}

	select {
	case <-reloadChan:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("Reload signal not received after SIGALRM")
	}

	if appState.GetSleepTimer() != 0 {
		t.Errorf("Expected SleepTimer to be 0, got %f", appState.GetSleepTimer())
	}
}

func TestSetupSignalHandler_SIGCHLD(t *testing.T) {
	appState := &state.State{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)

	SetupSignalHandler(ctx, appState, cancel, reloadChan, nil)

	time.Sleep(50 * time.Millisecond)

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = p.Signal(syscall.SIGCHLD)
	if err != nil {
		t.Fatalf("Failed to send SIGCHLD: %v", err)
	}

	// Give handler time to process
	time.Sleep(100 * time.Millisecond)

	// SIGCHLD should NOT trigger reload
	select {
	case <-reloadChan:
		t.Error("SIGCHLD should not trigger reload")
	default:
		// Success - no reload signal
	}

	// Context should still be active
	select {
	case <-ctx.Done():
		t.Error("Context should not be canceled after SIGCHLD")
	default:
		// Success
	}
}

func TestSetupSignalHandler_ReloadChannelBlocked(t *testing.T) {
	appState := &state.State{
		SleepTimer: 100,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create channel with capacity 1 and fill it
	reloadChan := make(chan struct{}, 1)
	reloadChan <- struct{}{} // Block the channel

	SetupSignalHandler(ctx, appState, cancel, reloadChan, nil)

	time.Sleep(50 * time.Millisecond)

	// Send SIGHUP when channel is blocked
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = p.Signal(syscall.SIGHUP)
	if err != nil {
		t.Fatalf("Failed to send SIGHUP: %v", err)
	}

	// Handler should not block - it uses non-blocking send
	time.Sleep(100 * time.Millisecond)

	// Sleep timer should still be reset
	if appState.GetSleepTimer() != 0 {
		t.Errorf("Expected SleepTimer to be 0 even with blocked channel, got %f", appState.GetSleepTimer())
	}

	// Drain the channel
	<-reloadChan

	// Original signal should still be in channel (was already there)
	// The new signal was dropped due to non-blocking send
}

// TestSleepWithContext tests are already in shutdown_test.go
// We add a few more edge cases here

func TestSleepWithContext_ZeroDuration(t *testing.T) {
	ctx := context.Background()
	reloadChan := make(chan struct{})

	start := time.Now()
	interrupted := SleepWithContext(ctx, 0, reloadChan)
	elapsed := time.Since(start)

	if interrupted {
		t.Error("Zero duration sleep should not be interrupted")
	}

	if elapsed > 100*time.Millisecond {
		t.Errorf("Zero duration sleep took too long: %v", elapsed)
	}
}

func TestSleepWithContext_ImmediateCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reloadChan := make(chan struct{})

	// Cancel immediately
	cancel()

	start := time.Now()
	interrupted := SleepWithContext(ctx, 5*time.Second, reloadChan)
	elapsed := time.Since(start)

	if !interrupted {
		t.Error("Expected immediate interruption when context already canceled")
	}

	if elapsed > 100*time.Millisecond {
		t.Errorf("Should return immediately when context already canceled, took %v", elapsed)
	}
}

func TestSleepWithContext_ImmediateReload(t *testing.T) {
	ctx := context.Background()
	reloadChan := make(chan struct{}, 1)

	// Send reload signal before sleep
	reloadChan <- struct{}{}

	start := time.Now()
	interrupted := SleepWithContext(ctx, 5*time.Second, reloadChan)
	elapsed := time.Since(start)

	if !interrupted {
		t.Error("Expected immediate interruption when reload signal already sent")
	}

	if elapsed > 100*time.Millisecond {
		t.Errorf("Should return immediately when reload signal ready, took %v", elapsed)
	}
}

func TestSleepWithContext_ContextCancelDuringReload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reloadChan := make(chan struct{}, 1)

	// Both signals sent
	reloadChan <- struct{}{}
	cancel()

	interrupted := SleepWithContext(ctx, 5*time.Second, reloadChan)

	if !interrupted {
		t.Error("Expected interruption when both context and reload are triggered")
	}

	// Either signal is acceptable, but should be interrupted
}

// TestDispatchSignalSafe_ShutdownPanicStillCancels: when handling SIGTERM panics
// (here: a zero-value notifier), the daemon context must still be cancelled,
// otherwise the consumed SIGTERM leaves the daemon unstoppable.
func TestDispatchSignalSafe_ShutdownPanicStillCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)
	stop := dispatchSignalSafe(ctx, syscall.SIGTERM, state.NewState(), cancel, reloadChan, &sdnotify.Notifier{})

	if !stop {
		t.Error("dispatchSignalSafe() = false, want true after a panic on a shutdown signal")
	}

	if ctx.Err() == nil {
		t.Error("context not cancelled after a panic while handling SIGTERM")
	}
}

// TestDispatchSignalSafe_ReloadPanicKeepsHandlerAlive: a panic on a reload signal
// is absorbed and the handler keeps running.
func TestDispatchSignalSafe_ReloadPanicKeepsHandlerAlive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A nil state panics in SetSleepTimer.
	stop := dispatchSignalSafe(ctx, syscall.SIGHUP, nil, cancel, make(chan struct{}, 1), nil)

	if stop {
		t.Error("dispatchSignalSafe() = true, want false: the handler must keep running after a reload panic")
	}

	if ctx.Err() != nil {
		t.Error("context cancelled by a reload-signal panic")
	}
}

func TestSignalNumber(t *testing.T) {
	if got := signalNumber(syscall.SIGTERM); got != int(syscall.SIGTERM) {
		t.Errorf("signalNumber(SIGTERM) = %d, want %d", got, int(syscall.SIGTERM))
	}

	if got := signalNumber(fakeSignal{}); got != -1 {
		t.Errorf("signalNumber(non-syscall signal) = %d, want -1", got)
	}
}

type fakeSignal struct{}

func (fakeSignal) String() string { return "fake" }
func (fakeSignal) Signal()        {}
