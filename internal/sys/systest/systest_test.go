// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systest_test

import (
	"context"
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestRecorder_OrdersOpsAcrossFakes(t *testing.T) {
	t.Parallel()

	f := systest.New()
	s := f.System()

	require.NoError(t, s.FS.WriteFileAtomic("/etc/a", []byte("1"), 0o640))
	_, err := sys.Output(t.Context(), s.Exec, "/bin/x", "-y")
	require.NoError(t, err)
	require.ErrorIs(t, s.Proc.Signal(7, syscall.SIGTERM), syscall.ESRCH) // no such pid in the table

	f.Rec.Record("ldap", "set a=b")

	assert.Equal(t, []string{
		"fs.write-atomic /etc/a mode=0640",
		"exec /bin/x -y",
		"proc.signal 7 15",
		"ldap set a=b",
	}, f.Rec.Strings())
}

func TestExec_ScriptedOutcomes(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnCmdline("/bin/pc", systest.Fail(2, "boom"))
	f.Exec.OnPath("/bin/ok", systest.Reply("hi"))

	out, err := sys.CombinedOutput(t.Context(), f.Exec, "/bin/pc", "-e")

	var ee *sys.ExitError

	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.Code)
	assert.Equal(t, "boom", string(out))

	out, err = sys.Output(t.Context(), f.Exec, "/bin/ok")
	require.NoError(t, err)
	assert.Equal(t, "hi", string(out))
}

func TestFS_AtomicWriteTempRenameAndLinks(t *testing.T) {
	t.Parallel()

	f := systest.New()
	fsys := f.FS

	tmp, err := fsys.CreateTemp("/tmp", "rw-*")
	require.NoError(t, err)
	_, err = tmp.WriteString("hello")
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	require.NoError(t, fsys.Chmod(tmp.Name(), 0o440))
	require.NoError(t, fsys.Rename(tmp.Name(), "/etc/dest"))

	data, ok := fsys.Data("/etc/dest")
	require.True(t, ok)
	assert.Equal(t, "hello", string(data))

	mode, _ := fsys.Mode("/etc/dest")
	assert.Equal(t, fs.FileMode(0o440), mode)

	require.NoError(t, fsys.Symlink("/etc/dest", "/etc/link"))

	st, err := fsys.Lstat("/etc/link")
	require.NoError(t, err)
	assert.NotZero(t, st.Mode()&fs.ModeSymlink)

	data, err = fsys.ReadFile("/etc/link")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	_, err = fsys.ReadFile("/nope")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.True(t, os.IsNotExist(err))
}

func TestProc_SignalRemovesTargetUnlessSurvivor(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.Add(sys.ProcEntry{PID: 5, Argv: []string{"/usr/bin/a"}})
	f.Proc.Add(sys.ProcEntry{PID: 6, Argv: []string{"/usr/bin/b"}})
	f.Proc.SurviveTerm[6] = true

	require.NoError(t, f.Proc.Signal(5, syscall.SIGTERM))
	require.NoError(t, f.Proc.Signal(6, syscall.SIGTERM))

	assert.False(t, f.Proc.Alive(5))
	assert.True(t, f.Proc.Alive(6))

	require.NoError(t, f.Proc.Signal(6, syscall.SIGKILL))
	assert.False(t, f.Proc.Alive(6))
	assert.ErrorIs(t, f.Proc.Signal(6, syscall.SIGTERM), syscall.ESRCH)
}

func TestClock_AdvanceFiresInOrder(t *testing.T) {
	t.Parallel()

	f := systest.New()
	c := f.Clock

	tick := c.NewTicker(10 * time.Second)
	after := c.After(25 * time.Second)

	c.Advance(10 * time.Second)
	<-tick.C()
	assert.Equal(t, systest.Epoch.Add(10*time.Second), c.Now())

	select {
	case <-after:
		t.Fatal("After fired early")
	default:
	}

	c.Advance(15 * time.Second)
	<-after
	<-tick.C()

	tick.Stop()
	assert.Zero(t, c.Waiters())
}

func TestClock_SleepReleasedByAdvanceOrContext(t *testing.T) {
	t.Parallel()

	c := systest.New().Clock
	done := make(chan error, 1)

	go func() { done <- c.Sleep(context.Background(), time.Minute) }()

	require.Eventually(t, func() bool { return c.Waiters() == 1 }, time.Second, time.Millisecond)
	c.Advance(time.Minute)
	require.NoError(t, <-done)

	ctx, cancel := context.WithCancel(t.Context())
	go func() { done <- c.Sleep(ctx, time.Hour) }()

	require.Eventually(t, func() bool { return c.Waiters() == 1 }, time.Second, time.Millisecond)
	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}
