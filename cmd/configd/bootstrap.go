// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// cmd/configd/bootstrap.go
package main

import (
	"context"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
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

// legacyModeDoc is where the legacy-mode drop-in is explained.
const legacyModeDoc = "docs/systemd.md#legacy-mode-on-a-systemd-host"

// warnHardenedLegacyMode logs one WARN when the daemon, which orchestrates
// services itself (legacy mode: no carbonio target enabled), runs under the
// hardened carbonio-configd.service. There no-new-privileges is set, so the
// `sudo` and file-capability binaries (nginx on port 443) it starts fail with
// "operation not permitted" or "no new privileges". The shipped unit is meant
// for hosts where systemd orchestrates the services; for the unsupported
// mixed case the package ships an opt-in drop-in (legacy-mode.conf).
func warnHardenedLegacyMode(ctx context.Context, fs sys.FS) {
	status, err := fs.ReadFile("/proc/self/status")
	if err != nil || !noNewPrivsSet(status) {
		return
	}

	logger.WarnContext(ctx, "configd runs with no-new-privileges (hardened carbonio-configd.service) but no Carbonio "+
		"systemd target is enabled, so it starts the services itself: sudo and file-capability binaries such as "+
		"nginx will fail. Either enable the carbonio targets or install the legacy-mode drop-in "+
		"/usr/share/carbonio-configd/systemd/legacy-mode.conf into "+
		"/etc/systemd/system/carbonio-configd.service.d/",
		"doc", legacyModeDoc)
}

// noNewPrivsSet reports whether a /proc/<pid>/status has NoNewPrivs: 1.
func noNewPrivsSet(status []byte) bool {
	for line := range strings.SplitSeq(string(status), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok && name == "NoNewPrivs" {
			return strings.TrimSpace(value) == "1"
		}
	}

	return false
}

// bootstrapWatchdog builds the watchdog on the runtime's one service manager.
func bootstrapWatchdog(ctx context.Context, rt *runtime) *watchdog.Watchdog {
	if rt.hooks == nil {
		// The service manager's answer is the daemon's one systemd-mode
		// detection: the restart policy and the watchdog use it too.
		if rt.sm.IsSystemdMode() {
			logger.InfoContext(ctx, "Detected systemd-enabled environment",
				"use_systemctl", true,
				"fallback", "zm*ctl")
		} else {
			logger.InfoContext(ctx, "Detected traditional environment",
				"use_systemctl", false,
				"scripts_only", "zm*ctl")
			warnHardenedLegacyMode(ctx, rt.env.FS)
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
		Snapshots:      rt.sm,
		Systemd:        rt.sm,
		ConfigLookup: func(key string) string {
			val, _ := rt.state.Snapshot().Local(key)

			return val
		},
	})

	wd.SetMetrics(rt.cm.Metrics())

	// A service restarted on purpose is not the watchdog's to restart: it stops
	// watching it until a later check sees it running again.
	rt.sm.BeforeRestart = func(service string) { wd.RemoveService(ctx, service) }

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
