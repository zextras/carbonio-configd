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

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestHookBackend_LookupServiceResolvesTheRegistry(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	b := newHooks(t, ctl, `{"*":{"restart":["skip"]}}`)

	def := b.LookupService("amavis")
	require.NotNil(t, def)
	assert.Equal(t, svcAmavis, def.Name)

	assert.Nil(t, b.LookupService("no-such-service"))
}

func TestHookBackend_UnknownServiceIsRejectedBeforeAnyHookRuns(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	b := newHooks(t, ctl, `{"*":{"restart":["exec:/bin/x"],"status":["exec:/bin/x"]}}`)

	err := b.ServiceRestart(context.Background(), "no-such-service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-service")

	running, err := b.ServiceStatus(context.Background(), "no-such-service")
	require.Error(t, err)
	assert.False(t, running)
	assert.Contains(t, err.Error(), "no-such-service")

	assert.Empty(t, f.Exec.Lines(), "the wildcard hook did not run for a service that does not exist")
}

func TestHookBackend_StatusWithoutHookIsAnError(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	b := newHooks(t, ctl, `{"mta":{"restart":["skip"]}}`)

	running, err := b.ServiceStatus(context.Background(), "mta")

	require.ErrorIs(t, err, ErrHooksMissing)
	assert.False(t, running)
}

func TestHookBackend_StatusStopsAtTheFirstHookThatSaysNotRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath("/bin/up", systest.Reply(""))
	f.Exec.OnPath("/bin/down", systest.Fail(3, ""))
	b := newHooks(t, ctl, `{"amavis":{"status":["exec:/bin/up","exec:/bin/down","exec:/bin/never"]}}`)

	running, err := b.ServiceStatus(context.Background(), "amavis")

	require.NoError(t, err, "a non-zero exit is an answer, not a failure")
	assert.False(t, running)
	assert.Equal(t, []string{"/bin/up", "/bin/down"}, f.Exec.Lines())
}

func TestHookBackend_StatusReportsACommandThatCannotRun(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == "/bin/probe" }, errors.New("exec format error"))
	b := newHooks(t, ctl, `{"amavis":{"status":["exec:/bin/probe"]}}`)

	running, err := b.ServiceStatus(context.Background(), "amavis")

	require.ErrorContains(t, err, "run /bin/probe")
	assert.False(t, running)
}

func TestHookBackend_RestartStopsAtTheFirstFailingHook(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath("/bin/first", systest.Fail(1, "boom"))
	b := newHooks(t, ctl, `{"amavis":{"restart":["exec:/bin/first","exec:/bin/second"]}}`)

	err := b.ServiceRestart(context.Background(), "amavis")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `hook "exec:/bin/first"`)
	assert.Equal(t, []string{"/bin/first"}, f.Exec.Lines())
}

func TestHookBackend_ExecThatCannotStartNamesTheBinary(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == "/bin/missing" }, errors.New("no such file"))
	b := newHooks(t, ctl, `{"amavis":{"restart":["exec:/bin/missing"]}}`)

	err := b.ServiceRestart(context.Background(), "amavis")

	require.ErrorContains(t, err, "run /bin/missing")
	require.ErrorContains(t, err, "no such file")
}
