// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// secondsPerDay is the longest interval waitUntilNiceRoundSecond aligns to.
	secondsPerDay = 24 * 60 * 60
	// defaultInterval is the interval of getZmstatInterval when zmstat_interval
	// is unset or below 1.
	defaultInterval = 30
	// defaultDiskInterval is the lcdefaults value of zmstat_disk_interval.
	defaultDiskInterval = 6000
)

// tstamp is Stat.pm getTstamp: MM/DD/YYYY HH:MM:SS in local time.
func (e *Env) tstamp(t time.Time) string {
	t = t.In(e.loc())

	return fmt.Sprintf("%02d/%02d/%04d %02d:%02d:%02d",
		int(t.Month()), t.Day(), t.Year(), t.Hour(), t.Minute(), t.Second())
}

// date is Stat.pm getDate: YYYY-MM-DD in local time.
func (e *Env) date(t time.Time) string {
	t = t.In(e.loc())

	return fmt.Sprintf("%04d-%02d-%02d", t.Year(), int(t.Month()), t.Day())
}

// percent is Stat.pm percent: "%.1f" of val*100/total, 0.0 when total is not
// positive.
func percent(val, total int64) string {
	if total <= 0 {
		return "0.0"
	}

	return strconv.FormatFloat(float64(val*100)/float64(total), 'f', 1, 64)
}

// interval is the default interval of a collector: getZmstatInterval
// (zmstat_interval, 30 when below 1) or zmstat_disk_interval for df.
func (e *Env) defaultInterval(disk bool) time.Duration {
	key, def := "zmstat_interval", defaultInterval
	if disk {
		key, def = "zmstat_disk_interval", defaultDiskInterval
	}

	n, err := strconv.Atoi(strings.TrimSpace(e.LC[key]))
	if err != nil || n < 1 {
		n = def
	}

	return time.Duration(n) * time.Second
}

// niceRoundWait is how long to wait until the second of the day is a multiple
// of the interval (a day for an interval of 0 or a multiple of a day); zero on
// such a second.
func (e *Env) niceRoundWait(interval time.Duration) time.Duration {
	secs := int64(interval/time.Second) % secondsPerDay
	if secs == 0 {
		secs = secondsPerDay
	}

	now := e.Sys.Clock.Now().In(e.loc())
	sod := int64(now.Hour()*3600 + now.Minute()*60 + now.Second())

	rem := sod % secs
	if rem == 0 {
		return 0
	}

	return time.Duration(secs-rem)*time.Second - time.Duration(now.Nanosecond())
}

// alive is Perl's kill(0, pid) hardened against pid reuse: the process must
// exist and be a collector (its command line is a zmstat-* script, "configd stat" or, of the release before,
// "setup stat").
func (e *Env) alive(pid int) bool {
	if e.PidAlive != nil {
		return e.PidAlive(pid)
	}

	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}

	cmdline, err := fs.ReadFile(e.Root, procPath(strconv.Itoa(pid), "cmdline"))

	return err != nil || strings.Contains(string(cmdline), "zmstat") || strings.Contains(string(cmdline), "\x00stat\x00")
}

// readPid is Stat.pm readPidFile: the first line of the file, "" when it
// cannot be read.
func readPid(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // a pid file
	if err != nil {
		return ""
	}

	line, _, _ := strings.Cut(string(data), "\n")

	return line
}

var errAlreadyRunning = errors.New("already running")

// createPid is Stat.pm createPidFile: a pid file of a live collector means
// errAlreadyRunning (the caller exits 0 after the message), a stale one is
// replaced.
func (e *Env) createPid(name string) error {
	pidFile := filepath.Join(e.PidDir, name)

	if _, err := os.Stat(pidFile); err == nil {
		if pid, err := strconv.Atoi(readPid(pidFile)); err == nil && pid > 0 {
			if e.alive(pid) {
				_, _ = fmt.Fprintf(e.Stderr, "%s: Already running as pid %d\n", name, pid)

				return errAlreadyRunning
			}

			_ = os.Remove(pidFile)
		}
	}

	if _, err := os.Stat(e.Dir); err != nil {
		return fmt.Errorf("%s does not exist", e.Dir)
	}

	if _, err := os.Stat(e.PidDir); err != nil {
		_ = os.Mkdir(e.PidDir, 0o755)
	}

	f, err := os.OpenFile(pidFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666) //nolint:gosec // a pid file
	if err != nil {
		return fmt.Errorf("unable to create pid file %s: %w", pidFile, err)
	}

	_, werr := fmt.Fprintf(f, "%d\n", e.Pid)
	cerr := f.Close()

	return errors.Join(werr, cerr)
}

// removePid deletes the pid file when it still names this process.
func (e *Env) removePid(name string) {
	pidFile := filepath.Join(e.PidDir, name)
	if readPid(pidFile) == strconv.Itoa(e.Pid) {
		_ = os.Remove(pidFile)
	}
}

// procPath is the path, in Env.Root, of a file of process pid (a number).
func procPath(pid, name string) string { return "proc/" + pid + "/" + name }
