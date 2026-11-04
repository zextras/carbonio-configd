// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"github.com/stretchr/testify/require"
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

// SIGCHLD is informational: it must neither reload nor cancel nor touch the
// sleep timer. dispatchSignal is synchronous, so this needs no waiting; the OS
// delivery path is covered by the other SetupSignalHandler tests.
func TestDispatchSignal_SIGCHLD(t *testing.T) {
	appState := &state.State{SleepTimer: 100}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)

	stop := dispatchSignal(ctx, syscall.SIGCHLD, appState, cancel, reloadChan, nil)

	if stop {
		t.Error("SIGCHLD must not stop the handler")
	}

	select {
	case <-reloadChan:
		t.Error("SIGCHLD should not trigger reload")
	default:
	}

	if ctx.Err() != nil {
		t.Error("Context should not be canceled after SIGCHLD")
	}

	if got := appState.GetSleepTimer(); got != 100 {
		t.Errorf("SIGCHLD changed SleepTimer to %f, want 100", got)
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

	// Send SIGHUP when channel is blocked
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = p.Signal(syscall.SIGHUP)
	if err != nil {
		t.Fatalf("Failed to send SIGHUP: %v", err)
	}

	// The handler uses a non-blocking send, so it must still reset the sleep
	// timer with the channel full.
	require.Eventually(t, func() bool { return appState.GetSleepTimer() == 0 },
		5*time.Second, time.Millisecond, "SleepTimer not reset with a blocked reload channel")

	// Free the slot; the handler must have survived the blocked send and
	// deliver a fresh SIGHUP.
	<-reloadChan

	require.NoError(t, p.Signal(syscall.SIGHUP))

	select {
	case <-reloadChan:
	case <-time.After(5 * time.Second):
		t.Error("handler stopped reacting after a blocked reload")
	}
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
