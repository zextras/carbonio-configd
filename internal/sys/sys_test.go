// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

func TestRealExec_RunCapturesStreamsAndExitCode(t *testing.T) {
	t.Parallel()

	res, err := sys.RealExec{}.Run(t.Context(), &sys.Cmd{
		Path: "/bin/sh", Args: []string{"-c", "echo out; echo err >&2; exit 3"},
	})

	var ee *sys.ExitError

	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 3, ee.ExitCode())
	assert.Equal(t, "exit status 3", ee.Error())
	assert.Equal(t, "out\n", string(res.Stdout))
	assert.Equal(t, "err\n", string(res.Stderr))
	assert.Equal(t, 3, res.ExitCode)
}

func TestRealExec_CombinedMergesStreams(t *testing.T) {
	t.Parallel()

	out, err := sys.CombinedOutput(t.Context(), sys.RealExec{}, "/bin/sh", "-c", "echo out; echo err >&2")

	require.NoError(t, err)
	assert.Equal(t, "out\nerr\n", string(out))
}

func TestRealExec_EnvOverridesInheritedAndStdinFeedsChild(t *testing.T) {
	t.Setenv("SYS_TEST_VAR", "inherited")

	res, err := sys.RealExec{}.Run(t.Context(), &sys.Cmd{
		Path:  "/bin/sh",
		Args:  []string{"-c", `read x; echo "$SYS_TEST_VAR $x"`},
		Env:   []string{"SYS_TEST_VAR=override"},
		Stdin: strings.NewReader("from-stdin\n"),
	})

	require.NoError(t, err)
	assert.Equal(t, "override from-stdin\n", string(res.Stdout))
}

func TestRealExec_MissingBinaryIsNotAnExitError(t *testing.T) {
	t.Parallel()

	_, err := sys.RealExec{}.Run(t.Context(), &sys.Cmd{Path: "/nonexistent/binary"})

	require.Error(t, err)

	var ee *sys.ExitError

	assert.False(t, errors.As(err, &ee))
}

func TestRealExec_StartWait(t *testing.T) {
	t.Parallel()

	p, err := sys.RealExec{}.Start(t.Context(), &sys.Cmd{Path: "/bin/sh", Args: []string{"-c", "exit 2"}, Setsid: true})
	require.NoError(t, err)
	assert.Positive(t, p.Pid())

	var ee *sys.ExitError

	require.ErrorAs(t, p.Wait(), &ee)
	assert.Equal(t, 2, ee.Code)
}

func TestRealFS_WriteFileAtomicEnforcesMode(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "f")

	require.NoError(t, sys.RealFS{}.WriteFileAtomic(target, []byte("x"), 0o640))

	st, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o640), st.Mode().Perm())
}

func TestRealFS_OpenFileNilOnError(t *testing.T) {
	t.Parallel()

	f, err := sys.RealFS{}.Open(filepath.Join(t.TempDir(), "missing"))

	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.True(t, f == nil, "an error must not yield a typed-nil File")
}

func TestRealProc_TableEntryAndZombie(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mk := func(pid, cmdline, comm, status string) {
		dir := filepath.Join(root, pid)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644))
	}

	mk("20", "/usr/sbin/memcached\x00-p\x0011211\x00", "memcached\n", "State:\tS (sleeping)\n")
	mk("3", "", "kworker\n", "State:\tZ (zombie)\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "self"), 0o755))

	p := sys.NewRealProc(root)

	tbl, err := p.Table()
	require.NoError(t, err)
	require.Len(t, tbl, 2)
	assert.Equal(t, 3, tbl[0].PID)
	assert.Equal(t, "kworker", tbl[0].Comm)
	assert.Equal(t, []string{"/usr/sbin/memcached", "-p", "11211"}, tbl[1].Argv)
	assert.Empty(t, tbl[1].Comm, "comm is only read when argv is empty")

	assert.True(t, p.Alive(20))
	assert.False(t, p.Alive(3), "a zombie is not alive")
	assert.False(t, p.Alive(999))

	_, ok := p.Entry(999)
	assert.False(t, ok)
}

func TestRealProc_SignalGoneProcess(t *testing.T) {
	t.Parallel()

	err := sys.NewRealProc(sys.ProcRoot).Signal(1<<22+12345, syscall.SIGTERM)

	require.Error(t, err)
	assert.True(t, errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone), "got %v", err)
}

func TestRealClock_SleepHonoursContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	assert.ErrorIs(t, sys.RealClock{}.Sleep(ctx, time.Hour), context.Canceled)
	require.NoError(t, sys.RealClock{}.Sleep(t.Context(), time.Millisecond))
}
