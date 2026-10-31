// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systest_test

import (
	"bytes"
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestExec_LaterRulesWinAndDefaultSucceeds(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("/bin/x", systest.Reply("first"))
	f.Exec.OnCmdline("/bin/x -v", systest.Reply("second"))

	out, err := sys.Output(t.Context(), f.Exec, "/bin/x", "-v")
	require.NoError(t, err)
	assert.Equal(t, "second", string(out))

	out, err = sys.Output(t.Context(), f.Exec, "/bin/x")
	require.NoError(t, err)
	assert.Equal(t, "first", string(out))

	res, err := f.Exec.Run(t.Context(), &sys.Cmd{Path: "/bin/unmatched"})
	require.NoError(t, err)
	assert.Empty(t, res.Stdout)

	assert.Equal(t, []string{"/bin/x -v", "/bin/x", "/bin/unmatched"}, f.Exec.Lines())
	assert.Len(t, f.Exec.Calls(), 3)
	assert.Equal(t, []string{"/bin/x -v", "/bin/x", "/bin/unmatched"}, f.Rec.Of("exec"))
}

func TestExec_RunRoutesOutputToWriters(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("/bin/w", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("o"), Stderr: []byte("e")}, nil
	})

	var so, se bytes.Buffer

	res, err := f.Exec.Run(t.Context(), &sys.Cmd{Path: "/bin/w", Stdout: &so, Stderr: &se})
	require.NoError(t, err)
	assert.Equal(t, "o", so.String())
	assert.Equal(t, "e", se.String())
	assert.Empty(t, res.Stdout, "output given to a writer is not captured")
	assert.Empty(t, res.Stderr)

	res, err = f.Exec.Run(t.Context(), &sys.Cmd{Path: "/bin/w"})
	require.NoError(t, err)
	assert.Equal(t, "o", string(res.Stdout))
	assert.Equal(t, "e", string(res.Stderr))

	var comb bytes.Buffer

	res, err = f.Exec.Run(t.Context(), &sys.Cmd{Path: "/bin/w", Stdout: &comb, Combined: true})
	require.NoError(t, err)
	assert.Equal(t, "o", string(res.Stdout), "Combined keeps stdout captured")
	assert.Empty(t, comb.String())
}

func TestExec_FailStartAndCancelledContext(t *testing.T) {
	t.Parallel()

	spawn := errors.New("exec format error")
	f := systest.New()
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == "/bin/a" }, spawn)
	f.Exec.FailStart(func(c *sys.Cmd) bool { return c.Path == "/bin/b" }, syscall.EACCES)

	res, err := f.Exec.Run(t.Context(), &sys.Cmd{Path: "/bin/a"})
	require.ErrorIs(t, err, spawn)
	assert.Equal(t, -1, res.ExitCode)

	_, err = f.Exec.Start(t.Context(), &sys.Cmd{Path: "/bin/b"})
	require.ErrorIs(t, err, syscall.EACCES, "chained matchers all stay active")

	_, err = f.Exec.Run(t.Context(), &sys.Cmd{Path: "/bin/c"})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err = f.Exec.Run(ctx, &sys.Cmd{Path: "/bin/c"})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, -1, res.ExitCode)
}

func TestExec_StartedProcesses(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Exec.OnPath("/bin/d", func(c *sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte("log")}, nil
	})
	f.Exec.OnPath("/bin/bad", systest.Fail(4, "x"))

	var out bytes.Buffer

	p, err := f.Exec.Start(t.Context(), &sys.Cmd{Path: "/bin/d", Stdout: &out})
	require.NoError(t, err)
	require.NoError(t, p.Wait())
	assert.Equal(t, "log", out.String())

	q, err := f.Exec.Start(t.Context(), &sys.Cmd{Path: "/bin/bad"})
	require.NoError(t, err)
	assert.Equal(t, p.Pid()+1, q.Pid(), "pids are allocated sequentially")

	var ee *sys.ExitError

	require.ErrorAs(t, q.Wait(), &ee)
	assert.Equal(t, 4, ee.Code)

	require.NoError(t, q.Release())
	assert.Equal(t, []string{"1002"}, f.Rec.Of("exec.release"))

	started := f.Exec.Started(q.Pid())
	require.NotNil(t, started)
	assert.Equal(t, "/bin/bad", started.Cmd().Path)
	assert.Nil(t, f.Exec.Started(1))
}

func TestProc_TableAndEntry(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.Add(sys.ProcEntry{PID: 9, Comm: "z"})
	f.Proc.Add(sys.ProcEntry{PID: 3, Comm: "a"})

	tbl, err := f.Proc.Table()
	require.NoError(t, err)
	require.Len(t, tbl, 2)
	assert.Equal(t, 3, tbl[0].PID, "table is in pid order")

	e, ok := f.Proc.Entry(9)
	require.True(t, ok)
	assert.Equal(t, "z", e.Comm)

	f.Proc.Remove(9)

	_, ok = f.Proc.Entry(9)
	assert.False(t, ok)
}

func TestProc_ZombieIsPresentButNotAlive(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.Add(sys.ProcEntry{PID: 4})
	f.Proc.SetZombie(4, true)

	assert.True(t, f.Proc.Zombie(4))
	assert.False(t, f.Proc.Alive(4))

	_, ok := f.Proc.Entry(4)
	assert.True(t, ok, "a zombie keeps its table entry")

	f.Proc.SetZombie(4, false)
	assert.True(t, f.Proc.Alive(4))

	f.Proc.SetZombie(4, true)
	f.Proc.Remove(4)
	assert.False(t, f.Proc.Zombie(4), "Remove clears the zombie mark")
	assert.False(t, f.Proc.Alive(4))
}

func TestProc_SignalDelivery(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.Add(sys.ProcEntry{PID: 5})
	f.Proc.Add(sys.ProcEntry{PID: 6})

	require.NoError(t, f.Proc.Signal(5, syscall.SIGHUP))
	assert.True(t, f.Proc.Alive(5), "SIGHUP does not remove the process")

	require.NoError(t, f.Proc.SignalGroup(6, syscall.SIGTERM))
	assert.False(t, f.Proc.Alive(6))

	require.ErrorIs(t, f.Proc.SignalGroup(6, syscall.SIGTERM), syscall.ESRCH)

	assert.Equal(t, []systest.Signal{
		{PID: 5, Sig: syscall.SIGHUP},
		{PID: 6, Sig: syscall.SIGTERM, Group: true},
		{PID: 6, Sig: syscall.SIGTERM, Group: true},
	}, f.Proc.Signals())
	assert.Equal(t, []string{"5 1", "6 15", "6 15"}, f.Rec.Of("proc.signal"),
		"signals and group signals share the proc.signal prefix")
	assert.Equal(t, []string{"6 15", "6 15"}, f.Rec.Of("proc.signal-group"))
}

func TestProc_SignalErrHookWinsOverTable(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.Proc.Add(sys.ProcEntry{PID: 5})
	f.Proc.SignalErr = func(pid int, _ syscall.Signal, group bool) error {
		if pid == 5 && !group {
			return syscall.EPERM
		}

		return nil
	}

	require.ErrorIs(t, f.Proc.Signal(5, syscall.SIGKILL), syscall.EPERM)
	assert.True(t, f.Proc.Alive(5), "an undelivered signal leaves the process alone")
	require.NoError(t, f.Proc.SignalGroup(5, syscall.SIGKILL))
	assert.False(t, f.Proc.Alive(5))
}

func TestProc_IdentityAndGroups(t *testing.T) {
	t.Parallel()

	f := systest.New()

	assert.Equal(t, 100, f.Proc.Pid())
	assert.Equal(t, 99, f.Proc.Ppid())
	assert.Equal(t, 1000, f.Proc.UID())
	assert.Equal(t, 0o027, int(f.Proc.Umask()))

	f.Proc.SetGID("zextras", 998)

	gid, err := f.Proc.LookupGID("zextras")
	require.NoError(t, err)
	assert.Equal(t, 998, gid)

	_, err = f.Proc.LookupGID("nogroup")
	require.ErrorContains(t, err, "unknown group nogroup")
}

func TestRecorder_OfResetAndOps(t *testing.T) {
	t.Parallel()

	r := &systest.Recorder{}
	r.Recordf("fs.x", "%d", 1)
	r.Record("fs.y", "b")
	r.Record("exec", "c")

	assert.Equal(t, []string{"1", "b"}, r.Of("fs."))
	assert.Equal(t, "exec c", r.Ops()[2].String())

	ops := r.Ops()
	ops[0].Detail = "mutated"
	assert.Equal(t, "fs.x 1", r.Strings()[0], "Ops returns a copy")

	r.Reset()
	assert.Empty(t, r.Ops())
}

func TestClock_TimerStopAndTickerDrops(t *testing.T) {
	t.Parallel()

	c := systest.New().Clock

	tm := c.NewTimer(time.Second)
	assert.True(t, tm.Stop(), "stopping an armed timer reports true")
	assert.False(t, tm.Stop(), "a second Stop reports false")
	assert.Zero(t, c.Waiters())

	c.Advance(time.Hour)

	select {
	case <-tm.C():
		t.Fatal("stopped timer fired")
	default:
	}

	fired := c.NewTimer(time.Second)
	c.Advance(time.Second)
	<-fired.C()
	assert.False(t, fired.Stop(), "a fired timer is no longer active")

	tk := c.NewTicker(time.Second)
	c.Advance(3 * time.Second)
	assert.Len(t, tk.C(), 1, "ticks are dropped while the channel is full")
	<-tk.C()
	assert.Equal(t, systest.Epoch.Add(time.Hour+4*time.Second), c.Now())
	tk.Stop()
}

func TestClock_AdvanceFiresTimerAtItsOwnInstant(t *testing.T) {
	t.Parallel()

	c := systest.New().Clock
	tm := c.NewTimer(5 * time.Second)

	c.Advance(time.Minute)

	assert.Equal(t, systest.Epoch.Add(5*time.Second), <-tm.C(), "fires with its due time, not the target")
	assert.Equal(t, systest.Epoch.Add(time.Minute), c.Now())
}
