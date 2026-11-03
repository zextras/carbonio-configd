// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	amavisdPID  = 900
	amavisMCPID = 901
)

func amavisdEntry(pid int) sys.ProcEntry {
	return sys.ProcEntry{PID: pid, Argv: []string{"amavisd", "(master)"}}
}

func amavisMCEntry(ctl *Controller, pid int) sys.ProcEntry {
	return sys.ProcEntry{PID: pid, Argv: []string{"/usr/bin/perl", ctl.p.amavisMC}}
}

func execLines(f *systest.Fakes) string { return strings.Join(f.Exec.Lines(), "\n") }

func TestAmavisPreStart_CreatesSpamassassinAndRunsAltermime(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.altermime, []byte("#!/bin/sh"), 0o755)

	require.NoError(t, ctl.amavisPreStart(context.Background(), nil))

	assert.True(t, f.FS.Exists(ctl.p.amavisDir+"/.spamassassin"))
	assert.Contains(t, f.Exec.Lines(), ctl.p.altermime)
}

func TestAmavisPreStart_AltermimeSkippedWhenRewriteSkipped(t *testing.T) {
	t.Parallel()

	t.Run("context", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t)
		f.FS.Put(ctl.p.altermime, []byte("#!/bin/sh"), 0o755)

		require.NoError(t, ctl.amavisPreStart(WithNoRewriteContext(context.Background()), nil))

		assert.True(t, f.FS.Exists(ctl.p.amavisDir+"/.spamassassin"), "directory still prepared")
		assert.NotContains(t, execLines(f), ctl.p.altermime)
	})

	t.Run("controller option", func(t *testing.T) {
		t.Parallel()

		ctl, f := newFakeController(t, WithNoRewrite(true))
		f.FS.Put(ctl.p.altermime, []byte("#!/bin/sh"), 0o755)

		require.NoError(t, ctl.amavisPreStart(context.Background(), nil))

		assert.True(t, f.FS.Exists(ctl.p.amavisDir+"/.spamassassin"))
		assert.NotContains(t, execLines(f), ctl.p.altermime)
	})
}

func TestAmavisPreStart_AltermimeMissingOrFailingIsIgnored(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	require.NoError(t, ctl.amavisPreStart(context.Background(), nil), "script not installed")
	assert.NotContains(t, execLines(f), ctl.p.altermime)

	f.FS.Put(ctl.p.altermime, []byte("#!/bin/sh"), 0o755)
	f.Exec.OnPath(ctl.p.altermime, systest.Fail(1, "boom"))

	require.NoError(t, ctl.amavisPreStart(context.Background(), nil), "failure is ignored")
}

// installAmavis makes the daemons "appear" when launched: sudo amavis-mc
// registers an amavis-mc process, the amavisd binary registers amavisd.
func installAmavis(ctl *Controller, f *systest.Fakes) {
	f.FS.Put(ctl.p.common+"/sbin/amavisd", []byte("x"), 0o755)

	f.Exec.OnCmdline(sudoBin+" "+ctl.p.amavisMC, func(*sys.Cmd) (sys.Result, error) {
		f.Proc.Add(amavisMCEntry(ctl, amavisMCPID))

		return sys.Result{}, nil
	})
	f.Exec.OnPath(ctl.p.common+"/sbin/amavisd", func(*sys.Cmd) (sys.Result, error) {
		f.Proc.Add(amavisdEntry(amavisdPID))

		return sys.Result{}, nil
	})
}

func TestAmavisCustomStart_StartsMCViaSudoThenAmavisdAndPreparesDirs(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	installAmavis(ctl, f)

	// stale tmp from a previous run
	f.FS.PutDir(ctl.p.amavisDir+"/tmp", 0o755)
	f.FS.PutDir(ctl.p.amavisDir+"/tmp/amavis-old-1", 0o755)
	f.FS.Put(ctl.p.amavisDir+"/tmp/amavis-old-1/f", []byte("x"), 0o644)
	f.FS.PutDir(ctl.p.amavisDir+"/tmp/keep-me", 0o755)

	pumpClock(t, f.Clock, amavisReadyInterval)

	require.NoError(t, ctl.amavisCustomStart(context.Background(), ctl.registry[svcAmavis]))

	lines := f.Exec.Lines()
	mcIdx, dIdx := -1, -1

	for i, l := range lines {
		if strings.HasPrefix(l, sudoBin+" "+ctl.p.amavisMC) && mcIdx < 0 {
			mcIdx = i
		}

		if strings.HasPrefix(l, ctl.p.common+"/sbin/amavisd") && dIdx < 0 {
			dIdx = i
		}
	}

	require.GreaterOrEqual(t, mcIdx, 0, "amavis-mc started via sudo: %v", lines)
	require.GreaterOrEqual(t, dIdx, 0, "amavisd started: %v", lines)
	assert.Less(t, mcIdx, dIdx, "amavis-mc first")

	for _, d := range []string{"quarantine", "tmp", "var"} {
		assert.True(t, f.FS.Exists(ctl.p.amavisDir+"/"+d), d)
	}

	assert.False(t, f.FS.Exists(ctl.p.amavisDir+"/tmp/amavis-old-1"), "stale amavis-* removed")
	assert.True(t, f.FS.Exists(ctl.p.amavisDir+"/tmp/keep-me"), "other tmp content kept")
}

func TestAmavisCustomStart_SkipsWhenAmavisdRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	installAmavis(ctl, f)
	f.Proc.Add(amavisdEntry(amavisdPID))
	f.Proc.Add(amavisMCEntry(ctl, amavisMCPID))
	f.FS.PutDir(ctl.p.amavisDir+"/tmp", 0o755)
	f.FS.PutDir(ctl.p.amavisDir+"/tmp/amavis-live", 0o755)

	require.NoError(t, ctl.amavisCustomStart(context.Background(), ctl.registry[svcAmavis]))

	lines := execLines(f)
	assert.NotContains(t, lines, sudoBin, "amavis-mc already running: no sudo")
	assert.NotContains(t, lines, ctl.p.common+"/sbin/amavisd", "amavisd already running: not started again")
	assert.True(t, f.FS.Exists(ctl.p.amavisDir+"/tmp/amavis-live"), "running instance's tmp untouched")
}

func TestAmavisCustomStart_StartsMCEvenIfAmavisdRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	installAmavis(ctl, f)
	f.Proc.Add(amavisdEntry(amavisdPID))

	pumpClock(t, f.Clock, amavisReadyInterval)

	require.NoError(t, ctl.amavisCustomStart(context.Background(), ctl.registry[svcAmavis]))

	assert.Contains(t, execLines(f), sudoBin+" "+ctl.p.amavisMC)
	assert.True(t, f.Proc.Alive(amavisMCPID))
}

func TestAmavisCustomStart_NotInstalledIsNoOp(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	require.NoError(t, ctl.amavisCustomStart(context.Background(), ctl.registry[svcAmavis]))

	assert.Empty(t, f.Exec.Lines())
}

func TestAmavisCustomStart_MCNeverAppears(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.common+"/sbin/amavisd", []byte("x"), 0o755) // sudo succeeds, but no process shows up

	pumpClock(t, f.Clock, amavisReadyInterval)

	err := ctl.amavisCustomStart(context.Background(), ctl.registry[svcAmavis])

	require.Error(t, err)
	assert.Contains(t, err.Error(), "amavis-mc did not start")
}

func TestAmavisCustomStart_AmavisdNeverAppears(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put(ctl.p.common+"/sbin/amavisd", []byte("x"), 0o755)
	f.Proc.Add(amavisMCEntry(ctl, amavisMCPID))

	pumpClock(t, f.Clock, amavisReadyInterval)

	err := ctl.amavisCustomStart(context.Background(), ctl.registry[svcAmavis])

	require.Error(t, err)
	assert.Contains(t, err.Error(), "amavisd did not start")
}

func TestAmavisCustomStop_KillsAmavisdThenMC(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Proc.Add(amavisdEntry(amavisdPID))
	f.Proc.Add(amavisMCEntry(ctl, amavisMCPID))

	require.NoError(t, ctl.amavisCustomStop(context.Background(), ctl.registry[svcAmavis]))

	sigs := f.Proc.Signals()
	require.Len(t, sigs, 2)
	assert.Equal(t, amavisdPID, sigs[0].PID)
	assert.Equal(t, amavisMCPID, sigs[1].PID)
	assert.Equal(t, syscall.SIGTERM, sigs[0].Sig)
	assert.Equal(t, syscall.SIGTERM, sigs[1].Sig)
	assert.False(t, f.Proc.Alive(amavisdPID))
	assert.False(t, f.Proc.Alive(amavisMCPID))
}

func TestAmavisCustomStop_NothingRunningIsNil(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	require.NoError(t, ctl.amavisCustomStop(context.Background(), ctl.registry[svcAmavis]))
	assert.Empty(t, f.Proc.Signals())
}

func TestCompanionsRunning(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	def := ctl.registry[svcAmavis]

	assert.False(t, ctl.companionsRunning(context.Background(), def))

	f.Proc.Add(amavisMCEntry(ctl, amavisMCPID))
	assert.True(t, ctl.companionsRunning(context.Background(), def))

	assert.True(t, ctl.companionsRunning(context.Background(), &ServiceDef{}), "no companions: vacuously true")
}

func TestServiceStatus_AmavisNeedsAmavisMC(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctx := context.Background()

	running, err := ctl.ServiceStatus(ctx, svcAmavis)
	require.NoError(t, err)
	assert.False(t, running, "nothing running")

	f.Proc.Add(amavisdEntry(amavisdPID))

	running, err = ctl.ServiceStatus(ctx, svcAmavis)
	require.NoError(t, err)
	assert.False(t, running, "amavisd alone is not running: amavis-mc is absent")

	f.Proc.Add(amavisMCEntry(ctl, amavisMCPID))

	running, err = ctl.ServiceStatus(ctx, svcAmavis)
	require.NoError(t, err)
	assert.True(t, running, "both up")

	f.Proc.Remove(amavisdPID)

	running, err = ctl.ServiceStatus(ctx, svcAmavis)
	require.NoError(t, err)
	assert.False(t, running, "amavis-mc alone is not running either")
}
