// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

func TestRealFS_FileLifecycle(t *testing.T) {
	t.Parallel()

	var rfs sys.RealFS

	dir := filepath.Join(t.TempDir(), "a", "b")
	require.NoError(t, rfs.MkdirAll(dir, 0o755))

	name := filepath.Join(dir, "f.txt")
	w, err := rfs.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	assert.Equal(t, name, w.Name())

	_, err = w.WriteString("hello")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	data, err := rfs.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	r, err := rfs.Open(name)
	require.NoError(t, err)

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
	require.NoError(t, r.Close())

	_, err = rfs.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	require.ErrorIs(t, err, fs.ErrExist)

	st, err := rfs.Stat(name)
	require.NoError(t, err)
	assert.Equal(t, int64(5), st.Size())

	require.NoError(t, rfs.Chmod(name, 0o640))

	st, err = rfs.Stat(name)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o640), st.Mode().Perm())

	require.NoError(t, rfs.Chown(name, os.Getuid(), os.Getgid()))

	moved := filepath.Join(dir, "g.txt")
	require.NoError(t, rfs.Rename(name, moved))

	_, err = rfs.Stat(name)
	require.ErrorIs(t, err, fs.ErrNotExist)

	require.NoError(t, rfs.Remove(moved))
	require.ErrorIs(t, rfs.Remove(moved), fs.ErrNotExist)

	require.NoError(t, rfs.RemoveAll(filepath.Dir(dir)))

	_, err = rfs.Lstat(dir)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestRealFS_CreateTempHonoursPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	f, err := sys.RealFS{}.CreateTemp(dir, "pre-*.suf")
	require.NoError(t, err)

	defer func() { _ = f.Close() }()

	assert.Equal(t, dir, filepath.Dir(f.Name()))
	assert.True(t, strings.HasPrefix(filepath.Base(f.Name()), "pre-"))
	assert.True(t, strings.HasSuffix(f.Name(), ".suf"))

	_, err = sys.RealFS{}.CreateTemp(filepath.Join(dir, "missing"), "x")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestRealFS_DirectoryOperations(t *testing.T) {
	t.Parallel()

	var rfs sys.RealFS

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.conf"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.conf"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "c.conf"), nil, 0o600))

	ents, err := rfs.ReadDir(root)
	require.NoError(t, err)

	var names []string

	for _, e := range ents {
		names = append(names, e.Name())
	}

	assert.Equal(t, []string{"a.conf", "b.conf", "sub"}, names)

	var walked []string

	require.NoError(t, rfs.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		require.NoError(t, err)

		rel, _ := filepath.Rel(root, p)
		walked = append(walked, rel)

		return nil
	}))
	assert.Equal(t, []string{".", "a.conf", "b.conf", "sub", "sub/c.conf"}, walked)

	matches, err := rfs.Glob(filepath.Join(root, "*.conf"))
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "a.conf"), filepath.Join(root, "b.conf")}, matches)

	_, err = rfs.ReadDir(filepath.Join(root, "missing"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestRealFS_SymlinkReadlinkLstat(t *testing.T) {
	t.Parallel()

	var rfs sys.RealFS

	root := t.TempDir()
	target := filepath.Join(root, "t")
	link := filepath.Join(root, "l")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
	require.NoError(t, rfs.Symlink(target, link))
	require.ErrorIs(t, rfs.Symlink(target, link), fs.ErrExist)

	got, err := rfs.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, target, got)

	_, err = rfs.Readlink(target)
	require.Error(t, err, "a regular file is not a link")

	lst, err := rfs.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, lst.Mode()&fs.ModeSymlink)

	st, err := rfs.Stat(link)
	require.NoError(t, err)
	assert.Zero(t, st.Mode()&fs.ModeSymlink)
}

func TestRealFS_CopyFileAndAvailBytes(t *testing.T) {
	t.Parallel()

	var rfs sys.RealFS

	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	require.NoError(t, os.WriteFile(src, []byte("payload"), 0o600))

	require.NoError(t, rfs.CopyFile(t.Context(), src, dst))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(data))

	require.Error(t, rfs.CopyFile(t.Context(), filepath.Join(root, "missing"), dst))

	n, err := rfs.AvailBytes(root)
	require.NoError(t, err)
	assert.Positive(t, n)

	_, err = rfs.AvailBytes(filepath.Join(root, "missing"))
	require.ErrorIs(t, err, syscall.ENOENT)
}

func TestRealFS_WriteFileAtomicReplacesWithoutLeavingTemps(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "conf")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))

	require.NoError(t, sys.RealFS{}.WriteFileAtomic(target, []byte("new"), 0o644))

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))

	ents, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, ents, 1, "the temp file is renamed away")

	require.Error(t, sys.RealFS{}.WriteFileAtomic(filepath.Join(root, "nodir", "f"), nil, 0o600))
}

func TestRealFS_OpenFileErrorYieldsNilInterface(t *testing.T) {
	t.Parallel()

	f, err := sys.RealFS{}.OpenFile(filepath.Join(t.TempDir(), "missing"), os.O_RDONLY, 0)

	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.True(t, f == nil)
}

func TestRealExec_RunStdoutStderrWritersAndCwd(t *testing.T) {
	t.Parallel()

	var so, se bytes.Buffer

	dir := t.TempDir()

	res, err := sys.RealExec{}.Run(t.Context(), &sys.Cmd{
		Path: "/bin/sh", Args: []string{"-c", "pwd; echo err >&2"}, Dir: dir, Stdout: &so, Stderr: &se,
	})
	require.NoError(t, err)

	resolved, _ := filepath.EvalSymlinks(dir)
	assert.Equal(t, resolved+"\n", so.String())
	assert.Equal(t, "err\n", se.String())
	assert.Empty(t, res.Stdout)
	assert.Empty(t, res.Stderr)
}

func TestRealExec_RunOnlyStdoutWriterStillCapturesStderr(t *testing.T) {
	t.Parallel()

	var so bytes.Buffer

	res, err := sys.RealExec{}.Run(t.Context(), &sys.Cmd{
		Path: "/bin/sh", Args: []string{"-c", "echo out; echo err >&2; exit 1"}, Stdout: &so, Combined: true,
	})

	var ee *sys.ExitError

	require.ErrorAs(t, err, &ee)
	assert.Equal(t, "out\n", so.String())
	assert.Equal(t, "err\n", string(res.Stderr), "Combined is ignored when a writer is set")
	assert.Equal(t, "err\n", string(ee.Stderr))
	assert.Equal(t, 1, ee.Code)

	var xe *exec.ExitError

	require.ErrorAs(t, err, &xe, "the os/exec cause stays reachable")
}

func TestRealExec_RunKilledBySignalReportsMinusOne(t *testing.T) {
	t.Parallel()

	res, err := sys.RealExec{}.Run(t.Context(), &sys.Cmd{
		Path: "/bin/sh", Args: []string{"-c", "kill -KILL $$"},
	})

	var ee *sys.ExitError

	require.ErrorAs(t, err, &ee)
	assert.Equal(t, -1, ee.Code)
	assert.Equal(t, -1, res.ExitCode)
}

func TestRealExec_RunCancelledContextIsNotAnExitError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err := sys.RealExec{}.Run(ctx, &sys.Cmd{Path: "/bin/sleep", Args: []string{"30"}})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, -1, res.ExitCode)

	var ee *sys.ExitError

	assert.False(t, errors.As(err, &ee))
}

func TestRealExec_RunTimeoutKillsChild(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := sys.RealExec{}.Run(ctx, &sys.Cmd{Path: "/bin/sleep", Args: []string{"30"}})

	require.Error(t, err)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestRealExec_EnvNilInheritsAndExtraAdds(t *testing.T) {
	t.Setenv("SYS_INHERIT", "yes")

	out, err := sys.Output(t.Context(), sys.RealExec{}, "/bin/sh", "-c", `echo "$SYS_INHERIT"`)
	require.NoError(t, err)
	assert.Equal(t, "yes\n", string(out))

	res, err := sys.RealExec{}.Run(t.Context(), &sys.Cmd{
		Path: "/bin/sh", Args: []string{"-c", `echo "$SYS_INHERIT $SYS_NEW"`}, Env: []string{"SYS_NEW=1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "yes 1\n", string(res.Stdout), "extra variables layer over the inherited environment")
}

func TestRealExec_StartSuccessMissingAndRelease(t *testing.T) {
	t.Parallel()

	_, err := sys.RealExec{}.Start(t.Context(), &sys.Cmd{Path: "/nonexistent/binary"})

	require.Error(t, err)

	var ee *sys.ExitError

	assert.False(t, errors.As(err, &ee))

	var so bytes.Buffer

	p, err := sys.RealExec{}.Start(t.Context(), &sys.Cmd{Path: "/bin/sh", Args: []string{"-c", "echo hi"}, Stdout: &so})
	require.NoError(t, err)
	require.NoError(t, p.Wait())
	assert.Equal(t, "hi\n", so.String())

	rel, err := sys.RealExec{}.Start(t.Context(), &sys.Cmd{Path: "/bin/sleep", Args: []string{"0"}})
	require.NoError(t, err)
	require.NoError(t, rel.Release())
}

func TestRealExec_StartSetsidMakesChildItsOwnGroupLeader(t *testing.T) {
	t.Parallel()

	var so bytes.Buffer

	p, err := sys.RealExec{}.Start(t.Context(), &sys.Cmd{
		Path: "/bin/sh", Args: []string{"-c", "ps -o pid=,pgid= -p $$"}, Setsid: true, Stdout: &so,
	})
	require.NoError(t, err)
	require.NoError(t, p.Wait())

	fields := strings.Fields(so.String())
	if len(fields) != 2 {
		t.Skipf("ps unavailable: %q", so.String())
	}

	assert.Equal(t, fields[0], fields[1], "pgid == pid under Setsid")
}

func TestRealProc_SelfIdentity(t *testing.T) {
	t.Parallel()

	p := sys.NewRealProc(sys.ProcRoot)

	assert.Equal(t, os.Getpid(), p.Pid())
	assert.Equal(t, os.Getppid(), p.Ppid())
	assert.Equal(t, os.Getuid(), p.UID())
	assert.True(t, p.Alive(os.Getpid()))
	assert.False(t, p.Zombie(os.Getpid()))

	e, ok := p.Entry(os.Getpid())
	require.True(t, ok)
	assert.Equal(t, os.Getpid(), e.PID)
	assert.NotEmpty(t, e.Argv)
}

func TestRealProc_NewRealProcAddsTrailingSlash(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "7"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "7", "comm"), []byte("x\n"), 0o600))

	e, ok := sys.NewRealProc(root).Entry(7)
	require.True(t, ok)
	assert.Equal(t, "x", e.Comm)

	_, err := sys.NewRealProc(filepath.Join(root, "missing")).Table()
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestRealProc_TableSkipsNonPidEntriesAndStripsDeletedExe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "5"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "9"), nil, 0o600), "a file named like a pid")
	require.NoError(t, os.Symlink("/opt/bin/app (deleted)", filepath.Join(root, "5", "exe")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "5", "cmdline"),
		[]byte("nginx: master process /opt/nginx -c /etc/n.conf\x00"), 0o600))

	tbl, err := sys.NewRealProc(root).Table()
	require.NoError(t, err)
	require.Len(t, tbl, 1)
	assert.Equal(t, "/opt/bin/app", tbl[0].Exe)
	assert.Equal(t, []string{"nginx:", "master", "process", "/opt/nginx", "-c", "/etc/n.conf"}, tbl[0].Argv)
}

func TestRealProc_ZombieParsesStatusState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(pid, status string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, pid), 0o755))

		if status != "" {
			require.NoError(t, os.WriteFile(filepath.Join(root, pid, "status"), []byte(status), 0o600))
		}
	}

	write("1", "Name:\tz\nState:\tZ (zombie)\n")
	write("2", "Name:\tZsh\nState:\tS (sleeping)\n")
	write("3", "Name:\tx\n")
	write("4", "")

	p := sys.NewRealProc(root)

	assert.True(t, p.Zombie(1))
	assert.False(t, p.Zombie(2), "a Z in the name is not the state")
	assert.False(t, p.Zombie(3), "no State line")
	assert.False(t, p.Zombie(4), "unreadable status")
	assert.False(t, p.Zombie(99))
}

func TestRealProc_UmaskFromStatusAndFallback(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "self"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "self", "status"), []byte("Name:\tx\nUmask:\t0027\n"), 0o600))

	assert.Equal(t, os.FileMode(0o027), sys.NewRealProc(root).Umask())

	// Unparseable or missing field: falls back to the real syscall.
	require.NoError(t, os.WriteFile(filepath.Join(root, "self", "status"), []byte("Name:\tx\n"), 0o600))

	want := sys.NewRealProc(sys.ProcRoot).Umask()
	assert.Equal(t, want, sys.NewRealProc(root).Umask())
	assert.Equal(t, want, sys.NewRealProc(filepath.Join(root, "missing")).Umask())
}

func TestRealProc_SignalLiveChildAndGroup(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), "/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())

	p := sys.NewRealProc(sys.ProcRoot)

	require.NoError(t, p.Signal(cmd.Process.Pid, 0), "signal 0 probes existence")
	require.NoError(t, p.SignalGroup(cmd.Process.Pid, syscall.SIGTERM))

	err := cmd.Wait()

	var xe *exec.ExitError

	require.ErrorAs(t, err, &xe)
	assert.Equal(t, syscall.SIGTERM, xe.Sys().(syscall.WaitStatus).Signal())

	require.ErrorIs(t, p.SignalGroup(1<<22+54321, syscall.SIGTERM), syscall.ESRCH)
}

func TestRealProc_LookupGID(t *testing.T) {
	t.Parallel()

	p := sys.NewRealProc(sys.ProcRoot)

	gid, err := p.LookupGID("root")
	if err == nil {
		assert.Equal(t, 0, gid)
	}

	_, err = p.LookupGID("no-such-group-configd-test")
	require.ErrorContains(t, err, "lookup group no-such-group-configd-test")
}

func TestRealClock_NowTimerTickerAfter(t *testing.T) {
	t.Parallel()

	var c sys.RealClock

	before := time.Now()
	assert.False(t, c.Now().Before(before))

	select {
	case <-c.After(time.Millisecond):
	case <-time.After(5 * time.Second):
		t.Fatal("After did not fire")
	}

	tm := c.NewTimer(time.Hour)
	assert.True(t, tm.Stop())
	assert.False(t, tm.Stop())

	fired := c.NewTimer(time.Millisecond)

	select {
	case <-fired.C():
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not fire")
	}

	tk := c.NewTicker(time.Millisecond)
	defer tk.Stop()

	select {
	case <-tk.C():
	case <-time.After(5 * time.Second):
		t.Fatal("ticker did not tick")
	}
}
