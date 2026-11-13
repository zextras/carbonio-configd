// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

const statsPidFile = "/run/carbonio/stats/zmstat-proc.pid"

// TestKillStatsPidFile_RefusesNonPositivePID: a pidfile holding 0, a negative
// number or 1 must never reach kill(-pid): kill(0) would signal configd's own
// process group and kill(-1) every process it may signal.
func TestKillStatsPidFile_RefusesNonPositivePID(t *testing.T) {
	t.Parallel()

	for _, content := range []string{"0\n", "-1\n", "-4242"} {
		t.Run(content, func(t *testing.T) {
			t.Parallel()

			ctl, f := newFakeController(t)
			f.FS.Put(statsPidFile, []byte(content), 0o644)

			assert.False(t, ctl.killStatsPidFile(context.Background(), statsPidFile, ctl.registry[svcStats].ProcessName))
			assert.Empty(t, f.Proc.Signals(), "nothing may be signaled for pid %q", content)
			assert.False(t, f.FS.Exists(statsPidFile), "the invalid pidfile is removed")
		})
	}
}

// TestKillStatsPidFile_StalePIDOfUnrelatedProcessIsNotKilled: the pid in the
// file was recycled by an unrelated process; its process group must be left
// alone and the stale file removed.
func TestKillStatsPidFile_StalePIDOfUnrelatedProcessIsNotKilled(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(statsPidFile, []byte("4242\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 4242, Argv: []string{"/usr/bin/vim", "notes.txt"}})

	assert.True(t, ctl.killStatsPidFile(context.Background(), statsPidFile, ctl.registry[svcStats].ProcessName))
	assert.Empty(t, f.Proc.Signals())
	assert.False(t, f.FS.Exists(statsPidFile))
	assert.True(t, f.Proc.Alive(4242), "the unrelated process survives")
}

// TestKillStatsPidFile_KillsCollectorGroup: a genuine collector gets its whole
// process group terminated.
func TestKillStatsPidFile_KillsCollectorGroup(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(statsPidFile, []byte("4242\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 4242, Argv: []string{"/usr/bin/perl", ctl.p.libexec + "/zmstat-proc"}})

	require.True(t, ctl.killStatsPidFile(context.Background(), statsPidFile, ctl.registry[svcStats].ProcessName))
	require.NotEmpty(t, f.Proc.Signals())
	assert.Equal(t, syscall.SIGTERM, f.Proc.Signals()[0].Sig)
	assert.True(t, f.Proc.Signals()[0].Group, "the group is signaled, not just the leader")
	assert.False(t, f.FS.Exists(statsPidFile))
}

// TestStartCollectors_RunsOneProcess: the collectors are one process, "setup
// stat run" with the command line of each collector as an argument and the log
// in zmstat.out; the optional ones only when their localconfig key is TRUE
// (statctl.pl, zmstatctl start).
func TestStartCollectors_RunsOneProcess(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{"zmstat_mysql_enabled": "TRUE", "zmstat_nginx_enabled": "FALSE"}, nil
	}))
	f.FS.Put(ctl.p.libexec+"/setup", []byte("x"), 0o755)
	f.FS.PutDir(ctl.p.log, 0o755)

	require.NoError(t, ctl.startCollectors(context.Background(), ctl.registry[svcStats]))

	calls := f.Exec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, ctl.p.libexec+"/setup", calls[0].Path)
	assert.Equal(t, []string{
		"stat", "run", "proc", "cpu", "vm", "io -x", "df", "io", "fd", "allprocs", "mysql",
	}, calls[0].Args)
	assert.True(t, calls[0].Setsid, "its own session: stop signals its process group")
	assert.True(t, f.FS.Exists(ctl.p.log+"/zmstat.out"))
}

// TestStartCollectors_EveryOptionalCollector: all three keys TRUE add the
// optional collectors after the base ones, in their order.
func TestStartCollectors_EveryOptionalCollector(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{
			"zmstat_mysql_enabled": "TRUE", "zmstat_nginx_enabled": "TRUE", "zmstat_mtaqueue_enabled": "TRUE",
		}, nil
	}))
	f.FS.Put(ctl.p.libexec+"/setup", []byte("x"), 0o755)
	f.FS.PutDir(ctl.p.log, 0o755)

	require.NoError(t, ctl.startCollectors(context.Background(), ctl.registry[svcStats]))

	require.Len(t, f.Exec.Calls(), 1)
	assert.Equal(t, []string{"mysql", "nginx", "mtaqueue"}, f.Exec.Calls()[0].Args[len(f.Exec.Calls()[0].Args)-3:])
}

// TestStartCollectors_NoSetupBinary: without libexec/setup nothing starts.
func TestStartCollectors_NoSetupBinary(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.PutDir(ctl.p.log, 0o755)

	require.ErrorContains(t, ctl.startCollectors(context.Background(), ctl.registry[svcStats]),
		"no stats collectors started")
	assert.Empty(t, f.Exec.Calls())
}

// TestStartCollectors_ProcessDoesNotStart: a process that cannot be started is
// an error of the start.
func TestStartCollectors_ProcessDoesNotStart(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.libexec+"/setup", []byte("x"), 0o755)
	f.FS.PutDir(ctl.p.log, 0o755)
	f.Exec.FailStart(func(*sys.Cmd) bool { return true }, errors.New("exec format error"))

	require.ErrorContains(t, ctl.startCollectors(context.Background(), ctl.registry[svcStats]),
		"no stats collectors started: exec format error")
}

// TestStopCollectors_OneProcessManyPidfiles: the pidfiles of the collectors all
// name the one process; it is killed once and every pidfile is removed.
func TestStopCollectors_OneProcessManyPidfiles(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.PutDir(ctl.p.statsPidDir, 0o755)
	f.FS.Put(ctl.p.statsPidDir+"/cpu.pid", []byte("4242\n"), 0o644)
	f.FS.Put(ctl.p.statsPidDir+"/io-x.pid", []byte("4242\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 4242, Argv: []string{ctl.p.libexec + "/setup", "stat", "run", "cpu", "io -x"}})

	require.NoError(t, ctl.stopCollectors(context.Background(), ctl.registry[svcStats]))

	assert.False(t, f.Proc.Alive(4242))
	assert.False(t, f.FS.Exists(ctl.p.statsPidDir+"/cpu.pid"))
	assert.False(t, f.FS.Exists(ctl.p.statsPidDir+"/io-x.pid"))
}

// TestKillStatsPidFile_KillsSetupCollectorGroup: a collector of this release
// ("setup stat <name>") is recognised and its group terminated.
func TestKillStatsPidFile_KillsSetupCollectorGroup(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(statsPidFile, []byte("4242\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 4242, Argv: []string{ctl.p.libexec + "/setup", "stat", "proc"}})

	require.True(t, ctl.killStatsPidFile(context.Background(), statsPidFile, ctl.registry[svcStats].ProcessName))
	require.NotEmpty(t, f.Proc.Signals())
	assert.True(t, f.Proc.Signals()[0].Group)
	assert.False(t, f.FS.Exists(statsPidFile))
}

// TestKillStatsPidFile_OtherSetupCommandIsNotACollector: "setup proxyconfig"
// is not a zmstat collector, so a recycled pid of it is left alone.
func TestKillStatsPidFile_OtherSetupCommandIsNotACollector(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(statsPidFile, []byte("4242\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 4242, Argv: []string{ctl.p.libexec + "/setup", "proxyconfig"}})

	ctl.killStatsPidFile(context.Background(), statsPidFile, ctl.registry[svcStats].ProcessName)
	assert.Empty(t, f.Proc.Signals())
}
