// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/state"
)

// SetupSignalHandler sets up a goroutine to listen for OS signals.
// It takes a context cancel function to trigger immediate shutdown
// and an optional sd_notify notifier for systemd lifecycle notifications.
func SetupSignalHandler(
	appState *state.State,
	cancel context.CancelFunc,
	reloadChan chan struct{},
	notifier *sdnotify.Notifier) {
	ctx := context.Background()
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
		logger.DebugContext(ctx, "Signal handler goroutine started")

		for sig := range signals {
			logger.InfoContext(ctx, "Received signal",
				"signal", sig.String(),
				"signal_number", int(sig.(syscall.Signal)))

			if dispatchSignal(ctx, sig, appState, cancel, reloadChan, notifier) {
				return
			}
		}

		logger.DebugContext(ctx, "Signal channel closed, exiting handler goroutine")
	}()
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
func SleepWithContext(ctx context.Context, interval time.Duration, reloadChan <-chan struct{}) bool {
	ctx = logger.ContextWithComponent(ctx, "signals")
	logger.DebugContext(ctx, "Sleeping",
		"duration_seconds", interval.Seconds())

	timer := time.NewTimer(interval)
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
	case <-timer.C:
		logger.DebugContext(ctx, "Waking up from sleep (timer expired)")

		return false
	}
}
