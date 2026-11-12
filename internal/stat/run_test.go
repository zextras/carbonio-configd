// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cpuHeading = "timestamp, cpu:user, cpu:nice, cpu:sys, cpu:idle, cpu:iowait, cpu:irq, cpu:softirq, " +
		"cpu0:user, cpu0:nice, cpu0:sys, cpu0:idle, cpu0:iowait, cpu0:irq, cpu0:softirq"
	cpuFirstRow = ", 50.0, 0.0, 0.0, 50.0, 0.0, 0.0, 0.0, 50.0, 0.0, 0.0, 50.0, 0.0, 0.0, 0.0"
	cpuIdleRow  = ", 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0"
)

func cpuEnv(t *testing.T) *testEnv {
	t.Helper()

	return newTestEnv(t, files{"proc/stat": "cpu  100 0 0 100 0 0 0 0\ncpu0 100 0 0 100 0 0 0 0\n"})
}

// running is a collector started by Run on the manual clock.
type running struct {
	te   *testEnv
	sigs chan os.Signal
	done chan int
}

func start(t *testing.T, te *testEnv, args ...string) *running {
	t.Helper()

	r := &running{te: te, sigs: make(chan os.Signal, 4), done: make(chan int, 1)}
	te.Signals = r.sigs

	go func() { r.done <- Run(context.Background(), te.Env, args) }()

	r.settle(t)

	return r
}

// settle waits until the collector is parked on the clock.
func (r *running) settle(t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool { return r.te.fakes.Clock.Waiters() > 0 }, 5*time.Second, time.Millisecond)
}

// advance moves the clock and waits for the next sample to be written.
func (r *running) advance(t *testing.T, d time.Duration) {
	t.Helper()

	r.te.fakes.Clock.Advance(d)
	r.settle(t)
}

// stop sends SIGTERM and returns the exit status.
func (r *running) stop(t *testing.T) int {
	t.Helper()

	r.sigs <- syscall.SIGTERM

	select {
	case code := <-r.done:
		return code
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the collector did not stop")

		return -1
	}
}

// zmstat-cpu -c: the heading, then a line every interval at the same second of
// the minute; the pid file exists while it runs and is gone after SIGTERM, and
// every line also goes to syslog (Logger::LogStats, "info").
func TestRunConsoleCollector(t *testing.T) {
	t.Parallel()

	te := cpuEnv(t)
	rec := &recSyslog{}
	te.Syslog = rec

	r := start(t, te, "cpu", "-c", "-i", "30")
	assert.FileExists(t, filepath.Join(te.PidDir, "cpu.pid"))
	assert.Equal(t, "4242", readPid(filepath.Join(te.PidDir, "cpu.pid")))

	r.advance(t, 30*time.Second)
	r.advance(t, 30*time.Second)

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, cpuHeading+"\n"+stamp(0)+cpuFirstRow+"\n"+stamp(1)+cpuIdleRow+"\n"+stamp(2)+cpuIdleRow+"\n",
		te.stdout.String())
	assert.Equal(t, "info zmstat cpu.csv: "+cpuHeading+":: "+stamp(0)+cpuFirstRow, rec.stats[0])
	assert.Len(t, rec.stats, 3)
	assert.NoFileExists(t, filepath.Join(te.PidDir, "cpu.pid"))
}

// A collector started mid-interval waits for the next multiple of the interval
// (waitUntilNiceRoundSecond) before its first line.
func TestRunWaitsForARoundSecond(t *testing.T) {
	t.Parallel()

	te := cpuEnv(t)
	te.fakes.Clock.Advance(7 * time.Second)

	r := start(t, te, "cpu", "-c", "-i", "30")
	assert.Equal(t, cpuHeading+"\n", te.stdout.String()[:len(cpuHeading)+1], "heading is out before the wait")

	te.fakes.Clock.Advance(23 * time.Second)
	r.settle(t)

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, cpuHeading+"\n"+stamp(1)+cpuFirstRow+"\n", te.stdout.String())
}

// The default log is <dir>/<name>.csv; at midnight the day's file goes to
// <dir>/<day>/<name>.csv.gz and the new day starts with the heading.
func TestRunRollsTheLogOverAtMidnight(t *testing.T) {
	t.Parallel()

	te := cpuEnv(t)
	te.fakes.Clock.Advance(24*time.Hour - 30*time.Second)

	r := start(t, te, "cpu", "-i", "30")
	r.advance(t, 30*time.Second)

	assert.Equal(t, exitOK, r.stop(t))

	assert.Equal(t, cpuHeading+"\n01/01/2026 23:59:30"+cpuFirstRow+"\n",
		readGz(t, filepath.Join(te.Dir, "2026-01-01", "cpu.csv.gz")))
	assert.Equal(t, cpuHeading+"\n01/02/2026 00:00:00"+cpuIdleRow+"\n", readFile(t, filepath.Join(te.Dir, "cpu.csv")))
}

// SIGHUP rotates the current log at once (Stat.pm sighup).
func TestRunRotatesOnSIGHUP(t *testing.T) {
	t.Parallel()

	te := cpuEnv(t)
	r := start(t, te, "cpu", "-i", "30")

	r.sigs <- syscall.SIGHUP

	gz := filepath.Join(te.Dir, "2026-01-01", "cpu.csv.gz")
	require.Eventually(t, func() bool { return fileExists(gz) }, 5*time.Second, time.Millisecond)

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, cpuHeading+"\n"+stamp(0)+cpuFirstRow+"\n", readGz(t, gz))
	assert.Equal(t, cpuHeading+"\n", readFile(t, filepath.Join(te.Dir, "cpu.csv")))
}

// stat-<x>.pl createPidFile: a pid file of a live collector ends the new one
// with status 0; a stale one is replaced.
func TestRunAlreadyRunning(t *testing.T) {
	t.Parallel()

	te := cpuEnv(t)
	pidFile := filepath.Join(te.PidDir, "cpu.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte("9999\n"), 0o644))
	te.PidAlive = func(pid int) bool { return pid == 9999 }

	assert.Equal(t, exitOK, Run(context.Background(), te.Env, []string{"cpu", "-c"}))
	assert.Equal(t, "cpu.pid: Already running as pid 9999\n", te.stderr.String())
	assert.Equal(t, "9999", readPid(pidFile), "the live collector keeps its pid file")
	assert.Empty(t, te.stdout.String())
}

func TestRunReplacesStalePidFile(t *testing.T) {
	t.Parallel()

	te := cpuEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(te.PidDir, "cpu.pid"), []byte("9999\n"), 0o644))

	r := start(t, te, "cpu", "-c")
	assert.Equal(t, "4242", readPid(filepath.Join(te.PidDir, "cpu.pid")))
	assert.Equal(t, exitOK, r.stop(t))
}

func TestRunRefusals(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		args       []string
		user       string
		wantStderr string
		wantStdout string
	}{
		"no collector": {wantStderr: "Usage: setup zmstat <name> [options]\nCollectors: " + strings.Join(Names(), ", ") + "\n"},
		"unknown":      {args: []string{"bogus"}, wantStderr: "Unknown collector \"bogus\"; one of: " + strings.Join(Names(), ", ") + "\n"},
		"wrong user":   {args: []string{"cpu"}, user: "alice", wantStderr: "Must be user zextras to run this command\n"},
		"root is only for fd": {args: []string{"cpu"}, user: "root",
			wantStderr: "Must be user zextras to run this command\n"},
		"bad option": {args: []string{"cpu", "--zzz"},
			wantStderr: "Unknown option: zzz\n\n" + cpuUsage},
		"log outside the log directories": {args: []string{"fd", "-l", "/etc/fd.csv"}, user: "root",
			wantStdout: "Error: Specified log file must be created in one of the following locations:"},
	} {
		te := cpuEnv(t)
		if tc.user != "" {
			te.User = tc.user
		}

		assert.Equal(t, exitError, Run(context.Background(), te.Env, tc.args), name)
		assert.Equal(t, tc.wantStderr, te.stderr.String(), name)
		assert.True(t, strings.HasPrefix(te.stdout.String(), tc.wantStdout), name)
		assert.Empty(t, readDir(t, te.PidDir), name, "no pid file is left behind")
	}
}

func readDir(t *testing.T, dir string) []string {
	t.Helper()

	ents, err := os.ReadDir(dir)
	require.NoError(t, err)

	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}

	return names
}

// The root user may run the fd collector (the Perl re-ran it through sudo).
func TestRunFDAsRoot(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{"proc/sys/fs/file-nr": "12 0 99\n"})
	te.User = "root"

	r := start(t, te, "fd", "-c")
	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, "timestamp, fd_count, mailboxd_fd_count\n"+stamp(0)+", 12, 0\n", te.stdout.String())
}

// zmstat_interval and zmstat_disk_interval give the default interval; a
// non-positive -i falls back to it.
func TestDefaultInterval(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		lc   map[string]string
		disk bool
		want time.Duration
	}{
		"unset":            {want: 30 * time.Second},
		"zmstat_interval":  {lc: map[string]string{"zmstat_interval": "15"}, want: 15 * time.Second},
		"below one":        {lc: map[string]string{"zmstat_interval": "0"}, want: 30 * time.Second},
		"not a number":     {lc: map[string]string{"zmstat_interval": "x"}, want: 30 * time.Second},
		"disk default":     {disk: true, want: 6000 * time.Second},
		"disk interval":    {lc: map[string]string{"zmstat_disk_interval": "600"}, disk: true, want: 600 * time.Second},
		"disk ignores the": {lc: map[string]string{"zmstat_interval": "15"}, disk: true, want: 6000 * time.Second},
	} {
		te := newTestEnv(t, files{})
		te.LC = tc.lc

		assert.Equal(t, tc.want, te.defaultInterval(tc.disk), name)
	}
}

// Stat.pm waitUntilNiceRoundSecond: the next second of the day that is a
// multiple of the interval (a whole day for 0), at once when it is one.
func TestWaitUntilNiceRoundSecond(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		at       time.Duration
		interval time.Duration
		wait     time.Duration
	}{
		"on a round second": {at: 60 * time.Second, interval: 30 * time.Second},
		"mid interval":      {at: 7 * time.Second, interval: 30 * time.Second, wait: 23 * time.Second},
		"a day":             {at: time.Hour, interval: 0, wait: 23 * time.Hour},
		"hourly":            {at: 61 * time.Minute, interval: time.Hour, wait: 59 * time.Minute},
	} {
		te := newTestEnv(t, files{})
		te.fakes.Clock.Advance(tc.at)

		done := make(chan bool, 1)

		go func() { done <- te.waitUntilNiceRoundSecond(context.Background(), tc.interval) }()

		if tc.wait == 0 {
			assert.True(t, <-done, name)

			continue
		}

		require.Eventually(t, func() bool { return te.fakes.Clock.Waiters() > 0 }, 5*time.Second, time.Millisecond, name)
		te.fakes.Clock.Advance(tc.wait - time.Second)
		assert.Empty(t, done, name, "not yet")
		te.fakes.Clock.Advance(time.Second)
		assert.True(t, <-done, name)
	}
}
