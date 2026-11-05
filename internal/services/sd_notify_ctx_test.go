// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// ctxRecordingExec captures the context handed to Start.
type ctxRecordingExec struct {
	sys.Exec
	startCtx chan context.Context
}

func (e ctxRecordingExec) Start(ctx context.Context, c *sys.Cmd) (sys.Process, error) {
	e.startCtx <- ctx

	return e.Exec.Start(ctx, c) //nolint:wrapcheck // test double
}

// TestStartWithSDNotify_ChildIsNotBoundToCallerContext: the sd_notify child is
// a long-lived daemon (opendkim, nginx); os/exec kills a child when the context
// it was started with ends, so tying it to the caller's context would SIGKILL
// the daemon when configd shuts down.
func TestStartWithSDNotify_ChildIsNotBoundToCallerContext(t *testing.T) {
	t.Parallel()

	ctl, _ := newSDController(t)
	rec := ctxRecordingExec{Exec: ctl.env.Exec, startCtx: make(chan context.Context, 1)}
	ctl.env.Exec = rec

	ctx, cancel := context.WithCancel(context.Background())
	done := startAsync(ctx, t, ctl, &sys.Cmd{Path: "opendkim"}, "test-ctx-bound")

	// startWithSDNotify returning before it starts the child (e.g. it could
	// not bind its notify socket) must fail the test, not leave it waiting for
	// a context that is never recorded.
	var startCtx context.Context

	select {
	case startCtx = <-rec.startCtx:
	case r := <-done:
		require.FailNow(t, "startWithSDNotify returned before starting the child", "error: %v", r.err)
	case <-time.After(sdWaitBound):
		require.FailNow(t, "the child was not started", "within %s", sdWaitBound)
	}

	cancel()

	r := awaitSDResult(t, done)
	require.ErrorIs(t, r.err, context.Canceled, "the readiness wait still honours the caller's context")
	assert.NoError(t, startCtx.Err(), "the child's context must outlive the caller's")
	assert.Nil(t, startCtx.Done(), "the child gets a context that is never cancelled")
}
