// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package statcmd

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/stat"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// "stat run" is the long-running process of all the collectors: its
// garbage collection target is the lower one of the collectors; a single
// collector keeps the runtime's.
func TestRunTunesTheMemoryOfTheProcessThatRunsCollectors(t *testing.T) { //nolint:paralleltest // the GC target is the process's
	orig := runStat

	defer func() { runStat = orig }()

	var got []string

	runStat = func(_ context.Context, _ *stat.Env, args []string) int {
		got = args

		return 0
	}

	prev := debug.SetGCPercent(100)

	defer debug.SetGCPercent(prev)

	run := func(args ...string) int {
		debug.SetGCPercent(100)
		assert.Zero(t, Run(context.Background(), systest.New().System(), &bytes.Buffer{}, &bytes.Buffer{}, args))

		return debug.SetGCPercent(100)
	}

	assert.Equal(t, 25, run("run", "cpu", "fd"))
	assert.Equal(t, []string{"run", "cpu", "fd"}, got)
	assert.Equal(t, 100, run("cpu"))
	assert.Equal(t, 100, run())
}

// currentUserName is the login of the test process; giving a file to oneself
// is the one chown an unprivileged test may do.
func currentUserName(t *testing.T) string {
	t.Helper()

	u, err := user.Current()
	if err != nil || u.Username == "" {
		t.Skipf("no passwd entry for the test user: %v", err)
	}

	return u.Username
}

// lchownTo changes the link itself, never its target: on a dangling link a
// following chown fails with ENOENT, a non-following one succeeds.
func TestLchownTo_DoesNotFollowASymlink(t *testing.T) {
	t.Parallel()

	link := filepath.Join(t.TempDir(), "log")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing"), link))

	require.NoError(t, lchownTo(currentUserName(t))(link))
}

// fchownTo works through the descriptor: it still reaches the open file once
// its path is gone, where a chown by path would fail.
func TestFchownTo_GoesThroughTheDescriptor(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "log")
	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	require.NoError(t, os.Remove(path))

	require.NoError(t, fchownTo(currentUserName(t))(f))
}

func TestChownTo_UnknownUserIsAnError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "log")
	f, err := os.Create(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = f.Close() })

	const nobody = "configd-test-no-such-user"

	require.ErrorContains(t, lchownTo(nobody)(path), nobody)
	require.ErrorContains(t, fchownTo(nobody)(f), nobody)
}
