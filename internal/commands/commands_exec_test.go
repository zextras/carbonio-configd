// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestBinaryCommand_ArgvIsCmdArgsThenRuntimeArgs(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("/opt/zextras/bin/zmmtactl", systest.Reply("started\n"))

	cmd := &Command{Name: "mta", Binary: "/opt/zextras/bin/zmmtactl", CmdArgs: []string{"-q"}, Exec: f.Exec}

	rc, out, errText := cmd.Execute(context.Background(), "status")

	assert.Equal(t, 0, rc)
	assert.Equal(t, "started\n", out)
	assert.Equal(t, "OK", errText)
	assert.Equal(t, []string{"/opt/zextras/bin/zmmtactl -q status"}, f.Exec.Lines())
}

func TestBinaryCommand_NonZeroExitKeepsOutputAndIsNotAnError(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("failing", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("out-text\n"), Stderr: []byte("err-text\n"), ExitCode: 3},
			&sys.ExitError{Code: 3, Stderr: []byte("err-text\n")}
	})

	cmd := &Command{Name: "failing", Binary: "failing", Exec: f.Exec}

	rc, out, errText := cmd.Execute(context.Background())

	assert.Equal(t, 3, rc)
	assert.Equal(t, "out-text\n", out)
	assert.Equal(t, "err-text\n", errText)
}

func TestBinaryCommand_SignalDeathIsAnError(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("sig", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{ExitCode: -1}, &sys.ExitError{Code: -1}
	})

	cmd := &Command{Name: "sig", Binary: "sig", Exec: f.Exec}

	rc, _, errText := cmd.Execute(context.Background())

	assert.Negative(t, rc)
	assert.Contains(t, errText, "UNKNOWN: sig died with error")
}

func TestBinaryCommand_SpawnFailureReportsStatus1(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.FailStart(func(*sys.Cmd) bool { return true }, &exec.Error{Name: "missing", Err: exec.ErrNotFound})

	cmd := &Command{Name: "missing", Binary: "missing", Exec: f.Exec}

	rc, _, errText := cmd.Execute(context.Background())

	assert.Equal(t, 1, rc)
	assert.Contains(t, errText, "UNKNOWN: missing died with error")
}

func TestMakePostconfExec_RunsPostconfDashE(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("/opt/zextras/bin/postconf", systest.Reply(""))

	_, err := makePostconfExec(f.Exec, "/opt/zextras/bin/postconf -e")(context.Background(), "smtpd_banner=x", "ignored=1")
	require.NoError(t, err)
	assert.Equal(t, []string{"/opt/zextras/bin/postconf -e smtpd_banner=x"}, f.Exec.Lines())

	f.Exec.OnPath("/opt/zextras/bin/postconf", systest.Fail(1, "boom"))

	_, err = makePostconfExec(f.Exec, "/opt/zextras/bin/postconf -e")(context.Background(), "a=b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postconf command failed")
}

func TestNewRegistry_BinaryCommandsRunThroughEnvExec(t *testing.T) {
	t.Parallel()

	f := systest.New()
	r := NewRegistry("/opt/zextras", f.System())

	rc, _, _ := r.Commands[cmdMTA].Execute(context.Background(), "status")

	assert.Equal(t, 0, rc)
	assert.Equal(t, []string{"/opt/zextras/bin/zmmtactl status"}, f.Exec.Lines())
}
