// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const stubbornPID = 4242

// newStubbornProcess returns a Controller over fakes in which stubbornPID
// ignores SIGTERM (only SIGKILL removes it).
func newStubbornProcess(t *testing.T) (*Controller, *systest.Fakes) {
	t.Helper()

	ctl, f := newFakeController(t)
	f.Proc.Add(sys.ProcEntry{PID: stubbornPID, Argv: []string{"stubborn"}})
	f.Proc.SurviveTerm[stubbornPID] = true

	return ctl, f
}

func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	return ctx
}

func signalsOf(f *systest.Fakes, sig syscall.Signal) int {
	n := 0

	for _, s := range f.Proc.Signals() {
		if s.Sig == sig {
			n++
		}
	}

	return n
}

func TestPollUntilExit_DistinguishesTimeoutFromCancellation(t *testing.T) {
	t.Parallel()

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		ctl, f := newStubbornProcess(t)
		pumpClock(t, f.Clock, time.Second)

		assert.Equal(t, pollTimedOut, ctl.pollUntilExit(context.Background(), stubbornPID, 5*time.Second, time.Second))
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()

		ctl, _ := newStubbornProcess(t)

		assert.Equal(t, pollCancelled, ctl.pollUntilExit(cancelledCtx(), stubbornPID, time.Hour, time.Second))
	})

	t.Run("exited", func(t *testing.T) {
		t.Parallel()

		ctl, _ := newFakeController(t)

		assert.Equal(t, pollExited, ctl.pollUntilExit(cancelledCtx(), stubbornPID, time.Hour, time.Second),
			"a process that is already gone is exited even when ctx is cancelled")
	})
}

// A cancelled context (configd shutting down) must leave the process to finish
// its SIGTERM shutdown: no escalation to SIGKILL on any stop path.
func TestStopPaths_CancellationNeverEscalatesToSIGKILL(t *testing.T) {
	t.Parallel()

	t.Run("waitAndEscalate", func(t *testing.T) {
		t.Parallel()

		ctl, f := newStubbornProcess(t)

		ctl.waitAndEscalate(cancelledCtx(), []int{stubbornPID}, time.Hour)

		assert.Zero(t, signalsOf(f, syscall.SIGKILL))
		assert.True(t, f.Proc.Alive(stubbornPID))
	})

	t.Run("gracefulStopPID", func(t *testing.T) {
		t.Parallel()

		ctl, f := newStubbornProcess(t)

		err := ctl.gracefulStopPID(cancelledCtx(), stubbornPID, "svc", time.Hour)

		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 1, signalsOf(f, syscall.SIGTERM))
		assert.Zero(t, signalsOf(f, syscall.SIGKILL))
	})

	t.Run("killByPIDWithGroupAndSudo", func(t *testing.T) {
		t.Parallel()

		ctl, f := newStubbornProcess(t)

		assert.False(t, ctl.killByPIDWithGroupAndSudo(cancelledCtx(), stubbornPID))
		assert.Zero(t, signalsOf(f, syscall.SIGKILL))
		assert.True(t, f.Proc.Alive(stubbornPID))
	})
}

// The same stop paths still escalate when the grace period really expires.
func TestStopPaths_TimeoutStillEscalatesToSIGKILL(t *testing.T) {
	t.Parallel()

	t.Run("waitAndEscalate", func(t *testing.T) {
		t.Parallel()

		ctl, f := newStubbornProcess(t)
		pumpClock(t, f.Clock, time.Second)

		ctl.waitAndEscalate(context.Background(), []int{stubbornPID}, 3*time.Second)

		assert.Equal(t, 1, signalsOf(f, syscall.SIGKILL))
	})

	t.Run("gracefulStopPID", func(t *testing.T) {
		t.Parallel()

		ctl, f := newStubbornProcess(t)
		pumpClock(t, f.Clock, time.Second)

		require.NoError(t, ctl.gracefulStopPID(context.Background(), stubbornPID, "svc", 3*time.Second))
		assert.Equal(t, 1, signalsOf(f, syscall.SIGKILL))
	})
}

// After the SIGKILL, gracefulStopPID returns only once the process has left
// the table, so a restart's start never races the dying process for its lock
// files or ports.
func TestGracefulStopPID_WaitsForTheKilledProcessToGo(t *testing.T) {
	t.Parallel()

	ctl, f := newStubbornProcess(t)
	f.Proc.KillLinger[stubbornPID] = 3
	pumpClock(t, f.Clock, 50*time.Millisecond)

	require.NoError(t, ctl.gracefulStopPID(context.Background(), stubbornPID, "svc", time.Second))

	assert.Equal(t, 1, signalsOf(f, syscall.SIGKILL))
	assert.False(t, f.Proc.Alive(stubbornPID), "returned while the killed process was still present")
}

// A process that never goes away after SIGKILL (stuck in the kernel) does not
// hang the stop: the wait is bounded by sigkillWait.
func TestGracefulStopPID_KillWaitIsBounded(t *testing.T) {
	t.Parallel()

	ctl, f := newStubbornProcess(t)
	f.Proc.KillLinger[stubbornPID] = 1 << 30
	pumpClock(t, f.Clock, 50*time.Millisecond)

	start := f.Clock.Now()

	require.NoError(t, ctl.gracefulStopPID(context.Background(), stubbornPID, "svc", time.Second))

	waited := f.Clock.Now().Sub(start)
	assert.GreaterOrEqual(t, waited, time.Second+sigkillWait, "gave up before the SIGKILL wait")
	assert.Less(t, waited, time.Second+sigkillWait+time.Second, "waited past the SIGKILL bound")
	assert.True(t, f.Proc.Alive(stubbornPID))
}
