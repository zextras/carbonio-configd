// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A restart that keeps failing is retried by the later cycles and dropped once
// the default MaxFailedRestarts (3) is reached, not re-stopped and re-started
// by every cycle forever.
func TestProcessRestarts_DefaultCapDropsPersistentFailure(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	registerRestartStub(t, ctl, "broken", errors.New("boom"))

	sm := NewServiceManager(ctl)
	require.Equal(t, 3, sm.MaxFailedRestarts)

	ctx := context.Background()

	require.NoError(t, sm.AddRestart(ctx, "broken"))

	for attempt := 1; attempt <= 2; attempt++ {
		require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed)
		assert.Equal(t, []string{"broken"}, sm.GetPendingRestarts(), "attempt %d: still queued", attempt)
	}

	require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed, "the third failure drops it")
	assert.Empty(t, sm.GetPendingRestarts())

	assert.NoError(t, sm.ProcessRestarts(ctx, nil), "nothing left to retry")

	// A later request for the same service starts with a fresh count.
	require.NoError(t, sm.AddRestart(ctx, "broken"))
	require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed)
	assert.Equal(t, []string{"broken"}, sm.GetPendingRestarts())
}

// A success forgets the failures so far: the cap counts consecutive failures.
func TestProcessRestarts_SuccessResetsFailureCount(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	startErr := errors.New("boom")
	registerRestartStub(t, ctl, "flaky", nil)

	ctl.registry["flaky"].CustomStart = func(context.Context, *ServiceDef) error { return startErr }

	sm := NewServiceManager(ctl)
	ctx := context.Background()

	require.NoError(t, sm.AddRestart(ctx, "flaky"))
	require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed)
	require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed)

	startErr = nil

	require.NoError(t, sm.ProcessRestarts(ctx, nil))
	assert.Empty(t, sm.GetPendingRestarts())

	startErr = errors.New("boom again")

	require.NoError(t, sm.AddRestart(ctx, "flaky"))

	for range 2 {
		require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed)
		assert.Equal(t, []string{"flaky"}, sm.GetPendingRestarts(), "the earlier failures no longer count")
	}
}

// ClearRestarts forgets the failure counts together with the queue.
func TestClearRestarts_ForgetsFailureCounts(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	registerRestartStub(t, ctl, "broken", errors.New("boom"))

	sm := NewServiceManager(ctl)
	ctx := context.Background()

	for range 2 {
		require.NoError(t, sm.AddRestart(ctx, "broken"))
		require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed)
	}

	sm.ClearRestarts(ctx)

	require.NoError(t, sm.AddRestart(ctx, "broken"))
	require.ErrorIs(t, sm.ProcessRestarts(ctx, nil), ErrRestartsFailed)
	assert.Equal(t, []string{"broken"}, sm.GetPendingRestarts(), "the count started over")
}
