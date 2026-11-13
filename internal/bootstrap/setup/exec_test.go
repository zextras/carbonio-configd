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

// The zextras environment's PERL5LIB is what zextras.bashrc builds from
// `perl -V:archname`: "<lib>/<archname>:<lib>". Perl itself is never run.
func TestZextrasEnv_PerlLibFromArchDirectory(t *testing.T) {
	const lib = "/opt/zextras/common/lib/perl5"

	tests := []struct {
		name string
		dirs []string
		want string
	}{
		{"one arch dir", []string{"x86_64-linux-thread-multi", "auto", "Mail"}, lib + "/x86_64-linux-thread-multi:" + lib},
		{"no arch dir", []string{"auto", "Mail"}, lib},
		{"no perl5 dir", nil, lib},
		{"several: first by name", []string{"b-linux-x", "a-linux-y"}, lib + "/a-linux-y:" + lib},
		{"several: this machine's", []string{"aarch64-linux-thread-multi", "x86_64-linux-thread-multi"}, lib + "/x86_64-linux-thread-multi:" + lib},
		{"debian archname", []string{"x86_64-linux-gnu-thread-multi"}, lib + "/x86_64-linux-gnu-thread-multi:" + lib},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, fakes, _ := testEnv(t)
			e.perlLib = ""

			for _, d := range tc.dirs {
				require.NoError(t, fakes.FS.MkdirAll(lib+"/"+d, 0o755))
			}

			if tc.name == "several: this machine's" {
				assert.Equal(t, tc.want, perlLib(e.Sys.FS, lib, "amd64"))
				assert.Equal(t, lib+"/aarch64-linux-thread-multi:"+lib, perlLib(e.Sys.FS, lib, "arm64"))

				return
			}

			env := e.zextrasEnv()
			assert.Contains(t, env, "PERL5LIB="+tc.want)
			assert.Contains(t, env, "PERLLIB="+tc.want)

			for _, c := range fakes.Exec.Calls() {
				assert.NotEqual(t, "perl", c.Path)
			}
		})
	}
}

// A regular file named like an arch directory is not one.
func TestPerlLib_IgnoresFiles(t *testing.T) {
	e, fakes, _ := testEnv(t)

	require.NoError(t, fakes.FS.MkdirAll("/lib", 0o755))
	require.NoError(t, fakes.FS.WriteFileAtomic("/lib/x86_64-linux-thread-multi", []byte("x"), 0o644))

	assert.Equal(t, "/lib", perlLib(e.Sys.FS, "/lib", "amd64"))
}
