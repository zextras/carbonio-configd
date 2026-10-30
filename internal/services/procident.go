// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Process identity (legacy, non-systemd mode).
//
// A service's processes are recognised from what the kernel records about
// them, never from a substring of the command line. ServiceDef.ProcessName is
// the identity: an exact name (or glob over a whole path, e.g.
// "/opt/zextras/libexec/zmstat-*") compared with
//
//  1. /proc/<pid>/exe, when readable (absolute names only). The kernel's own
//     record of the running binary; immune to argv rewriting and recycled
//     PIDs. Other users' exe links are unreadable without privileges, so this
//     tier is best-effort.
//  2. the argv tokens (cmdline split on NUL and whitespace, which also
//     un-rewrites process titles such as "nginx: master process
//     /opt/zextras/common/sbin/nginx -c ..."). A token must EQUAL the name:
//     argv0 for natively launched daemons, the script path for perl/sh
//     services, the main class for JVM services (java ... com.zextras.mailbox
//     .Mailbox). A name without a slash ("amavisd", a Java class) also
//     matches the basename of a token (minus the colon of a "name: role"
//     title), so a daemon that retitles itself "amavisd (master)" or
//     "nginx: worker process" is still found.
//  3. comm, only when argv is empty (zeroed after fork, kernel threads), and
//     only as an exact match of the name's basename (truncated to the 15
//     characters comm keeps).
//
// A process that merely mentions the service in an argument ("tail -f
// /opt/zextras/log/clamd.log", "consul ... -sidecar-for carbonio-memcached")
// matches none of these.

// commMaxLen is the longest process name the kernel keeps in comm
// (TASK_COMM_LEN-1).
const commMaxLen = 15

// procFSRoot is the path prefix for Linux process entries in /proc.
// Declared as var so tests can override it with a temporary directory.
var procFSRoot = "/proc/"

// procEntry is the identity-relevant view of one /proc/<pid> entry.
type procEntry struct {
	// argv is the cmdline split on NUL and whitespace.
	argv []string
	// exe is the resolved /proc/<pid>/exe target ("" when unreadable).
	exe string
	// comm is the kernel short name, read only when argv is empty (the only
	// case in which it is consulted).
	comm string
	pid  int
}

// isArgSep splits a raw cmdline into tokens at NUL (argv boundaries) and
// whitespace (title-rewriting daemons keep their whole title in one slot).
func isArgSep(r rune) bool { return r == 0 || unicode.IsSpace(r) }

// readProcEntry reads the /proc entry e. It reports false for non-numeric
// names and non-directories. Ownership is intentionally NOT checked: services
// we register legitimately run as root (postfix master, sudo-spawned
// zmstat-fd) and must be visible to status. Cross-UID signaling is handled by
// the caller via sudo fallback in killByPIDWithGroupAndSudo.
func readProcEntry(e os.DirEntry) (procEntry, bool) {
	if !e.IsDir() {
		return procEntry{}, false
	}

	pid, convErr := strconv.Atoi(e.Name())
	if convErr != nil {
		return procEntry{}, false
	}

	return loadProcEntry(pid), true
}

// loadProcEntry reads exe, cmdline (and comm when cmdline is empty) of pid.
// Unreadable files leave the corresponding field empty; the caller decides
// whether the pid exists.
func loadProcEntry(pid int) procEntry {
	procDir := procFSRoot + strconv.Itoa(pid)
	pe := procEntry{pid: pid}

	if target, err := os.Readlink(procDir + "/exe"); err == nil {
		pe.exe = strings.TrimSuffix(target, " (deleted)")
	}

	data, readErr := os.ReadFile(procDir + "/cmdline") //nolint:gosec // path is /proc/<pid>/cmdline, not user-controlled
	if readErr == nil {
		pe.argv = strings.FieldsFunc(string(data), isArgSep)
	}

	if len(pe.argv) > 0 {
		return pe
	}

	comm, readErr := os.ReadFile(procDir + "/comm") //nolint:gosec // path is /proc/<pid>/comm, not user-controlled
	if readErr == nil {
		pe.comm = strings.TrimSpace(string(comm))
	}

	return pe
}

// nameMatches compares one identity name with one observed string. Names
// carrying glob metacharacters are matched with path.Match (whole string,
// '*' never crosses a '/'); everything else must be equal.
func nameMatches(name, s string) bool {
	if strings.ContainsAny(name, "*?[") {
		ok, err := path.Match(name, s)

		return err == nil && ok
	}

	return name == s
}

// commName is the comm value a process named name would carry: the basename,
// cut to the kernel's 15 characters.
func commName(name string) string {
	base := path.Base(name)
	if !strings.ContainsAny(base, "*?[") && len(base) > commMaxLen {
		return base[:commMaxLen]
	}

	return base
}

// matches reports whether the entry is a process of the given identity name.
// See the package-level notes above for the three tiers.
//
// argv is authoritative when present: a negative argv verdict must NOT fall
// through to comm, because the 15-char comm truncation collides unrelated
// processes. Example: service-discover-wrapper.sh has comm="service-discove",
// a prefix of the daemon binary's name (serviceDiscoverBinary).
func (p procEntry) matches(name string) bool {
	if name == "" {
		return false
	}

	absolute := strings.HasPrefix(name, "/")

	if absolute && p.exe != "" && nameMatches(name, p.exe) {
		return true
	}

	if len(p.argv) == 0 {
		return p.comm != "" && nameMatches(commName(name), p.comm)
	}

	for _, tok := range p.argv {
		if nameMatches(name, tok) || (!absolute && nameMatches(name, bareToken(tok))) {
			return true
		}
	}

	return false
}

// bareToken is how a slash-less identity name sees an argv token: its
// basename, without the trailing colon of a "name: role" process title
// ("nginx: worker process").
func bareToken(tok string) string {
	return strings.TrimSuffix(path.Base(tok), ":")
}

// procSnapshot is a point-in-time listing of procFSRoot, read once and
// matched against any number of identity names. A status listing or watchdog
// tick probes N services; without a snapshot each probe would re-walk /proc
// and re-read every cmdline. The listing is loaded lazily on first use and is
// safe for concurrent use. Entries are ordered by PID so every answer derived
// from it (first match, lookup) is deterministic.
//
// Snapshots are for read-only status probes. Anything that signals a process
// must use scanProcesses, which always takes a fresh listing.
type procSnapshot struct {
	err     error
	entries []procEntry
	once    sync.Once
}

func (s *procSnapshot) load() {
	s.once.Do(func() {
		dirents, err := os.ReadDir(procFSRoot)
		if err != nil {
			s.err = fmt.Errorf("read %s: %w", procFSRoot, err)

			return
		}

		s.entries = make([]procEntry, 0, len(dirents))

		for _, e := range dirents {
			if pe, ok := readProcEntry(e); ok {
				s.entries = append(s.entries, pe)
			}
		}

		slices.SortFunc(s.entries, func(a, b procEntry) int { return a.pid - b.pid })
	})
}

// scan returns, in PID order, the PIDs of live (non-zombie) processes of the
// given identity. Zombies have already exited and cannot be signaled — never
// reported. The zombie check runs only for matches, against the current state.
func (s *procSnapshot) scan(name string) ([]int, error) {
	s.load()

	if s.err != nil {
		return nil, s.err
	}

	var pids []int

	for i := range s.entries {
		if s.entries[i].matches(name) && !isZombie(s.entries[i].pid) {
			pids = append(pids, s.entries[i].pid)
		}
	}

	return pids, nil
}

// lookup returns the snapshot's entry for pid.
func (s *procSnapshot) lookup(pid int) (procEntry, bool) {
	s.load()

	if s.err != nil {
		return procEntry{}, false
	}

	i, found := slices.BinarySearchFunc(s.entries, pid, func(e procEntry, pid int) int { return e.pid - pid })
	if !found {
		return procEntry{}, false
	}

	return s.entries[i], true
}

type procSnapshotKey struct{}

// WithProcessSnapshot returns a context carrying a process snapshot that
// every legacy-mode ServiceStatus / RunningPID probe made with it shares, so
// a batch of probes (zmcontrol status, a watchdog tick) walks /proc once
// instead of once per service and answers all of them from one consistent
// view. The snapshot is taken lazily on the first probe that needs it and
// never refreshed: derive the context per batch, not per daemon lifetime. A
// context that already carries one is returned unchanged.
func WithProcessSnapshot(ctx context.Context) context.Context {
	if snapshotFrom(ctx) != nil {
		return ctx
	}

	return context.WithValue(ctx, procSnapshotKey{}, &procSnapshot{})
}

func snapshotFrom(ctx context.Context) *procSnapshot {
	s, _ := ctx.Value(procSnapshotKey{}).(*procSnapshot)

	return s
}

// scanProcesses lists the live processes of the given identity from a fresh
// /proc listing, in PID order. Kill paths use it: signaling must always see
// live state.
func scanProcesses(name string) ([]int, error) {
	return (&procSnapshot{}).scan(name)
}

// scanProcessesForStatus is scanProcesses for read-only status probes: it
// matches against the snapshot carried by ctx (WithProcessSnapshot) when
// there is one, else against a fresh listing.
func scanProcessesForStatus(ctx context.Context, name string) ([]int, error) {
	if s := snapshotFrom(ctx); s != nil {
		return s.scan(name)
	}

	return scanProcesses(name)
}

// readPidFile parses the PID recorded in pidFile. ok is false when the file
// is unreadable (missing, permission denied — e.g. postfix master.pid is
// root:root 0600), empty or corrupt: the caller should fall through to an
// alternative detection method. Only the first line counts — some services
// (stats) write several PIDs, the first being the primary/orchestrator.
func readPidFile(pidFile string) (pid int, ok bool) {
	data, err := os.ReadFile(pidFile) //nolint:gosec // path from internal registry
	if err != nil {
		return 0, false
	}

	pidStr, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")

	pid, err = strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil || pid <= 0 {
		return 0, false
	}

	return pid, true
}

// isRunningByPidFile reports whether the PID in pidFile is alive. ok=false
// means the file gave no usable PID (see readPidFile). It checks liveness
// only: use pidBelongsTo when the service's identity is known.
func isRunningByPidFile(pidFile string) (running bool, ok bool) {
	pid, ok := readPidFile(pidFile)
	if !ok {
		return false, false
	}

	return processAlive(pid), true
}

// isZombie reports whether a process is in the zombie state (Z).
// Zombies have already exited — they appear in /proc until their parent reaps
// them, but they are not running. Returns false if /proc/<pid>/status is
// unreadable (caller assumes the process is alive in that case).
func isZombie(pid int) bool {
	data, err := os.ReadFile(procFSRoot + strconv.Itoa(pid) + "/status") //nolint:gosec // path is /proc/<pid>/status
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

// pidBelongsTo reports whether pid is a live process of the given identity —
// the validation that keeps a stale pidfile whose PID was recycled by an
// unrelated process from being believed. With a snapshot in ctx the verdict
// comes from that frozen view (like every other probe of the batch);
// otherwise only /proc/<pid> is read, with no full /proc walk. An empty
// identity means the service declares none, and liveness is all there is.
func pidBelongsTo(ctx context.Context, pid int, name string) bool {
	if name == "" {
		return processAlive(pid)
	}

	if s := snapshotFrom(ctx); s != nil {
		e, ok := s.lookup(pid)

		return ok && e.matches(name) && !isZombie(pid)
	}

	return processAlive(pid) && loadProcEntry(pid).matches(name)
}

// pidFromProcessName returns the lowest PID of a live process of the given
// identity that isn't the current process or its parent (so a `configd
// control status` invocation never matches its own ProcessName), or 0. It
// answers from the snapshot carried by ctx when present. Empty name
// short-circuits to 0.
func pidFromProcessName(ctx context.Context, name string) int {
	if name == "" {
		return 0
	}

	pids, err := scanProcessesForStatus(ctx, name)
	if err != nil {
		return 0
	}

	isSelfOrParent := selfOrParentFilter()

	for _, pid := range pids {
		if !isSelfOrParent(pid) {
			return pid
		}
	}

	return 0
}

// runningPID resolves the primary PID of a service in legacy mode, or 0 when
// it is not running. This is the single probe behind ServiceStatus and
// RunningPID, so status and the PID shown beside it never disagree:
//
//  1. the pidfile's PID, when readable and validated against the identity
//     (alive AND exe/argv match) — a stale pidfile naming a recycled PID of
//     another binary is ignored, not believed;
//  2. otherwise a scan of the process table for the identity (snapshot-aware).
//
// An unreadable pidfile (postfix master.pid is root:root 0600) or a stale one
// both fall through to the scan, as legacy amavisdctl/memcachedctl do.
func runningPID(ctx context.Context, def *ServiceDef) int {
	if def.PidFile != "" {
		if pid, ok := readPidFile(def.PidFile); ok && pidBelongsTo(ctx, pid, def.ProcessName) {
			return pid
		}
	}

	return pidFromProcessName(ctx, def.ProcessName)
}
