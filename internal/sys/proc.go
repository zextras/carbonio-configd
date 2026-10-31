// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys

import (
	"fmt"
	"os"
	"os/user"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

// ProcEntry is the identity-relevant view of one process-table entry.
type ProcEntry struct {
	// PID is the process id.
	PID int
	// Argv is the command line split on NUL and whitespace (whitespace
	// splitting un-rewrites process titles such as "nginx: master process
	// /opt/zextras/common/sbin/nginx -c ...").
	Argv []string
	// Exe is the resolved /proc/<pid>/exe target ("" when unreadable, which
	// is the rule for other users' processes without privileges).
	Exe string
	// Comm is the kernel short name, filled only when Argv is empty (the
	// only case in which it is consulted).
	Comm string
}

// Proc is the process boundary: the process table, signals, and the identity
// of the running process.
type Proc interface {
	// Table lists every numeric /proc entry in PID order. Ownership is
	// intentionally not checked: root-owned services must be visible.
	Table() ([]ProcEntry, error)
	// Entry reads one entry; ok is false when the pid has no entry.
	Entry(pid int) (ProcEntry, bool)
	// Alive reports whether the pid has an entry that is not a zombie.
	Alive(pid int) bool
	// Zombie reports whether the pid is in state Z; false when unreadable.
	Zombie(pid int) bool

	// Signal sends sig to pid. Errors satisfy errors.Is(err, syscall.ESRCH)
	// or errors.Is(err, os.ErrProcessDone) when the process is gone, and
	// syscall.EPERM when it belongs to another user.
	Signal(pid int, sig syscall.Signal) error
	// SignalGroup sends sig to the process group led by pgid.
	SignalGroup(pgid int, sig syscall.Signal) error

	// Pid, Ppid and UID identify the running process.
	Pid() int
	Ppid() int
	UID() int
	// Umask returns the process umask without disturbing it.
	Umask() os.FileMode
	// LookupGID resolves a group name to its gid.
	LookupGID(group string) (int, error)
}

// RealProc reads the Linux /proc filesystem mounted at Root.
type RealProc struct{ root string }

// NewRealProc returns a Proc reading the process table under root ("/proc/").
func NewRealProc(root string) RealProc {
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}

	return RealProc{root: root}
}

func isArgSep(r rune) bool { return r == 0 || unicode.IsSpace(r) }

// Table implements Proc.
func (p RealProc) Table() ([]ProcEntry, error) {
	dirents, err := os.ReadDir(p.root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.root, err)
	}

	entries := make([]ProcEntry, 0, len(dirents))

	for _, e := range dirents {
		if !e.IsDir() {
			continue
		}

		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil {
			continue
		}

		entries = append(entries, p.load(pid))
	}

	slices.SortFunc(entries, func(a, b ProcEntry) int { return a.PID - b.PID })

	return entries, nil
}

// Entry implements Proc.
func (p RealProc) Entry(pid int) (ProcEntry, bool) {
	if _, err := os.Stat(p.root + strconv.Itoa(pid)); err != nil {
		return ProcEntry{}, false
	}

	return p.load(pid), true
}

// load reads exe, cmdline (and comm when cmdline is empty) of pid.
func (p RealProc) load(pid int) ProcEntry {
	dir := p.root + strconv.Itoa(pid)
	pe := ProcEntry{PID: pid}

	if target, err := os.Readlink(dir + "/exe"); err == nil {
		pe.Exe = strings.TrimSuffix(target, " (deleted)")
	}

	if data, err := os.ReadFile(dir + "/cmdline"); err == nil { //nolint:gosec // /proc/<pid>/cmdline
		pe.Argv = strings.FieldsFunc(string(data), isArgSep)
	}

	if len(pe.Argv) > 0 {
		return pe
	}

	if comm, err := os.ReadFile(dir + "/comm"); err == nil { //nolint:gosec // /proc/<pid>/comm
		pe.Comm = strings.TrimSpace(string(comm))
	}

	return pe
}

// Alive implements Proc.
func (p RealProc) Alive(pid int) bool {
	if _, err := os.Stat(p.root + strconv.Itoa(pid)); err != nil { //nolint:gosec // /proc/<pid>
		return false
	}

	return !p.Zombie(pid)
}

// Zombie implements Proc.
func (p RealProc) Zombie(pid int) bool {
	data, err := os.ReadFile(p.root + strconv.Itoa(pid) + "/status") //nolint:gosec // /proc/<pid>/status
	if err != nil {
		return false
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if state, ok := strings.CutPrefix(line, "State:"); ok {
			return strings.Contains(state, "Z")
		}
	}

	return false
}

// Signal implements Proc.
func (RealProc) Signal(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	return proc.Signal(sig) //nolint:wrapcheck // callers match ESRCH/ErrProcessDone/EPERM
}

// SignalGroup implements Proc.
func (RealProc) SignalGroup(pgid int, sig syscall.Signal) error {
	return syscall.Kill(-pgid, sig) //nolint:wrapcheck // callers match ESRCH/EPERM
}

// Pid implements Proc.
func (RealProc) Pid() int { return os.Getpid() }

// Ppid implements Proc.
func (RealProc) Ppid() int { return os.Getppid() }

// UID implements Proc.
func (RealProc) UID() int { return os.Getuid() }

// umaskLine matches the "Umask:" field of /proc/self/status (Linux >= 4.7).
var umaskLine = regexp.MustCompile(`(?m)^Umask:\s*([0-7]+)\s*$`)

// Umask implements Proc. It reads /proc/self/status, which does not touch the
// process-wide umask and is therefore race-free against concurrent file
// creation. Only when that is unavailable does it fall back to
// syscall.Umask(0) followed by an immediate restore.
func (p RealProc) Umask() os.FileMode {
	if b, err := os.ReadFile(p.root + "self/status"); err == nil { //nolint:gosec // /proc/self/status
		if m := umaskLine.FindSubmatch(b); m != nil {
			if v, err := strconv.ParseUint(string(m[1]), 8, 32); err == nil {
				return os.FileMode(v) //nolint:gosec // G115: umask is at most 0777
			}
		}
	}

	old := syscall.Umask(0)
	syscall.Umask(old)

	return os.FileMode(old) //nolint:gosec // G115: umask is at most 0777
}

// LookupGID implements Proc.
func (RealProc) LookupGID(group string) (int, error) {
	g, err := user.LookupGroup(group)
	if err != nil {
		return 0, fmt.Errorf("lookup group %s: %w", group, err)
	}

	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, fmt.Errorf("group %s: invalid gid %q: %w", group, g.Gid, err)
	}

	return gid, nil
}
