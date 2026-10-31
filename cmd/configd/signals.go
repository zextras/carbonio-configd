// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// SetupSignalHandler sets up a goroutine to listen for OS signals until ctx is
// cancelled or a shutdown signal is dispatched.
// It takes a context cancel function to trigger immediate shutdown
// and an optional sd_notify notifier for systemd lifecycle notifications.
func SetupSignalHandler(
	ctx context.Context,
	appState *state.State,
	cancel context.CancelFunc,
	reloadChan chan struct{},
	notifier *sdnotify.Notifier) {
	ctx = logger.ContextWithComponent(ctx, "signals")

	// Create a channel to receive OS signals
	signals := make(chan os.Signal, 1)

	// Register ALL signals in one call to avoid overwriting
	// Shutdown signals: SIGINT, SIGTERM
	// Reload signals: SIGHUP, SIGUSR2, SIGALRM
	// Info signals: SIGCHLD (child process exits - logged but not acted upon)
	signal.Notify(signals,
		syscall.SIGINT, syscall.SIGTERM, // Shutdown
		syscall.SIGHUP, syscall.SIGUSR2, syscall.SIGALRM, // Reload
		syscall.SIGCHLD, // Info only
	)

	logger.InfoContext(ctx, "Signal handler registered for: SIGINT, SIGTERM, SIGHUP, SIGUSR2, SIGALRM, SIGCHLD")

	go func() {
		// Release the registration when the handler exits, so no signal stays
		// captured with nobody left to read it.
		defer signal.Stop(signals)

		logger.DebugContext(ctx, "Signal handler goroutine started")

		for {
			select {
			case <-ctx.Done():
				logger.DebugContext(ctx, "Context cancelled, exiting signal handler goroutine")

				return
			case sig := <-signals:
				logger.InfoContext(ctx, "Received signal",
					"signal", sig.String(),
					"signal_number", signalNumber(sig))

				if dispatchSignalSafe(ctx, sig, appState, cancel, reloadChan, notifier) {
					return
				}
			}
		}
	}()
}

// signalNumber returns the numeric value of sig, or -1 when it is not a syscall.Signal.
func signalNumber(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return int(s)
	}

	return -1
}

// dispatchSignalSafe runs dispatchSignal and absorbs a panic so one bad signal
// cannot kill the handler goroutine. A panic while handling a shutdown signal
// still cancels the daemon context: the signal is already consumed, so without
// this the daemon could no longer be stopped.
func dispatchSignalSafe(
	ctx context.Context,
	sig os.Signal,
	appState *state.State,
	cancel context.CancelFunc,
	reloadChan chan struct{},
	notifier *sdnotify.Notifier,
) (stop bool) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		logger.ErrorContext(ctx, "recovered from panic in signal dispatch",
			"signal", sig.String(),
			"panic", fmt.Sprintf("%v", r),
			"stack", string(debug.Stack()))

		if sig == syscall.SIGINT || sig == syscall.SIGTERM {
			cancel()

			stop = true
		}
	}()

	return dispatchSignal(ctx, sig, appState, cancel, reloadChan, notifier)
}

func dispatchSignal(
	ctx context.Context,
	sig os.Signal,
	appState *state.State,
	cancel context.CancelFunc,
	reloadChan chan struct{},
	notifier *sdnotify.Notifier,
) bool {
	switch sig {
	case syscall.SIGINT, syscall.SIGTERM:
		logger.InfoContext(ctx, "Shutting down",
			"signal", sig.String(),
			"reason", "shutdown_signal")

		if err := notifier.Stopping(); err != nil {
			logger.ErrorContext(ctx, "Failed to send sd_notify STOPPING",
				"error", err)
		}

		cancel()

		return true
	case syscall.SIGHUP, syscall.SIGUSR2, syscall.SIGALRM:
		appState.SetSleepTimer(0)

		if err := notifier.Reloading(); err != nil {
			logger.ErrorContext(ctx, "Failed to send sd_notify RELOADING",
				"error", err)
		}

		logger.InfoContext(ctx, "Triggering configuration reload",
			"signal", sig.String())

		select {
		case reloadChan <- struct{}{}:
			logger.DebugContext(ctx, "Reload signal sent to main loop")
		default:
			logger.DebugContext(ctx, "Reload channel blocked, main loop already processing or not ready")
		}
	case syscall.SIGCHLD:
		logger.DebugContext(ctx, "Child process exited (SIGCHLD), not triggering reload")
	}

	return false
}

// SleepWithContext sleeps for the specified interval but can be interrupted by:
// - Context cancellation (shutdown signal)
// - Reload signal via reloadChan
// Returns true if interrupted, false if sleep completed naturally.
//
//nolint:unparam // Return value is used in multiple places for flow control
func SleepWithContext(ctx context.Context, clock sys.Clock, interval time.Duration, reloadChan <-chan struct{}) bool {
	ctx = logger.ContextWithComponent(ctx, "signals")
	logger.DebugContext(ctx, "Sleeping",
		"duration_seconds", interval.Seconds())

	timer := clock.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		logger.DebugContext(ctx, "Sleep interrupted by shutdown signal",
			"reason", "shutdown")

		return true
	case <-reloadChan:
		logger.DebugContext(ctx, "Sleep interrupted by reload signal",
			"reason", "reload")

		return true
	case <-timer.C():
		logger.DebugContext(ctx, "Waking up from sleep (timer expired)")

		return false
	}
}
