// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestLoopHeartbeat_HealthyWithinDeclaredTime(t *testing.T) {
	clock := systest.New().Clock
	hb := newLoopHeartbeat(clock)

	assert.True(t, hb.healthy())

	hb.expect(time.Minute)
	clock.Advance(time.Minute)
	assert.True(t, hb.healthy(), "the deadline itself is still alive")

	clock.Advance(time.Nanosecond)
	assert.False(t, hb.healthy(), "a loop past its declared time is wedged")

	hb.expect(time.Hour)
	assert.True(t, hb.healthy(), "a new stage restores it")

	var none *loopHeartbeat

	none.expect(time.Second)
	assert.True(t, none.healthy(), "no heartbeat means no check")
}

// The keep-alive withholds WATCHDOG=1 while the loop is past its heartbeat, so
// systemd restarts a wedged daemon, and resumes once the loop is alive again.
func TestSendWatchdogPing_WithheldWhileLoopIsWedged(t *testing.T) {
	rec, notifier := newNotifyRecorder(t)
	clock := systest.New().Clock
	hb := newLoopHeartbeat(clock)

	hb.expect(0)
	clock.Advance(time.Minute) // the loop declared no time and a minute passed
	sendWatchdogPing(t.Context(), notifier, hb)
	assert.Zero(t, rec.count(t, "WATCHDOG=1"), "a wedged loop is not vouched for")

	hb.expect(time.Hour)
	sendWatchdogPing(t.Context(), notifier, hb)
	assert.Equal(t, 1, rec.count(t, "WATCHDOG=1"))
}

// The loop's sleep covers its whole length, however long the interval.
func TestDaemonLoopSleep_DeclaresTheWaitToTheHeartbeat(t *testing.T) {
	fix := newCycleFixture(t)
	clock := systest.New().Clock
	fix.deps.env.Clock = clock
	fix.deps.heartbeat = newLoopHeartbeat(clock)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		fix.deps.sleep(ctx, time.Hour, nil)
	}()

	require.Eventually(t, func() bool { return clock.Waiters() == 1 }, time.Second, time.Millisecond)
	clock.Advance(time.Hour)
	assert.True(t, fix.deps.heartbeat.healthy(), "an interval-long sleep is not a stall")

	clock.Advance(sleepSlack + time.Nanosecond)
	assert.False(t, fix.deps.heartbeat.healthy(), "a sleep that does not end is")

	cancel()
	<-done
}
