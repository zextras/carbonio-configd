// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// cmd/configd/bootstrap.go
package main

import (
	"context"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/systemd"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

// startSdWatchdogKeepAlive launches a goroutine that pings systemd's watchdog at pingInterval.
func startSdWatchdogKeepAlive(
	ctx context.Context, clock sys.Clock, notifier *sdnotify.Notifier, pingInterval time.Duration,
) {
	logger.InfoContext(ctx, "Starting systemd watchdog keep-alive",
		"ping_interval", pingInterval)

	go func() {
		defer recoverGoroutine(ctx, "watchdog")

		ticker := clock.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				if err := notifier.WatchdogPing(); err != nil {
					logger.ErrorContext(ctx, "Failed to send watchdog ping", "error", err)
				}
			}
		}
	}()
}

// bootstrapWatchdog builds the watchdog on the runtime's one service manager.
func bootstrapWatchdog(ctx context.Context, rt *runtime) *watchdog.Watchdog {
	if rt.hooks == nil {
		systemdManager := systemd.NewManager(rt.env)
		if systemdManager.IsSystemdEnabled(ctx) {
			logger.InfoContext(ctx, "Detected systemd-enabled environment",
				"use_systemctl", true,
				"fallback", "zm*ctl")
		} else {
			logger.InfoContext(ctx, "Detected traditional environment",
				"use_systemctl", false,
				"scripts_only", "zm*ctl")
		}
	} else {
		logger.InfoContext(ctx, "Service control through hooks", "hooks_file", rt.cfg.ServiceHooks)
	}

	watchdogInterval := time.Duration(rt.cfg.WatchdogInterval) * time.Second
	if watchdogInterval == 0 {
		watchdogInterval = 120 * time.Second
	}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  watchdogInterval,
		ServiceManager: rt.sm,
		State:          rt.state,
		Clock:          rt.env.Clock,
		Snapshots:      rt.ctl,
		ConfigLookup: func(key string) string {
			val, _ := rt.state.Snapshot().Local(key)

			return val
		},
	})

	wd.SetMetrics(rt.cm.Metrics())

	return wd
}

// bootstrapSystemd initializes systemd integration: signal handlers and watchdog keep-alive.
// Returns the buffered channel that signal handlers and network rewrite requests use to
// wake the main loop for a configuration reload.
func bootstrapSystemd(
	ctx context.Context,
	env sys.System,
	appState *state.State,
	cancel context.CancelFunc,
	notifier *sdnotify.Notifier,
) chan struct{} {
	reloadChan := make(chan struct{}, 1)
	SetupSignalHandler(ctx, appState, cancel, reloadChan, notifier)

	// Start systemd watchdog keep-alive goroutine if WATCHDOG_USEC is set.
	// Pings at half the interval so we stay well within the WatchdogSec deadline.
	if wdInterval, ok := sdnotify.WatchdogEnabled(); ok {
		pingInterval := wdInterval / 2 //nolint:mnd // half of WatchdogSec is the recommended ping interval
		startSdWatchdogKeepAlive(ctx, env.Clock, notifier, pingInterval)
	}

	return reloadChan
}
