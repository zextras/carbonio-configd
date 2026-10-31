// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// fakeProcFS points the Controller's process table at a temp dir and returns a writer that adds a
// /proc/<pid> entry (state "S", or "Z" for zombies).
func fakeProcFS(t *testing.T, ctl *Controller) (root string, add func(pid int, cmdline, comm string, zombie bool)) {
	t.Helper()

	root = t.TempDir()
	ctl.useProcRoot(root + "/")

	add = func(pid int, cmdline, comm string, zombie bool) {
		t.Helper()

		dir := filepath.Join(root, strconv.Itoa(pid))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))

		state := "S (sleeping)"
		if zombie {
			state = "Z (zombie)"
		}

		require.NoError(t, os.WriteFile(filepath.Join(dir, "status"),
			[]byte(fmt.Sprintf("Name:\t%s\nState:\t%s\n", comm, state)), 0o644))
	}

	return root, add
}

func TestScanProcesses_UsesProcFSRoot(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	_, add := fakeProcFS(t, ctl)
	add(4001, "/opt/zextras/common/sbin/opendkim\x00-x\x00/etc/opendkim.conf\x00", "opendkim", false)
	add(4002, "/usr/sbin/other\x00", "other", false)

	pids, err := ctl.scanProcesses("opendkim")
	require.NoError(t, err)
	assert.Equal(t, []int{4001}, pids)
}

func TestScanProcesses_MissingRootErrors(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	root, _ := fakeProcFS(t, ctl)
	require.NoError(t, os.Remove(root))

	_, err := ctl.scanProcesses("anything")
	require.Error(t, err)
}

// TestProcessSnapshot_MatchesLikeFreshScan pins that a snapshot yields the
// same verdicts as a fresh scan for every needle, including the zombie skip,
// the cmdline-authoritative rule and the exact-comm fallback.
func TestProcessSnapshot_MatchesLikeFreshScan(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	_, add := fakeProcFS(t, ctl)
	add(5001, "/usr/bin/memcached\x00-p\x0011211\x00", "memcached", false)
	add(5002, "", "nginx", false)                          // argv zeroed: exact comm fallback
	add(5003, "nginx: worker process\x00", "nginx", false) // cmdline authoritative
	add(5004, "/usr/bin/memcached\x00", "memcached", true) // zombie
	add(5005, "/bin/bash\x00/usr/bin/service-discover-wrapper.sh\x00", "service-discove", false)

	needles := []string{"memcached", "nginx", "nginx-worker", "service-discovered", "-p 11211", "absent"}
	snap := &procSnapshot{proc: ctl.env.Proc}

	for _, needle := range needles {
		t.Run(needle, func(t *testing.T) {
			want, err := ctl.scanProcesses(needle)
			require.NoError(t, err)

			got, err := snap.scan(needle)
			require.NoError(t, err)
			assert.ElementsMatch(t, want, got)
		})
	}

	got, _ := snap.scan("memcached")
	assert.Equal(t, []int{5001}, got, "zombie 5004 must be skipped")

	got, _ = snap.scan("nginx")
	assert.ElementsMatch(t, []int{5002, 5003}, got)
}

func TestIsProcessRunning_SnapshotReusedAcrossProbes(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	root, add := fakeProcFS(t, ctl)
	add(6001, "/usr/bin/memcached\x00", "memcached", false)

	ctx := ctl.WithProcessSnapshot(context.Background())
	assert.True(t, ctl.pidFromProcessName(ctx, "memcached") > 0)

	// The snapshot is frozen: a process appearing or vanishing afterwards is
	// only seen by a fresh scan.
	add(6002, "/usr/sbin/opendkim\x00", "opendkim", false)
	require.NoError(t, os.RemoveAll(filepath.Join(root, "6001")))

	assert.True(t, ctl.pidFromProcessName(ctx, "memcached") > 0, "snapshot must not be re-read")
	assert.Zero(t, ctl.pidFromProcessName(ctx, "opendkim"), "snapshot must not be re-read")

	assert.Zero(t, ctl.pidFromProcessName(context.Background(), "memcached"), "plain ctx takes a fresh listing")
	assert.True(t, ctl.pidFromProcessName(context.Background(), "opendkim") > 0, "plain ctx takes a fresh listing")
}

func TestWithProcessSnapshot_Idempotent(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctx := ctl.WithProcessSnapshot(context.Background())
	assert.Same(t, ctx, ctl.WithProcessSnapshot(ctx))
}

func TestIsProcessRunning_ExcludesSelfWithSnapshot(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	_, add := fakeProcFS(t, ctl)
	add(os.Getpid(), "configd\x00status\x00", "configd", false)

	ctx := ctl.WithProcessSnapshot(context.Background())
	assert.Equal(t, 0, ctl.pidFromProcessName(ctx, "configd"))
	assert.Equal(t, 0, ctl.pidFromProcessName(context.Background(), "configd"))
}

// TestKillProcess_IgnoresSnapshot pins that signaling always sees live state:
// scanProcesses (used by killProcess) never consults a snapshot.
func TestKillProcess_IgnoresSnapshot(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	root, add := fakeProcFS(t, ctl)
	add(7001, "/usr/bin/target\x00", "target", false)

	ctx := ctl.WithProcessSnapshot(context.Background())
	require.True(t, ctl.pidFromProcessName(ctx, "target") > 0)

	require.NoError(t, os.RemoveAll(filepath.Join(root, "7001")))

	pids, err := ctl.scanProcesses("target")
	require.NoError(t, err)
	assert.Empty(t, pids)
}

func BenchmarkServiceProbesAgainstProc(b *testing.B) {
	root := b.TempDir()
	ctl := NewController(sys.Real(), WithConfigLoader(emptyConfig))
	ctl.useProcRoot(root + "/")

	for pid := 1; pid <= 400; pid++ {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}

		cmdline := fmt.Sprintf("/usr/bin/daemon%d\x00--config\x00/etc/daemon%d.conf\x00", pid, pid)
		if pid%4 == 0 {
			cmdline = ""
		}

		_ = os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "comm"), []byte(fmt.Sprintf("daemon%d\n", pid)), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\tdaemon\nState:\tS (sleeping)\n"), 0o644)
	}

	needles := []string{"memcached", "opendkim", "cbpolicyd", "nginx", "slapd", "amavisd",
		"freshclam", "clamd", "saslauthd", "milter", "master", "stats", "zmstat", "mailboxd"}

	b.Run("fresh-per-probe", func(b *testing.B) {
		for b.Loop() {
			for _, n := range needles {
				_ = ctl.pidFromProcessName(context.Background(), n)
			}
		}
	})

	b.Run("shared-snapshot", func(b *testing.B) {
		for b.Loop() {
			ctx := ctl.WithProcessSnapshot(context.Background())
			for _, n := range needles {
				_ = ctl.pidFromProcessName(ctx, n)
			}
		}
	})
}
