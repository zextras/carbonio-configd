// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const fdUsage = `Usage: zmstat-fd [options]
Monitor system filedescriptor usage
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/fd.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when a HUP signal is sent or when
the date changes.  The current log is renamed to <dir>/YYY-MM-DD/fd.csv
and a new file is created.
`

// mailboxdCmd is the pattern of "pgrep -f" for the mailboxd JVM.
var mailboxdCmd = regexp.MustCompile(`/opt/zextras/.*/java.*mailboxd`)

func fdSpec() *spec {
	return &spec{
		name:      "fd",
		files:     fixedFiles("fd.csv", "fd.pid"),
		usage:     fdUsage,
		rootOK:    true,
		control:   fdControl,
		validate:  fdValidate,
		afterOpen: fdOwn,
		build:     func(env *Env, _ *options) (Collector, error) { return &fd{env: env}, nil },
	}
}

// fd is stat-fd.pl: the open file descriptors of the system (the first number
// of /proc/sys/fs/file-nr) and of mailboxd.
type fd struct{ env *Env }

// Heading implements Collector.
func (f *fd) Heading() string { return "timestamp, fd_count, mailboxd_fd_count" }

// Sample implements Collector.
func (f *fd) Sample(t Tick) ([]string, error) {
	data, err := fs.ReadFile(f.env.Root, "proc/sys/fs/file-nr")
	if err != nil {
		return nil, fmt.Errorf("can't open /proc/sys/fs/file-nr: %w", err)
	}

	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return nil, errors.New("empty /proc/sys/fs/file-nr")
	}

	return []string{fmt.Sprintf("%s, %s, %d", t.Stamp, fields[0], f.mailboxdFDs())}, nil
}

// mailboxdFDs is get_mbox_stat: the entries of /proc/<pid>/fd of mailboxd, 0
// when it is not running.
func (f *fd) mailboxdFDs() int {
	pid := f.mailboxdPid()
	if pid == 0 {
		_, _ = fmt.Fprintln(f.env.Stderr, "Unable to determine mailboxd pid")

		return 0
	}

	ents, err := fs.ReadDir(f.env.Root, "proc/"+strconv.Itoa(pid)+"/fd")
	if err != nil {
		_, _ = fmt.Fprintln(f.env.Stderr, err)

		return 0
	}

	return len(ents)
}

// mailboxdPid is `pgrep -o -f '/opt/zextras/.*/java.*mailboxd'`: the oldest
// (lowest) pid whose command line matches, 0 when there is none.
func (f *fd) mailboxdPid() int {
	ents, _ := fs.ReadDir(f.env.Root, "proc")
	best := 0

	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid == f.env.Pid || best != 0 && pid > best {
			continue
		}

		if args := f.env.cmdline(pid); mailboxdCmd.MatchString(strings.Join(args, " ")) {
			best = pid
		}
	}

	return best
}

// fdControl handles "stop" and "rotate", which signal the running collector.
func fdControl(env *Env, args []string) (int, bool) {
	if len(args) == 0 || args[0] != "stop" && args[0] != "rotate" {
		return 0, false
	}

	pidFile := filepath.Join(env.PidDir, "fd.pid")
	pid, _ := strconv.Atoi(readPid(pidFile))

	if args[0] == "rotate" {
		_ = env.Sys.Proc.Signal(pid, syscall.SIGHUP)

		return exitOK, true
	}

	// stop: kill(0) fails for a stale pid file, TERM is tried before KILL.
	if env.Sys.Proc.Signal(pid, 0) != nil ||
		env.Sys.Proc.Signal(pid, syscall.SIGTERM) == nil ||
		env.Sys.Proc.Signal(pid, syscall.SIGKILL) == nil {
		_ = os.Remove(pidFile)
	}

	return exitOK, true
}

// fdValidate restricts where the log may be written: the collector can run as
// root, so it refuses paths outside the log directories.
func fdValidate(env *Env, o *options) error {
	if o.console {
		return nil
	}

	log := realPath(o.logPath)
	zpaths := []string{"/tmp", "/var/log"}

	for _, d := range []string{"log", "data/tmp", procZmstat} {
		zpaths = append(zpaths, filepath.Join(env.Base, d))
	}

	for _, p := range zpaths {
		if rp, err := filepath.EvalSymlinks(p); err == nil && strings.HasPrefix(log, rp) {
			return nil
		}
	}

	return message("Error: Specified log file must be created in one of the following locations: " +
		strings.Join(zpaths, " ") + "\n\n")
}

// message is an error that is printed as it is, for the texts the Perl prints.
type message string

func (m message) Error() string { return string(m) }

// realPath is Cwd::realpath: symlinks resolved, the last component allowed to
// be missing.
func realPath(p string) string {
	p = filepath.Clean(p)
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}

	if rd, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(rd, filepath.Base(p))
	}

	return p
}

// fdOwn gives the log to zextras with mode 0640 (the collector may be run by
// root).
func fdOwn(env *Env, path string) {
	if env.Chown != nil {
		_ = env.Chown(path)
	}

	_ = os.Chmod(path, 0o640)
}
