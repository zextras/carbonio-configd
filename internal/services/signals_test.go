// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

func TestServiceRefresh_MilterSendsSIGCONT(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Proc.Add(sys.ProcEntry{PID: 700, Argv: []string{"java", "-cp", "x", milterMainClass}})

	report, err := ctl.ServiceRefresh(context.Background(), svcMilter)

	require.NoError(t, err)
	assert.Equal(t, []int{700}, report.Signaled)
	assert.Empty(t, report.NotRunning)
	require.Len(t, f.Proc.Signals(), 1)
	assert.Equal(t, 700, f.Proc.Signals()[0].PID)
	assert.Equal(t, syscall.SIGCONT, f.Proc.Signals()[0].Sig)
	assert.True(t, f.Proc.Alive(700), "SIGCONT does not terminate")
}

func TestServiceRefresh_StoppedIsEmptyReport(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	report, err := ctl.ServiceRefresh(context.Background(), svcMilter)

	require.NoError(t, err)
	assert.Empty(t, report.Signaled)
	assert.Empty(t, report.NotRunning)
	assert.Empty(t, f.Proc.Signals())
}

func TestServiceRefresh_UnsupportedVerb(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	_, err := ctl.ServiceRefresh(context.Background(), "mta")

	require.ErrorIs(t, err, ErrUnsupportedVerb)
	assert.Empty(t, f.Proc.Signals())
}

func TestServiceRefresh_UnknownService(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	_, err := ctl.ServiceRefresh(context.Background(), "no-such-service")

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnsupportedVerb)
}

func TestServiceRotate_UnsupportedVerbAndUnknown(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	_, err := ctl.ServiceRotate(context.Background(), "mta")
	require.ErrorIs(t, err, ErrUnsupportedVerb)

	_, err = ctl.ServiceRotate(context.Background(), "no-such-service")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnsupportedVerb)
}

func TestServiceRotate_StatsSignalsEveryPidfileWithSIGHUP(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.PutDir(ctl.p.statsPidDir, 0o755)
	f.FS.Put(ctl.p.statsPidDir+"/zmstat-cpu.pid", []byte("801\n"), 0o644)
	f.FS.Put(ctl.p.statsPidDir+"/zmstat-vm.pid", []byte("802"), 0o644)
	f.FS.Put(ctl.p.statsPidDir+"/notes.txt", []byte("803"), 0o644) // not a pidfile
	f.Proc.Add(sys.ProcEntry{PID: 801, Argv: []string{"/usr/bin/perl", "zmstat-cpu"}})
	f.Proc.Add(sys.ProcEntry{PID: 802, Argv: []string{"/usr/bin/perl", "zmstat-vm"}})
	f.Proc.Add(sys.ProcEntry{PID: 803, Argv: []string{"other"}})

	report, err := ctl.ServiceRotate(context.Background(), "stats")

	require.NoError(t, err)
	assert.ElementsMatch(t, []int{801, 802}, report.Signaled)
	assert.Empty(t, report.NotRunning)

	got := map[int]syscall.Signal{}
	for _, s := range f.Proc.Signals() {
		got[s.PID] = s.Sig
	}

	assert.Equal(t, map[int]syscall.Signal{801: syscall.SIGHUP, 802: syscall.SIGHUP}, got)
}

// TestServiceRotate_StatsSignalsOneProcessOnce: the pidfiles of the collectors
// of one process all name it, and it reopens its logs once.
func TestServiceRotate_StatsSignalsOneProcessOnce(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.PutDir(ctl.p.statsPidDir, 0o755)
	f.FS.Put(ctl.p.statsPidDir+"/cpu.pid", []byte("801\n"), 0o644)
	f.FS.Put(ctl.p.statsPidDir+"/vm.pid", []byte("801\n"), 0o644)
	f.FS.Put(ctl.p.statsPidDir+"/df.pid", []byte("801\n"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 801, Argv: []string{ctl.p.bin + "/configd", "stat", "run", "cpu", "vm", "df"}})

	report, err := ctl.ServiceRotate(context.Background(), "stats")

	require.NoError(t, err)
	assert.Equal(t, []int{801}, report.Signaled)
	require.Len(t, f.Proc.Signals(), 1)
	assert.Equal(t, syscall.SIGHUP, f.Proc.Signals()[0].Sig)
}

func TestServiceRotate_StatsReportsNotRunningPids(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.PutDir(ctl.p.statsPidDir, 0o755)
	f.FS.Put(ctl.p.statsPidDir+"/live.pid", []byte("801"), 0o644)
	f.FS.Put(ctl.p.statsPidDir+"/dead.pid", []byte("999"), 0o644)
	f.Proc.Add(sys.ProcEntry{PID: 801, Argv: []string{"zmstat-cpu"}})

	report, err := ctl.ServiceRotate(context.Background(), "stats")

	require.NoError(t, err)
	assert.Equal(t, []int{801}, report.Signaled)
	assert.Equal(t, []int{999}, report.NotRunning)
}

func TestServiceRotate_StatsNoPidFiles(t *testing.T) {
	t.Parallel()

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t)

		_, err := ctl.ServiceRotate(context.Background(), "stats")

		require.ErrorIs(t, err, ErrNoStatsPidFiles)
		assert.Empty(t, f.Proc.Signals())
	})

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t)
		f.FS.PutDir(ctl.p.statsPidDir, 0o755)

		_, err := ctl.ServiceRotate(context.Background(), "stats")

		require.ErrorIs(t, err, ErrNoStatsPidFiles)
		assert.Empty(t, f.Proc.Signals())
	})

	t.Run("only invalid pidfiles", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t)
		f.FS.PutDir(ctl.p.statsPidDir, 0o755)
		f.FS.Put(ctl.p.statsPidDir+"/bad.pid", []byte("not-a-pid"), 0o644)

		_, err := ctl.ServiceRotate(context.Background(), "stats")

		require.ErrorIs(t, err, ErrNoStatsPidFiles)
	})
}
