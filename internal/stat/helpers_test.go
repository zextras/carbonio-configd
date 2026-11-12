// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// files is a fixed tree of /proc, /sys and /etc files, keyed by path without
// the leading slash.
type files map[string]string

func (f files) fs() fs.FS {
	m := fstest.MapFS{}
	for name, data := range f {
		m[name] = &fstest.MapFile{Data: []byte(data), Mode: 0o644}
	}

	return m
}

// testEnv is an Env on fixed files: UTC, 4 kB pages, a manual clock, the pid
// and CSV directories in the test's temp dir, and the output captured.
type testEnv struct {
	*Env
	fakes  *systest.Fakes
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newTestEnv(t *testing.T, tree files) *testEnv {
	t.Helper()

	base := t.TempDir()
	f := systest.New()
	te := &testEnv{fakes: f, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	te.Env = &Env{
		Sys: f.System(), Root: tree.fs(), LC: map[string]string{}, User: defaultUser, Pid: 4242,
		PageSize: 4096, Loc: time.UTC, Base: base, Dir: filepath.Join(base, "zmstat"),
		PidDir: filepath.Join(base, "run"), Stdout: te.stdout, Stderr: te.stderr,
		PidAlive: func(int) bool { return false },
	}

	require.NoError(t, os.MkdirAll(te.Dir, 0o755))
	require.NoError(t, os.MkdirAll(te.PidDir, 0o755))

	return te
}

// setFiles replaces the whole tree: the system moved on between two ticks.
func (te *testEnv) setFiles(tree files) { te.Root = tree.fs() }

// stamp is the timestamp column of the n-th sample: the clock starts at
// 2026-01-01 00:00:00 UTC and ticks are 30 s apart.
func stamp(n int) string {
	return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * 30 * time.Second).
		Format("01/02/2006 15:04:05")
}

// sample takes one sample at the given tick number (stamp(n)).
func (te *testEnv) sample(t *testing.T, c Collector, n int) []string {
	t.Helper()

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * 30 * time.Second)
	lines, err := c.Sample(Tick{Now: now, Stamp: te.tstamp(now)})
	require.NoError(t, err)

	return lines
}

// opts30 are the options of a collector run with -i 30.
func opts30() *options { return &options{interval: 30 * time.Second} }
