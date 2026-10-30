// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/sys"
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

// entry is the identity-relevant view of one process-table entry: Argv is the
// cmdline split on NUL and whitespace (title-rewriting daemons keep their whole
// title in one slot), Exe the resolved /proc/<pid>/exe target ("" when
// unreadable), Comm the kernel short name (read only when Argv is empty).
// Ownership is intentionally NOT checked by the table: services we register
// legitimately run as root (postfix master, sudo-spawned zmstat-fd) and must
// be visible to status. Cross-UID signaling is handled by the caller via the
// sudo fallback in killByPIDWithGroupAndSudo.
type entry = sys.ProcEntry

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

// entryMatches reports whether the entry is a process of the given identity. An
// identity is a program name (see the package-level notes above for the three
// tiers) optionally followed by space-separated required arguments, for a
// binary shared with unrelated processes: "/usr/bin/consul agent
// /etc/zextras/service-discover" is the consul agent of that config dir, not
// the "consul connect envoy" sidecars. Every required argument must be an argv
// token (or the value of a -flag=value token); a trailing slash is ignored.
func entryMatches(p *entry, identity string) bool {
	prog, required, _ := strings.Cut(identity, " ")

	if !matchesProgram(p, prog) {
		return false
	}

	for want := range strings.FieldsSeq(required) {
		if !slices.ContainsFunc(p.Argv, func(tok string) bool { return argMatches(want, tok) }) {
			return false
		}
	}

	return true
}

// argMatches reports whether argv token tok is, or carries as -flag=value, the
// required argument want. Trailing slashes are not significant.
func argMatches(want, tok string) bool {
	want = strings.TrimRight(want, "/")

	if nameMatches(want, strings.TrimRight(tok, "/")) {
		return true
	}

	_, value, ok := strings.Cut(tok, "=")

	return ok && nameMatches(want, strings.TrimRight(value, "/"))
}

// matchesProgram is the program-name part of entryMatches.
//
// argv is authoritative when present: a negative argv verdict must NOT fall
// through to comm, because the 15-char comm truncation collides unrelated
// processes. Example: service-discover-wrapper.sh has comm="service-discove",
// a prefix of the name of the daemon binary the wrapper fronts.
func matchesProgram(p *entry, name string) bool {
	if name == "" {
		return false
	}

	absolute := strings.HasPrefix(name, "/")

	if absolute && p.Exe != "" && nameMatches(name, p.Exe) {
		return true
	}

	if len(p.Argv) == 0 {
		return p.Comm != "" && nameMatches(commName(name), p.Comm)
	}

	for _, tok := range p.Argv {
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

// procSnapshot is a point-in-time listing of the process table, read once and
// matched against any number of identity names. A status listing or watchdog
// tick probes N services; without a snapshot each probe would re-walk /proc
// and re-read every cmdline. The listing is loaded lazily on first use and is
// safe for concurrent use. Entries are ordered by PID so every answer derived
// from it (first match, lookup) is deterministic.
//
// Snapshots are for read-only status probes. Anything that signals a process
// must use scanProcesses, which always takes a fresh listing.
type procSnapshot struct {
	proc    sys.Proc
	err     error
	entries []entry
	once    sync.Once
}

func (s *procSnapshot) load() {
	s.once.Do(func() {
		s.entries, s.err = s.proc.Table()
		if s.err != nil {
			return
		}

		slices.SortFunc(s.entries, func(a, b entry) int { return a.PID - b.PID })
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
		if entryMatches(&s.entries[i], name) && !s.proc.Zombie(s.entries[i].PID) {
			pids = append(pids, s.entries[i].PID)
		}
	}

	return pids, nil
}

// lookup returns the snapshot's entry for pid.
func (s *procSnapshot) lookup(pid int) (entry, bool) {
	s.load()

	if s.err != nil {
		return entry{}, false
	}

	i, found := slices.BinarySearchFunc(s.entries, pid, func(e entry, pid int) int { return e.PID - pid })
	if !found {
		return entry{}, false
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
func (c *Controller) WithProcessSnapshot(ctx context.Context) context.Context {
	if snapshotFrom(ctx) != nil {
		return ctx
	}

	return context.WithValue(ctx, procSnapshotKey{}, &procSnapshot{proc: c.env.Proc})
}

func snapshotFrom(ctx context.Context) *procSnapshot {
	s, _ := ctx.Value(procSnapshotKey{}).(*procSnapshot)

	return s
}

// scanProcesses lists the live processes of the given identity from a fresh
// /proc listing, in PID order. Kill paths use it: signaling must always see
// live state.
func (c *Controller) scanProcesses(name string) ([]int, error) {
	return (&procSnapshot{proc: c.env.Proc}).scan(name)
}

// scanProcessesForStatus is scanProcesses for read-only status probes: it
// matches against the snapshot carried by ctx (WithProcessSnapshot) when
// there is one, else against a fresh listing.
func (c *Controller) scanProcessesForStatus(ctx context.Context, name string) ([]int, error) {
	if s := snapshotFrom(ctx); s != nil {
		return s.scan(name)
	}

	return c.scanProcesses(name)
}

// readPidFile parses the PID recorded in pidFile. ok is false when the file
// is unreadable (missing, permission denied — e.g. postfix master.pid is
// root:root 0600), empty or corrupt: the caller should fall through to an
// alternative detection method. Only the first line counts — some services
// (stats) write several PIDs, the first being the primary/orchestrator.
func (c *Controller) readPidFile(pidFile string) (pid int, ok bool) {
	data, err := c.env.FS.ReadFile(pidFile)
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
func (c *Controller) isRunningByPidFile(pidFile string) (running bool, ok bool) {
	pid, ok := c.readPidFile(pidFile)
	if !ok {
		return false, false
	}

	return c.processAlive(pid), true
}

// pidBelongsTo reports whether pid is a live process of the given identity —
// the validation that keeps a stale pidfile whose PID was recycled by an
// unrelated process from being believed. With a snapshot in ctx the verdict
// comes from that frozen view (like every other probe of the batch);
// otherwise only /proc/<pid> is read, with no full /proc walk. An empty
// identity means the service declares none, and liveness is all there is.
func (c *Controller) pidBelongsTo(ctx context.Context, pid int, name string) bool {
	if name == "" {
		return c.processAlive(pid)
	}

	if s := snapshotFrom(ctx); s != nil {
		e, ok := s.lookup(pid)

		return ok && entryMatches(&e, name) && !c.env.Proc.Zombie(pid)
	}

	e, ok := c.env.Proc.Entry(pid)

	return ok && !c.env.Proc.Zombie(pid) && entryMatches(&e, name)
}

// pidFromProcessName returns the lowest PID of a live process of the given
// identity that isn't the current process or its parent (so a `configd
// control status` invocation never matches its own ProcessName), or 0. It
// answers from the snapshot carried by ctx when present. Empty name
// short-circuits to 0.
func (c *Controller) pidFromProcessName(ctx context.Context, name string) int {
	if name == "" {
		return 0
	}

	pids, err := c.scanProcessesForStatus(ctx, name)
	if err != nil {
		return 0
	}

	isSelfOrParent := c.selfOrParentFilter()

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
func (c *Controller) runningPID(ctx context.Context, def *ServiceDef) int {
	if def.PidFile != "" {
		if pid, ok := c.readPidFile(def.PidFile); ok && c.pidBelongsTo(ctx, pid, def.ProcessName) {
			return pid
		}
	}

	return c.pidFromProcessName(ctx, def.ProcessName)
}
