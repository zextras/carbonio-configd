// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// sleepResult runs SleepWithContext on its own goroutine and returns the
// channel that receives its result, once the sleep has armed its timer on the
// fake clock (or returned early).
func sleepResult(
	t *testing.T, ctx context.Context, clock *systest.Clock, interval time.Duration, reloadChan <-chan struct{},
) <-chan bool {
	t.Helper()

	done := make(chan bool, 1)

	go func() { done <- SleepWithContext(ctx, clock, interval, reloadChan) }()

	require.Eventually(t, func() bool { return clock.Waiters() == 1 || len(done) == 1 },
		time.Second, time.Millisecond)

	return done
}

// TestSleepWithContext verifies the sleep is interrupted by shutdown or reload
// and otherwise lasts exactly the requested interval of (fake) time.
func TestSleepWithContext(t *testing.T) {
	t.Parallel()

	t.Run("context cancellation interrupts sleep", func(t *testing.T) {
		t.Parallel()

		clock := systest.New().Clock
		ctx, cancel := context.WithCancel(context.Background())

		done := sleepResult(t, ctx, clock, 5*time.Second, make(chan struct{}))
		cancel()

		require.True(t, <-done, "sleep must report an interruption")
	})

	t.Run("reload channel interrupts sleep", func(t *testing.T) {
		t.Parallel()

		clock := systest.New().Clock
		reloadChan := make(chan struct{})

		done := sleepResult(t, context.Background(), clock, 5*time.Second, reloadChan)
		reloadChan <- struct{}{}

		require.True(t, <-done, "sleep must report an interruption")
	})

	t.Run("sleep completes naturally", func(t *testing.T) {
		t.Parallel()

		clock := systest.New().Clock

		done := sleepResult(t, context.Background(), clock, 100*time.Millisecond, make(chan struct{}))

		clock.Advance(99 * time.Millisecond)
		require.Empty(t, done, "sleep must outlast less than the interval")

		clock.Advance(time.Millisecond)
		require.False(t, <-done, "sleep must complete naturally")
	})
}

func TestSleepWithContext_ZeroDuration(t *testing.T) {
	t.Parallel()

	clock := systest.New().Clock
	done := make(chan bool, 1)

	go func() { done <- SleepWithContext(context.Background(), clock, 0, make(chan struct{})) }()

	require.Eventually(t, func() bool { return clock.Waiters() == 1 }, time.Second, time.Millisecond)
	clock.Advance(0)

	require.False(t, <-done, "zero duration sleep is not an interruption")
}

func TestSleepWithContext_ImmediateCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.True(t, SleepWithContext(ctx, systest.New().Clock, 5*time.Second, make(chan struct{})),
		"an already cancelled context interrupts at once")
}

func TestSleepWithContext_ImmediateReload(t *testing.T) {
	t.Parallel()

	reloadChan := make(chan struct{}, 1)
	reloadChan <- struct{}{}

	require.True(t, SleepWithContext(context.Background(), systest.New().Clock, 5*time.Second, reloadChan),
		"a pending reload signal interrupts at once")
}

func TestSleepWithContext_ContextCancelDuringReload(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	reloadChan := make(chan struct{}, 1)

	// Both signals pending: either wins, but the sleep must be interrupted.
	reloadChan <- struct{}{}

	cancel()

	require.True(t, SleepWithContext(ctx, systest.New().Clock, 5*time.Second, reloadChan))
}
