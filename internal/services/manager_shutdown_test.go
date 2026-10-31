// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessRestarts_ShutdownMidRestartStopsAtOnce pins the SIGTERM behavior:
// when the context is cancelled while a restart runs (the child is killed and
// the control call fails with the cancellation), the processing stops at
// once. Nothing is retried, no further "Restarting service" line is logged, the
// interrupted restart does not count toward MaxFailedRestarts, no error is
// logged, and the pending services are reported.
func TestProcessRestarts_ShutdownMidRestartStopsAtOnce(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	ctl.pinMode(false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var started []string

	register := func(name string, interrupt bool) {
		ctl.registry[name] = &ServiceDef{
			Name: name,
			CustomStart: func(ctx context.Context, _ *ServiceDef) error {
				started = append(started, name)

				if interrupt {
					cancel()

					return ctx.Err()
				}

				return nil
			},
			CustomStop: func(context.Context, *ServiceDef) error { return nil },
		}

		t.Cleanup(func() { delete(ctl.registry, name) })
	}

	register("aa-interrupted", true)
	register("zz-next", false)

	sm := NewServiceManager(ctl)
	sm.MaxFailedRestarts = 1
	sm.RestartQueue["aa-interrupted"] = true
	sm.RestartQueue["zz-next"] = true

	var logs bytes.Buffer

	err := sm.ProcessRestarts(captureInto(ctx, &logs), nil)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrRestartsFailed, "a shutdown is not a failed restart")

	assert.Equal(t, []string{"aa-interrupted"}, started, "no retry, no further service")
	assert.Len(t, restartingLines(logs.String()), 1, "no `Restarting service` after the cancellation")
	assert.Contains(t, logs.String(), `level=INFO msg="Shutdown during service restarts"`)
	assert.Contains(t, logs.String(), `pending="aa-interrupted, zz-next"`)
	assert.NotContains(t, logs.String(), "Failed to restart service")
	assert.NotContains(t, logs.String(), "Removing service from restart queue")
	assert.NotContains(t, logs.String(), "No progress made")
	assert.NotContains(t, logs.String(), "level=ERROR")
	assert.True(t, sm.RestartQueue["aa-interrupted"], "the interrupted restart stays pending, not dropped")
}

// TestProcessRestarts_AlreadyCancelledAttemptsNothing: a context cancelled
// before the processing starts restarts nothing.
func TestProcessRestarts_AlreadyCancelledAttemptsNothing(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	registerRestartStub(t, ctl, "svc", nil)

	sm := NewServiceManager(ctl)
	sm.RestartQueue["svc"] = true

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var logs bytes.Buffer

	err := sm.ProcessRestarts(captureInto(ctx, &logs), nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, restartingLines(logs.String()))
	assert.True(t, sm.RestartQueue["svc"])
}
