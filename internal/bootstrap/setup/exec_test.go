// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// A step command must never get the controlling terminal: zmprov runs stty
// on /dev/tty and, from a background process group, is stopped by SIGTTOU,
// hanging a bootstrap started from a terminal.
func TestStepCommandsRunWithoutTerminal(t *testing.T) {
	e, fakes, _ := testEnv(t)

	require.NoError(t, e.Root("true"))
	require.NoError(t, e.Zextras("/opt/zextras/bin/zmprov", "-r", "-m", "-l"))

	calls := fakes.Exec.Calls()
	require.Len(t, calls, 2)

	for _, c := range calls {
		assert.True(t, c.Setsid, "%s runs in its own session", c)
	}
}

// The real Exec returns *sys.ExitError with a non-zero exit; steps branch on
// CmdError (zmcertmgr verifycrt comm failing means "try self-signed"), so a
// non-zero exit must surface as CmdError, while a command that cannot start
// stays a plain error.
func TestNonZeroExitIsCmdError(t *testing.T) {
	e, fakes, _ := testEnv(t)

	fakes.Exec.OnPath("zmcertmgr", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{ExitCode: 1}, &sys.ExitError{Code: 1}
	})
	fakes.Exec.OnPath("missing", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{ExitCode: -1}, errors.New("executable file not found")
	})

	cmdErr, ok := errors.AsType[*CmdError](e.Root("zmcertmgr", "verifycrt", "comm"))
	require.True(t, ok)
	assert.Equal(t, 1, cmdErr.Code)

	_, ok = errors.AsType[*CmdError](e.Root("missing"))
	assert.False(t, ok)
}
