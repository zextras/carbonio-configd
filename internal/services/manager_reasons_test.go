// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// capturedCtx returns a context whose logger writes text records to the buffer.
func capturedCtx(buf *bytes.Buffer) context.Context {
	return logger.ContextWithLogger(context.Background(),
		slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// restartingLines returns the "Restarting service" log lines.
func restartingLines(logs string) []string {
	var out []string

	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, `msg="Restarting service"`) {
			out = append(out, line)
		}
	}

	return out
}

func TestAddRestartWithReasons_MergesAndLogsOnce(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	registerRestartStub(t, ctl, "ok-svc", nil)

	sm := NewServiceManager(ctl)

	var logs bytes.Buffer

	ctx := capturedCtx(&logs)

	require.NoError(t, sm.AddRestartWithReasons(ctx, "ok-svc", []string{"a", "b"}))
	require.NoError(t, sm.AddRestartWithReasons(ctx, "ok-svc", []string{"b", "c"}))
	require.NoError(t, sm.AddRestart(ctx, "ok-svc"))

	require.NoError(t, sm.ProcessRestarts(ctx, nil))

	lines := restartingLines(logs.String())
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "service=ok-svc")
	assert.Contains(t, lines[0], `reasons="[a b c]"`)
	assert.Empty(t, sm.restartReasons, "reasons leave with the queue entry")
}

func TestAddRestart_NoReasonsLogsEmptySlice(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	registerRestartStub(t, ctl, "ok-svc", nil)

	sm := &ServiceManager{ctl: ctl, RestartQueue: map[string]bool{}, MaxFailedRestarts: 1}

	var logs bytes.Buffer

	ctx := capturedCtx(&logs)

	require.NoError(t, sm.AddRestart(ctx, "ok-svc"))
	require.NoError(t, sm.ProcessRestarts(ctx, nil))

	lines := restartingLines(logs.String())
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "reasons=[]")
}

func TestAddRestartWithReasons_StructLiteralNilMap(t *testing.T) {
	t.Parallel()

	sm := &ServiceManager{RestartQueue: map[string]bool{}}

	require.NoError(t, sm.AddRestartWithReasons(context.Background(), "mta", []string{"x"}))
	assert.Equal(t, []string{"x"}, sm.restartReasons["mta"])
}

func TestProcessRestarts_CascadeCarriesDependencyReason(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	registerRestartStub(t, ctl, "parent", nil)
	registerRestartStub(t, ctl, "child", nil)

	sm := NewServiceManager(ctl)
	sm.StartOrder = map[string]int{"parent": 0, "child": 1}
	sm.Dependencies = map[string][]string{"parent": {"child"}}

	var logs bytes.Buffer

	ctx := capturedCtx(&logs)

	require.NoError(t, sm.AddRestartWithReasons(ctx, "parent", []string{"because"}))

	lookup := func(key string) string {
		if key == "SERVICE_CHILD" {
			return "enabled"
		}

		return "disabled"
	}

	require.NoError(t, sm.ProcessRestarts(ctx, lookup))

	lines := restartingLines(logs.String())
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "service=parent")
	assert.Contains(t, lines[0], `reasons=[because]`)
	assert.Contains(t, lines[1], "service=child")
	assert.Contains(t, lines[1], `reasons="[dependency of parent]"`)
}

func TestClearRestarts_ClearsReasons(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	sm := NewServiceManager(ctl)

	require.NoError(t, sm.AddRestartWithReasons(context.Background(), "mta", []string{"x"}))
	sm.ClearRestarts(context.Background())

	assert.Empty(t, sm.RestartQueue)
	assert.Empty(t, sm.restartReasons)
}

func TestProcessRestarts_DroppedServiceClearsReasons(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	registerRestartStub(t, ctl, "bad", errors.New("boom"))

	sm := NewServiceManager(ctl)
	sm.MaxFailedRestarts = 1

	require.NoError(t, sm.AddRestartWithReasons(context.Background(), "bad", []string{"x"}))

	require.ErrorIs(t, sm.ProcessRestarts(context.Background(), nil), ErrRestartsFailed)
	assert.Empty(t, sm.restartReasons)
}
