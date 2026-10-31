// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sdnotify"
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
