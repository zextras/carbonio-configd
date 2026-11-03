// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestStartSdWatchdogKeepAlive_StopsOnCancel(t *testing.T) {
	notifier, err := sdnotify.New()
	require.NoError(t, err)

	clock := systest.New().Clock
	ctx, cancel := context.WithCancel(context.Background())

	startSdWatchdogKeepAlive(ctx, clock, notifier, time.Millisecond)
	require.Eventually(t, func() bool { return clock.Waiters() == 1 }, time.Second, time.Millisecond)

	// One ping interval elapses, then the goroutine observes ctx.Done and
	// releases its ticker.
	clock.Advance(time.Millisecond)
	cancel()
	require.Eventually(t, func() bool { return clock.Waiters() == 0 }, time.Second, time.Millisecond)
}

func TestBootstrapSystemd_ReturnsBufferedReloadChan(t *testing.T) {
	notifier, err := sdnotify.New()
	require.NoError(t, err)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := bootstrapSystemd(context.Background(), systest.New().System(), state.NewState(), cancel, notifier)
	require.NotNil(t, reloadChan)
	require.Equal(t, 1, cap(reloadChan))
}

// TestBootstrapDependencies_OneServiceManagerForConfigAndWatchdog pins the
// shared service manager: config-driven restarts and the watchdog must not own
// independent managers (concurrent restarts of one service, DEPENDS written
// by the main loop while the watchdog goroutine reads it), and a restart run
// by the manager must stop the watchdog watching that service.
func TestBootstrapDependencies_OneServiceManagerForConfigAndWatchdog(t *testing.T) {
	ctx := context.Background()
	appState := state.NewState()
	cfg := &config.Config{Progname: "configd-test", ConfigFile: t.TempDir() + "/missing.cf", WatchdogInterval: 1}

	configManager, serviceManager, wd := bootstrapDependencies(
		ctx, systest.New().System(), cfg, appState, nil, &Args{DisableRestarts: true})

	sm, ok := serviceManager.(*services.ServiceManager)
	require.True(t, ok)
	require.Same(t, sm, configManager.Services(), "the config manager and the daemon must share one service manager")
	require.Same(t, configManager.ServiceMgr, serviceManager)
	require.True(t, sm.DisableRestarts, "--disable-restarts reaches the shared manager")

	wd.AddService(ctx, "antivirus")
	require.True(t, wd.IsServiceTracked("antivirus"))

	sm.BeforeRestart("antivirus")

	require.False(t, wd.IsServiceTracked("antivirus"), "a service restarted on purpose is no longer watched")
}
