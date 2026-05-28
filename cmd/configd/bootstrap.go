// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// cmd/configd/bootstrap.go
package main

import (
	"context"
	"time"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/systemd"
	"github.com/zextras/carbonio-configd/internal/watchdog"
)

// startSdWatchdogKeepAlive launches a goroutine that pings systemd's watchdog at pingInterval.
func startSdWatchdogKeepAlive(ctx context.Context, notifier *sdnotify.Notifier, pingInterval time.Duration) {
	logger.InfoContext(ctx, "Starting systemd watchdog keep-alive",
		"ping_interval", pingInterval)

	go func() {
		defer recoverGoroutine(ctx, "watchdog")

		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := notifier.WatchdogPing(); err != nil {
					logger.ErrorContext(ctx, "Failed to send watchdog ping", "error", err)
				}
			}
		}
	}()
}

// bootstrapDependencies initializes the core dependencies for the daemon:
// config manager, service manager, and watchdog.
// Returns the initialized managers and watchdog instance.
func bootstrapDependencies(
	ctx context.Context,
	mainCfg *config.Config,
	appState *state.State,
	ldapClient *ldap.Ldap,
	args *Args,
) (*configmgr.ConfigManager, services.Manager, *watchdog.Watchdog) {
	cacheInstance := cache.New(ctx, false) // skipCache=false to enable caching

	configManager := configmgr.NewConfigManager(ctx, mainCfg, appState, ldapClient, cacheInstance)
	serviceManager := services.NewServiceManager()

	systemdManager := systemd.NewManager()
	if systemdManager.IsSystemdEnabled(ctx) {
		logger.InfoContext(ctx, "Detected systemd-enabled environment",
			"use_systemctl", true,
			"fallback", "zm*ctl")

		serviceManager.UseSystemd = true

		configManager.ServiceMgr.SetUseSystemd(true)
	} else {
		logger.InfoContext(ctx, "Detected traditional environment",
			"use_systemctl", false,
			"scripts_only", "zm*ctl")

		serviceManager.UseSystemd = false
	}

	serviceManager.DisableRestarts = args.DisableRestarts

	watchdogInterval := time.Duration(mainCfg.WatchdogInterval) * time.Second
	if watchdogInterval == 0 {
		watchdogInterval = 120 * time.Second
	}

	wd := watchdog.NewWatchdog(watchdog.Config{
		CheckInterval:  watchdogInterval,
		ServiceManager: serviceManager,
		State:          appState,
		ConfigLookup: func(key string) string {
			if val, exists := appState.LocalConfig.Data.Get(key); exists {
				return val
			}

			return ""
		},
	})

	return configManager, serviceManager, wd
}

// bootstrapSystemd initializes systemd integration: signal handlers and watchdog keep-alive.
// Returns a cancel function that should be called on shutdown.
func bootstrapSystemd(
	ctx context.Context,
	appState *state.State,
	cancel context.CancelFunc,
	notifier *sdnotify.Notifier,
) chan struct{} {
	reloadChan := make(chan struct{}, 1)
	SetupSignalHandler(appState, cancel, reloadChan, notifier)

	// Start systemd watchdog keep-alive goroutine if WATCHDOG_USEC is set.
	// Pings at half the interval so we stay well within the WatchdogSec deadline.
	if wdInterval, ok := sdnotify.WatchdogEnabled(); ok {
		pingInterval := wdInterval / 2 //nolint:mnd // half of WatchdogSec is the recommended ping interval
		startSdWatchdogKeepAlive(ctx, notifier, pingInterval)
	}

	return reloadChan
}
