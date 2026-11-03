// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// runService registers a running process for service in the fake host: its
// pid file and its entry in the process table.
func runService(t *testing.T, ctl *Controller, f *systest.Fakes, service string, pid int) {
	t.Helper()

	def := ctl.LookupService(service)
	require.NotNil(t, def)

	f.FS.Put(def.PidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: pid, Argv: []string{def.ProcessName}, Exe: def.ProcessName})
}

func newHooks(t *testing.T, ctl *Controller, json string) *HookBackend {
	t.Helper()

	hooks, err := ParseHooks([]byte(json))
	require.NoError(t, err)

	b, err := NewHookBackend(ctl, hooks)
	require.NoError(t, err)

	return b
}

func TestHookBackend_SignalTermsTheServiceAndNamedProcess(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	runService(t, ctl, f, svcAntivirus, 4001)
	runService(t, ctl, f, svcFreshclam, 4002)

	b := newHooks(t, ctl, `{"antivirus":{"restart":["signal:TERM","signal:HUP@freshclam"]}}`)

	require.NoError(t, b.ServiceRestart(context.Background(), "antivirus"))

	assert.Equal(t, []systest.Signal{
		{PID: 4001, Sig: syscall.SIGTERM},
		{PID: 4002, Sig: syscall.SIGHUP},
	}, f.Proc.Signals())
}

func TestHookBackend_SignalWithoutProcessFails(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	b := newHooks(t, ctl, `{"amavis":{"restart":["signal:TERM"]}}`)

	err := b.ServiceRestart(context.Background(), "amavis")

	require.ErrorIs(t, err, errNoProcess)
	assert.Empty(t, f.Proc.Signals())
}

func TestHookBackend_ExecRunsTheCommand(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath("/bin/reload", systest.Fail(2, "nope"))
	b := newHooks(t, ctl, `{"amavis":{"restart":["exec:/bin/reload -f now"]}}`)

	err := b.ServiceRestart(context.Background(), "amavis")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "restart amavis")
	assert.Equal(t, []string{"/bin/reload -f now"}, f.Exec.Lines())
}

func TestHookBackend_SkipSucceedsWithoutSideEffects(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	b := newHooks(t, ctl, `{"mta":{"restart":["skip"]},"*":{"start":["skip"],"stop":["skip"]}}`)

	require.NoError(t, b.ServiceRestart(context.Background(), "mta"))
	require.NoError(t, b.ServiceStart(context.Background(), "amavis"))
	require.NoError(t, b.ServiceStop(context.Background(), "antivirus"))

	assert.Empty(t, f.Proc.Signals())
	assert.Empty(t, f.Exec.Lines())
}

func TestHookBackend_UndeclaredActionIsAnErrorNotASkip(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	b := newHooks(t, ctl, `{"mta":{"restart":["skip"]}}`)

	require.ErrorIs(t, b.ServiceStart(context.Background(), "mta"), ErrHooksMissing)
}

func TestHookBackend_OwnEntryWinsOverWildcard(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	runService(t, ctl, f, svcAmavis, 4100)
	b := newHooks(t, ctl, `{"amavis":{"restart":["signal:TERM"]},"*":{"restart":["skip"]}}`)

	require.NoError(t, b.ServiceRestart(context.Background(), "amavis"))
	require.NoError(t, b.ServiceRestart(context.Background(), "mta"))

	assert.Len(t, f.Proc.Signals(), 1)
}

func TestHookBackend_Status(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath("/bin/probe", systest.Fail(1, ""))
	b := newHooks(t, ctl, `{"amavis":{"status":["process"]},"antivirus":{"status":["exec:/bin/probe"]}}`)

	running, err := b.ServiceStatus(context.Background(), "amavis")
	require.NoError(t, err)
	assert.False(t, running)

	runService(t, ctl, f, svcAmavis, 4100)

	running, err = b.ServiceStatus(context.Background(), "amavis")
	require.NoError(t, err)
	assert.True(t, running)

	running, err = b.ServiceStatus(context.Background(), "antivirus")
	require.NoError(t, err)
	assert.False(t, running, "a non-zero probe is not running, not an error")

	_, err = b.ServiceStatus(context.Background(), "mta")
	require.ErrorIs(t, err, ErrHooksMissing)
}

func TestHookBackend_ReadOnlyHostRecordsNothing(t *testing.T) {
	t.Parallel()

	f := systest.New()
	ctl := NewController(withMode(sys.ReadOnly(f.System()), false),
		WithConfigLoader(emptyConfig))
	runService(t, ctl, f, svcAmavis, 4100)
	f.Rec.Reset()

	b := newHooks(t, ctl, `{"amavis":{"restart":["signal:TERM","exec:/bin/x"]}}`)

	require.Error(t, b.ServiceRestart(context.Background(), "amavis"))
	assert.Empty(t, f.Proc.Signals())
	assert.Empty(t, f.Exec.Lines())
}

func TestHookBackend_ValidateListsEveryMissingHook(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	b := newHooks(t, ctl, `{"amavis":{"restart":["signal:TERM"]},"*":{"status":["process"]}}`)

	require.NoError(t, b.Validate([]string{"amavis"}, []string{"antivirus"}))

	err := b.Validate([]string{"amavis", "mta", "antivirus"}, []string{"antivirus"})

	require.ErrorIs(t, err, ErrHooksMissing)
	assert.Equal(t, "service hooks missing: restart:antivirus, restart:mta", err.Error())

	b = newHooks(t, ctl, `{"amavis":{"restart":["signal:TERM"]}}`)
	err = b.Validate([]string{"clamd"}, []string{"antivirus", "amavis"})
	require.ErrorIs(t, err, ErrHooksMissing)
	assert.Equal(t, "service hooks missing: restart:clamd, status:amavis, status:antivirus", err.Error())
}

func TestNewHookBackend_RejectsInvalidSpecs(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	for name, json := range map[string]string{
		"unknown service": `{"nope":{"restart":["skip"]}}`,
		"unknown action":  `{"amavis":{"reboot":["skip"]}}`,
		"empty list":      `{"amavis":{"restart":[]}}`,
		"unknown hook":    `{"amavis":{"restart":["kill"]}}`,
		"bad signal":      `{"amavis":{"restart":["signal:BOOM"]}}`,
		"bad target":      `{"amavis":{"restart":["signal:TERM@nope"]}}`,
		"empty exec":      `{"amavis":{"restart":["exec:"]}}`,
		"skip status":     `{"amavis":{"status":["skip"]}}`,
		"process restart": `{"amavis":{"restart":["process"]}}`,
		"signal status":   `{"amavis":{"status":["signal:TERM"]}}`,
	} {
		hooks, err := ParseHooks([]byte(json))
		require.NoError(t, err, name)

		_, err = NewHookBackend(ctl, hooks)
		require.ErrorIs(t, err, ErrHooks, name)
	}

	_, err := ParseHooks([]byte(`{`))
	require.ErrorIs(t, err, ErrHooks)
}
