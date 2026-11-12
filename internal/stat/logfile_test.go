// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	logHeading = "timestamp, a, b"
	oldLine    = "12/31/2025 23:59:30, 1, 2"
)

func today() string { return "2026-01-01" }

func readGz(t *testing.T, path string) string {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)

	defer func() { _ = f.Close() }()

	zr, err := gzip.NewReader(f)
	require.NoError(t, err)

	data, err := io.ReadAll(zr)
	require.NoError(t, err)

	return string(data)
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}

// Stat.pm openLogFile: the log directory is created (and handed to zextras),
// the heading is written, and a second open writes it again.
func TestOpenLogCreatesDirectoryAndHeading(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})

	var chowned []string

	te.Chown = func(p string) error { chowned = append(chowned, p); return nil }

	path := filepath.Join(te.Dir, "sub", "x.csv")

	l, err := openLog(te.Env, path, logHeading, today)
	require.NoError(t, err)
	require.NoError(t, l.write([]string{"01/01/2026 00:00:00, 3, 4"}))
	l.close()

	l, err = openLog(te.Env, path, logHeading, today)
	require.NoError(t, err)
	l.close()

	assert.Equal(t, []string{filepath.Dir(path)}, chowned)
	assert.Equal(t, logHeading+"\n01/01/2026 00:00:00, 3, 4\n"+logHeading+"\n", readFile(t, path))
}

func TestOpenLogCannotCreateDirectory(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	require.NoError(t, os.WriteFile(filepath.Join(te.Dir, "plain"), nil, 0o644))

	_, err := openLog(te.Env, filepath.Join(te.Dir, "plain", "deeper", "x.csv"), logHeading, today)
	require.ErrorContains(t, err, "unable to create log directory")
}

func TestConsoleLogIsStdoutAndNeverRotated(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})

	for _, path := range []string{"", "-"} {
		te.stdout.Reset()

		l, err := openLog(te.Env, path, logHeading, today)
		require.NoError(t, err)
		require.NoError(t, l.write([]string{"a", "b"}))
		require.NoError(t, l.rotate("2025-12-31"))
		l.close()

		assert.Equal(t, logHeading+"\na\nb\n", te.stdout.String(), path)
	}
}

// Stat.pm openLogFile: a log whose last line is of an earlier day is moved to
// <dir>/<that day>/<name>.gz first; a log with no data line is left alone.
func TestOpenLogPreRotatesStaleLog(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	path := filepath.Join(te.Dir, "x.csv")

	var chowned []string

	te.Chown = func(p string) error { chowned = append(chowned, p); return nil }

	require.NoError(t, os.WriteFile(path, []byte(logHeading+"\n"+oldLine+"\n"), 0o644))

	l, err := openLog(te.Env, path, logHeading, today)
	require.NoError(t, err)
	l.close()

	gz := filepath.Join(te.Dir, "2025-12-31", "x.csv.gz")
	assert.Equal(t, logHeading+"\n"+oldLine+"\n", readGz(t, gz))
	assert.Equal(t, logHeading+"\n", readFile(t, path))
	assert.Equal(t, path+" was stale (2025-12-31) pre-rotating\n", te.stderr.String())
	assert.Equal(t, []string{filepath.Dir(gz), gz}, chowned)
}

func TestOpenLogKeepsTodaysAndHeadingOnlyLogs(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"today":        logHeading + "\n01/01/2026 00:00:00, 1, 2\n",
		"heading only": logHeading + "\n",
		"empty":        "",
	} {
		te := newTestEnv(t, files{})
		path := filepath.Join(te.Dir, "x.csv")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		l, err := openLog(te.Env, path, logHeading, today)
		require.NoError(t, err, name)
		l.close()

		assert.Equal(t, content+logHeading+"\n", readFile(t, path), name)
		assert.Empty(t, te.stderr.String(), name)
		assert.NoDirExists(t, filepath.Join(te.Dir, "2025-12-31"), name)
	}
}

// rotateLogFile (HUP, midnight): the day's gz grows by `cat old new | gzip`,
// a bare copy left there is merged and removed, and the new log restarts with
// the heading.
func TestRotateAppendsToTheDaysArchive(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	path := filepath.Join(te.Dir, "x.csv")

	l, err := openLog(te.Env, path, logHeading, today)
	require.NoError(t, err)
	require.NoError(t, l.write([]string{"01/01/2026 00:00:00, 1, 2"}))
	require.NoError(t, l.rotate("2026-01-01"))
	require.NoError(t, l.write([]string{"01/01/2026 00:00:30, 3, 4"}))
	require.NoError(t, l.rotate("2026-01-01"))
	l.close()

	assert.Equal(t, logHeading+"\n01/01/2026 00:00:00, 1, 2\n"+logHeading+"\n01/01/2026 00:00:30, 3, 4\n",
		readGz(t, filepath.Join(te.Dir, "2026-01-01", "x.csv.gz")))
	assert.Equal(t, logHeading+"\n", readFile(t, path))
}

func TestRotateMergesBareCopy(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	path := filepath.Join(te.Dir, "x.csv")
	rdir := filepath.Join(te.Dir, "2026-01-01")
	require.NoError(t, os.Mkdir(rdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rdir, "x.csv"), []byte("bare\n"), 0o644))

	l, err := openLog(te.Env, path, logHeading, today)
	require.NoError(t, err)
	require.NoError(t, l.rotate("2026-01-01"))
	l.close()

	assert.Equal(t, "bare\n"+logHeading+"\n", readGz(t, filepath.Join(rdir, "x.csv.gz")))
	assert.NoFileExists(t, filepath.Join(rdir, "x.csv"))
}

func TestRotateDropsUnreadableArchive(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	path := filepath.Join(te.Dir, "x.csv")
	rdir := filepath.Join(te.Dir, "2026-01-01")
	require.NoError(t, os.Mkdir(rdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rdir, "x.csv.gz"), []byte("not gzip"), 0o644))

	l, err := openLog(te.Env, path, logHeading, today)
	require.NoError(t, err)
	require.NoError(t, l.rotate("2026-01-01"))
	l.close()

	assert.Equal(t, logHeading+"\n", readGz(t, filepath.Join(rdir, "x.csv.gz")))
}

func TestRotateWhereTheDayDirectoryIsAFile(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{})
	path := filepath.Join(te.Dir, "x.csv")
	require.NoError(t, os.WriteFile(filepath.Join(te.Dir, "2026-01-01"), nil, 0o644))

	l, err := openLog(te.Env, path, logHeading, today)
	require.NoError(t, err)

	defer l.close()

	require.ErrorContains(t, l.rotate("2026-01-01"), "unable to create log rotation directory")
}

func TestLineDate(t *testing.T) {
	t.Parallel()

	for line, want := range map[string]string{
		"12/31/2025 23:59:30, 1": "2025-12-31",
		"timestamp, a":           "",
		"12-31-2025 x":           "",
		"1a/31/2025 x":           "",
		"":                       "",
	} {
		got, ok := lineDate(line)
		assert.Equal(t, want != "", ok, line)
		assert.Equal(t, want, got, line)
	}
}
