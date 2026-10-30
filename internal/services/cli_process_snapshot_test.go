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
)

// fakeProcFS points procFSRoot at a temp dir and returns a writer that adds a
// /proc/<pid> entry (state "S", or "Z" for zombies).
func fakeProcFS(t *testing.T) (root string, add func(pid int, cmdline, comm string, zombie bool)) {
	t.Helper()

	root = t.TempDir()
	old := procFSRoot
	procFSRoot = root + "/"

	t.Cleanup(func() { procFSRoot = old })

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

func TestScanProcessesByCmdline_UsesProcFSRoot(t *testing.T) {
	_, add := fakeProcFS(t)
	add(4001, "/opt/zextras/common/sbin/opendkim\x00-x\x00/etc/opendkim.conf\x00", "opendkim", false)
	add(4002, "/usr/sbin/other\x00", "other", false)

	pids, err := scanProcessesByCmdline("opendkim")
	require.NoError(t, err)
	assert.Equal(t, []int{4001}, pids)
}

func TestScanProcessesByCmdline_MissingRootErrors(t *testing.T) {
	root, _ := fakeProcFS(t)
	require.NoError(t, os.Remove(root))

	_, err := scanProcessesByCmdline("anything")
	require.Error(t, err)
}

// TestProcessSnapshot_MatchesLikeFreshScan pins that a snapshot yields the
// same verdicts as a fresh scan for every needle, including the zombie skip,
// the cmdline-authoritative rule and the exact-comm fallback.
func TestProcessSnapshot_MatchesLikeFreshScan(t *testing.T) {
	_, add := fakeProcFS(t)
	add(5001, "/usr/bin/memcached\x00-p\x0011211\x00", "memcached", false)
	add(5002, "", "nginx", false)                          // argv zeroed: exact comm fallback
	add(5003, "nginx: worker process\x00", "nginx", false) // cmdline authoritative
	add(5004, "/usr/bin/memcached\x00", "memcached", true) // zombie
	add(5005, "/bin/bash\x00/usr/bin/service-discover-wrapper.sh\x00", "service-discove", false)

	needles := []string{"memcached", "nginx", "nginx-worker", "service-discovered", "-p 11211", "absent"}
	snap := &procSnapshot{}

	for _, needle := range needles {
		t.Run(needle, func(t *testing.T) {
			want, err := scanProcessesByCmdline(needle)
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
	root, add := fakeProcFS(t)
	add(6001, "/usr/bin/memcached\x00", "memcached", false)

	ctx := WithProcessSnapshot(context.Background())
	assert.True(t, isProcessRunning(ctx, "memcached"))

	// The snapshot is frozen: a process appearing or vanishing afterwards is
	// only seen by a fresh scan.
	add(6002, "/usr/sbin/opendkim\x00", "opendkim", false)
	require.NoError(t, os.RemoveAll(filepath.Join(root, "6001")))

	assert.True(t, isProcessRunning(ctx, "memcached"), "snapshot must not be re-read")
	assert.False(t, isProcessRunning(ctx, "opendkim"), "snapshot must not be re-read")

	assert.False(t, isProcessRunning(context.Background(), "memcached"), "plain ctx takes a fresh listing")
	assert.True(t, isProcessRunning(context.Background(), "opendkim"), "plain ctx takes a fresh listing")
}

func TestWithProcessSnapshot_Idempotent(t *testing.T) {
	ctx := WithProcessSnapshot(context.Background())
	assert.Same(t, ctx, WithProcessSnapshot(ctx))
}

func TestIsProcessRunning_ExcludesSelfWithSnapshot(t *testing.T) {
	_, add := fakeProcFS(t)
	add(os.Getpid(), "configd\x00status\x00", "configd", false)

	ctx := WithProcessSnapshot(context.Background())
	assert.False(t, isProcessRunning(ctx, "configd"))
	assert.Equal(t, 0, pidFromProcessName("configd"))
}

// TestKillProcess_IgnoresSnapshot pins that signaling always sees live state:
// scanProcessesByCmdline (used by killProcess) never consults a snapshot.
func TestKillProcess_IgnoresSnapshot(t *testing.T) {
	root, add := fakeProcFS(t)
	add(7001, "/usr/bin/target\x00", "target", false)

	ctx := WithProcessSnapshot(context.Background())
	require.True(t, isProcessRunning(ctx, "target"))

	require.NoError(t, os.RemoveAll(filepath.Join(root, "7001")))

	pids, err := scanProcessesByCmdline("target")
	require.NoError(t, err)
	assert.Empty(t, pids)
}

func BenchmarkServiceProbesAgainstProc(b *testing.B) {
	root := b.TempDir()
	old := procFSRoot
	procFSRoot = root + "/"

	b.Cleanup(func() { procFSRoot = old })

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
				isProcessRunning(context.Background(), n)
			}
		}
	})

	b.Run("shared-snapshot", func(b *testing.B) {
		for b.Loop() {
			ctx := WithProcessSnapshot(context.Background())
			for _, n := range needles {
				isProcessRunning(ctx, n)
			}
		}
	})
}
