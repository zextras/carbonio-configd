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

const fdHeading = "timestamp, fd_count, mailboxd_fd_count"

// cpuFDEnv is a system the cpu and fd collectors can read.
func cpuFDEnv(t *testing.T) *testEnv {
	t.Helper()

	return newTestEnv(t, files{
		"proc/stat":           "cpu  100 0 0 100 0 0 0 0\ncpu0 100 0 0 100 0 0 0 0\n",
		"proc/sys/fs/file-nr": "12 0 99\n",
	})
}

// startMany runs "configd stat run <cmds>" on the manual clock, once the n
// collectors that start are parked on it.
func startMany(t *testing.T, te *testEnv, n int, cmds ...string) *running {
	t.Helper()

	r := &running{te: te, sigs: make(chan os.Signal, 4), done: make(chan int, 1)}
	te.Signals = r.sigs

	go func() { r.done <- Run(context.Background(), te.Env, append([]string{"run"}, cmds...)) }()

	r.settleN(t, n)

	return r
}

func (r *running) settleN(t *testing.T, n int) {
	t.Helper()

	require.Eventually(t, func() bool { return r.te.fakes.Clock.Waiters() == n }, 5*time.Second, time.Millisecond)
}

// "configd stat run cpu fd": each collector writes its own CSV at its own
// interval and keeps the pid file of a process of its own, naming the one
// process; a stop removes them.
func TestRunManyWritesEveryCollector(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)
	r := startMany(t, te, 2, "cpu -i 30", "fd -i 60")

	for _, name := range []string{"cpu", "fd", "stats"} {
		assert.Equal(t, "4242", readPid(filepath.Join(te.PidDir, name+".pid")), name)
	}

	for range 2 {
		te.fakes.Clock.Advance(30 * time.Second)
		r.settleN(t, 2)
	}

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, cpuHeading+"\n"+stamp(0)+cpuFirstRow+"\n"+stamp(1)+cpuIdleRow+"\n"+stamp(2)+cpuIdleRow+"\n",
		readFile(t, filepath.Join(te.Dir, "cpu.csv")))
	assert.Equal(t, fdHeading+"\n"+stamp(0)+", 12, 0\n"+stamp(2)+", 12, 0\n", readFile(t, filepath.Join(te.Dir, "fd.csv")))
	assert.Empty(t, readDir(t, te.PidDir), "every pid file is removed")
}

// A collector that cannot start is reported and the rest run; the status says
// that one failed.
func TestRunManyIsolatesACollectorThatCannotStart(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t) // no /sys/block: the io collector cannot start
	r := startMany(t, te, 2, "io", "cpu -i 30", "fd -i 30")

	assert.Contains(t, te.stderr.String(), "stat io: unable to list /sys/block")
	assert.NoFileExists(t, filepath.Join(te.PidDir, "io.pid"))
	assert.NoFileExists(t, filepath.Join(te.Dir, "io.csv"))

	te.fakes.Clock.Advance(30 * time.Second)
	r.settleN(t, 2)

	assert.Equal(t, exitError, r.stop(t))
	assert.Equal(t, cpuHeading+"\n"+stamp(0)+cpuFirstRow+"\n"+stamp(1)+cpuIdleRow+"\n", readFile(t, filepath.Join(te.Dir, "cpu.csv")))
	assert.Equal(t, fdHeading+"\n"+stamp(0)+", 12, 0\n"+stamp(1)+", 12, 0\n", readFile(t, filepath.Join(te.Dir, "fd.csv")))
}

// A collector whose samples fail is reported at each tick and the others keep
// writing.
func TestRunManyIsolatesACollectorWhoseSamplesFail(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, files{"proc/stat": "cpu  100 0 0 100 0 0 0 0\ncpu0 100 0 0 100 0 0 0 0\n"}) // no file-nr
	r := startMany(t, te, 2, "cpu -i 30", "fd -i 30")

	te.fakes.Clock.Advance(30 * time.Second)
	r.settleN(t, 2)

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, 2, strings.Count(te.stderr.String(), "stat fd: can't open /proc/sys/fs/file-nr"))
	assert.Equal(t, cpuHeading+"\n"+stamp(0)+cpuFirstRow+"\n"+stamp(1)+cpuIdleRow+"\n", readFile(t, filepath.Join(te.Dir, "cpu.csv")))
}

// A collector that panics ends with a report and status 1; it does not take
// the process down.
func TestGuardedReportsAPanic(t *testing.T) {
	t.Parallel()

	te := cpuEnv(t)
	r := &runner{env: te.Env, sp: &spec{name: "boom"}, c: panicCollector{}, date: "2026-01-01", interval: 30 * time.Second}

	assert.Equal(t, exitError, r.guarded(context.Background(), make(chan os.Signal)))
	assert.Contains(t, te.stderr.String(), "stat boom: panic: kaboom\n")
}

type panicCollector struct{}

func (panicCollector) Heading() string { return "timestamp" }

func (panicCollector) Sample(Tick) ([]string, error) { panic("kaboom") }

// SIGHUP rotates the log of every collector.
func TestRunManyRotatesEveryCollector(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)
	r := startMany(t, te, 2, "cpu -i 30", "fd -i 30")

	r.sigs <- syscall.SIGHUP

	for _, name := range []string{"cpu", "fd"} {
		gz := filepath.Join(te.Dir, "2026-01-01", name+".csv.gz")
		require.Eventually(t, func() bool { return fileExists(gz) }, 5*time.Second, time.Millisecond, name)
	}

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, cpuHeading+"\n"+stamp(0)+cpuFirstRow+"\n", readGz(t, filepath.Join(te.Dir, "2026-01-01", "cpu.csv.gz")))
	assert.Equal(t, fdHeading+"\n"+stamp(0)+", 12, 0\n", readGz(t, filepath.Join(te.Dir, "2026-01-01", "fd.csv.gz")))
	assert.Equal(t, cpuHeading+"\n", readFile(t, filepath.Join(te.Dir, "cpu.csv")))
	assert.Equal(t, fdHeading+"\n", readFile(t, filepath.Join(te.Dir, "fd.csv")))
}

// SIGTERM and SIGINT each stop every collector.
func TestRunManyStopsEveryCollector(t *testing.T) {
	t.Parallel()

	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
		te := cpuFDEnv(t)
		r := startMany(t, te, 2, "cpu", "fd")

		r.sigs <- sig

		select {
		case code := <-r.done:
			assert.Equal(t, exitOK, code, sig.String())
		case <-time.After(5 * time.Second):
			require.FailNow(t, "the collectors did not stop", sig.String())
		}

		assert.Empty(t, readDir(t, te.PidDir), sig.String())
	}
}

// A collector another process runs is left to it, the others run here.
func TestRunManyLeavesACollectorThatIsAlreadyRunning(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(te.PidDir, "fd.pid"), []byte("9999\n"), 0o644))
	te.PidAlive = func(pid int) bool { return pid == 9999 }

	r := startMany(t, te, 1, "cpu", "fd")

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, "fd.pid: Already running as pid 9999\n", te.stderr.String())
	assert.Equal(t, "9999", readPid(filepath.Join(te.PidDir, "fd.pid")), "the live collector keeps its pid file")
	assert.NoFileExists(t, filepath.Join(te.Dir, "fd.csv"))
	assert.FileExists(t, filepath.Join(te.Dir, "cpu.csv"))
}

// A second "run" while the process of the first is running does nothing: it
// leaves the log and the pid files to it, and the status is 0.
func TestRunManyProcessAlreadyRunning(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(te.PidDir, "stats.pid"), []byte("9999\n"), 0o644))
	te.PidAlive = func(pid int) bool { return pid == 9999 }

	assert.Equal(t, exitOK, Run(context.Background(), te.Env, []string{"run", "cpu", "fd"}))
	assert.Equal(t, "stats.pid: Already running as pid 9999\n", te.stderr.String())
	assert.Equal(t, []string{"stats.pid"}, readDir(t, te.PidDir))
	assert.Empty(t, readDir(t, te.Dir))
}

// All of them already running: nothing to do, status 0.
func TestRunManyEveryCollectorAlreadyRunning(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)
	require.NoError(t, os.WriteFile(filepath.Join(te.PidDir, "cpu.pid"), []byte("9999\n"), 0o644))
	te.PidAlive = func(int) bool { return true }

	assert.Equal(t, exitOK, Run(context.Background(), te.Env, []string{"run", "cpu"}))
}

// Nothing starts when the command line is not right.
func TestRunManyRefusals(t *testing.T) {
	t.Parallel()

	usage := "Usage: configd stat run <collector> [<collector>...]\nCollectors: " + strings.Join(Names(), ", ") + "\n"

	for name, tc := range map[string]struct {
		cmds       []string
		user       string
		wantStderr string
	}{
		"no collector": {wantStderr: usage},
		"unknown":      {cmds: []string{"cpu", "bogus"}, wantStderr: "Unknown collector \"bogus\"; one of: " + strings.Join(Names(), ", ") + "\n"},
		"empty":        {cmds: []string{"cpu", " "}, wantStderr: "Empty collector\n"},
		"wrong user":   {cmds: []string{"cpu"}, user: "alice", wantStderr: "Must be user zextras to run this command\n"},
		"bad option":   {cmds: []string{"cpu", "cpu --zzz"}, wantStderr: "Unknown option: zzz\n\n" + cpuUsage},
		"twice":        {cmds: []string{"cpu", "cpu -i 5"}, wantStderr: "Collector \"cpu -i 5\" is given twice\n"},
		"argument":     {cmds: []string{"fd stop"}, wantStderr: "Collector \"fd stop\": unexpected argument \"stop\"\n"},
	} {
		te := cpuFDEnv(t)
		if tc.user != "" {
			te.User = tc.user
		}

		assert.Equal(t, exitError, Run(context.Background(), te.Env, append([]string{"run"}, tc.cmds...)), name)
		assert.Equal(t, tc.wantStderr, te.stderr.String(), name)
		assert.Empty(t, readDir(t, te.PidDir), name, "no pid file is left behind")
		assert.Empty(t, readDir(t, te.Dir), name, "no log is left behind")
	}
}

// fd keeps its check of the log path in a process of several collectors.
func TestRunManyChecksTheLogPathOfFD(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)

	assert.Equal(t, exitError, Run(context.Background(), te.Env, []string{"run", "cpu", "fd -l /etc/fd.csv"}))
	assert.True(t, strings.HasPrefix(te.stdout.String(), "Error: Specified log file must be created in one of the following locations:"))
	assert.Empty(t, readDir(t, te.PidDir))
}

// Each collector takes the options of its script, and the interval of the
// collector that is not given one is the default.
func TestRunManyCollectorOptions(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)
	r := startMany(t, te, 2, "cpu -i 10 -l "+filepath.Join(te.Dir, "other.csv"), "fd")

	te.fakes.Clock.Advance(10 * time.Second)
	r.settleN(t, 2)

	assert.Equal(t, exitOK, r.stop(t))
	assert.Equal(t, cpuHeading+"\n"+stamp(0)+cpuFirstRow+"\n01/01/2026 00:00:10"+cpuIdleRow+"\n",
		readFile(t, filepath.Join(te.Dir, "other.csv")))
	assert.Equal(t, fdHeading+"\n"+stamp(0)+", 12, 0\n", readFile(t, filepath.Join(te.Dir, "fd.csv")))
}

// When no collector can start the status is 1 and the process ends at once.
func TestRunManyNoCollectorStarts(t *testing.T) {
	t.Parallel()

	te := cpuFDEnv(t)
	assert.Equal(t, exitError, Run(context.Background(), te.Env, []string{"run", "io"}))
	assert.Contains(t, te.stderr.String(), "stat io: unable to list /sys/block")

	te = cpuFDEnv(t)
	require.NoError(t, os.Remove(te.Dir))

	assert.Equal(t, exitError, Run(context.Background(), te.Env, []string{"run", "cpu"}))
	assert.Equal(t, te.Dir+" does not exist\n", te.stderr.String())
}
