// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
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
func TestBootstrapWatchdog_OneServiceManagerForConfigAndWatchdog(t *testing.T) {
	ctx := context.Background()

	rt, err := buildRuntime(ctx, systest.New().System(), &runtimeOpts{
		disableRestarts: true,
		loadLocal:       func() (map[string]string, error) { return map[string]string{}, nil },
	})
	require.NoError(t, err)

	wd := bootstrapWatchdog(ctx, rt)

	require.Same(t, rt.sm, rt.cm.Services(), "the config manager and the daemon must share one service manager")
	require.Same(t, rt.cm.ServiceMgr, services.Manager(rt.sm))
	require.True(t, rt.sm.DisableRestarts, "--disable-restarts reaches the shared manager")

	wd.AddService(ctx, "antivirus")
	require.True(t, wd.IsServiceTracked("antivirus"))

	rt.sm.BeforeRestart("antivirus")

	require.False(t, wd.IsServiceTracked("antivirus"), "a service restarted on purpose is no longer watched")
}

func TestWarnHardenedLegacyMode(t *testing.T) {
	for name, tc := range map[string]struct {
		status   string
		wantWarn bool
	}{
		"no-new-privileges set":   {"Name:\tconfigd\nNoNewPrivs:\t1\nSeccomp:\t2\n", true},
		"no-new-privileges clear": {"Name:\tconfigd\nNoNewPrivs:\t0\nSeccomp:\t0\n", false},
		"field missing":           {"Name:\tconfigd\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer

			logger.InitStructuredLogging(&logger.Config{Output: &buf, Level: logger.LogLevelInfo})

			f := systest.New()
			f.FS.Put("/proc/self/status", []byte(tc.status), 0o444)

			warnHardenedLegacyMode(context.Background(), f.System().FS)

			if !tc.wantWarn {
				require.NotContains(t, buf.String(), "level=WARN")

				return
			}

			require.Equal(t, 1, strings.Count(buf.String(), "level=WARN"))
			require.Contains(t, buf.String(), "legacy-mode.conf")
			require.Contains(t, buf.String(), "docs/systemd.md#legacy-mode-on-a-systemd-host")
		})
	}
}

func TestWarnHardenedLegacyMode_UnreadableStatusIsSilent(t *testing.T) {
	var buf bytes.Buffer

	logger.InitStructuredLogging(&logger.Config{Output: &buf, Level: logger.LogLevelInfo})

	warnHardenedLegacyMode(context.Background(), systest.New().System().FS)

	require.NotContains(t, buf.String(), "level=WARN")
}
