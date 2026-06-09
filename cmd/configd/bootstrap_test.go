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
)

func TestStartSdWatchdogKeepAlive_StopsOnCancel(t *testing.T) {
	notifier, err := sdnotify.New()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// Tight ping interval so at least one tick fires before cancellation.
	startSdWatchdogKeepAlive(ctx, notifier, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Give the goroutine a beat to observe ctx.Done and exit cleanly.
	time.Sleep(20 * time.Millisecond)
}

func TestBootstrapSystemd_ReturnsBufferedReloadChan(t *testing.T) {
	notifier, err := sdnotify.New()
	require.NoError(t, err)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := bootstrapSystemd(context.Background(), state.NewState(), cancel, notifier)
	require.NotNil(t, reloadChan)
	require.Equal(t, 1, cap(reloadChan))
}
