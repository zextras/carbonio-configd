// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// procTree is the process tree of /proc: get_pid_tree of stat-allprocs.pl and
// stat-nginx.pl.
type procTree struct {
	children map[int][]int
	names    map[int]string
}

// pidStat holds the numbers collect_stats reads of one process: ppid, utime,
// stime, threads and rss (kB) from stat, and every key of io (rchar, wchar,
// read_bytes, write_bytes, ...). A key that is missing was not readable.
type pidStat map[string]int64

var (
	statusPPid = regexp.MustCompile(`^PPid:\s+(\d+)`)
	statusName = regexp.MustCompile(`^Name:\s+(\S+)`)
)

// scanTree lists the processes with their parents; with names it also reads
// the process name, the way stat-allprocs.pl names a process (a "configd"
// that was started as bin/configd is zmconfigd, a java with jetty.xml as its
// last argument is zmmailboxd).
func (e *Env) scanTree(withNames bool) *procTree {
	t := &procTree{children: map[int][]int{}, names: map[int]string{}}

	entries, _ := fs.ReadDir(e.Root, "proc")

	for _, ent := range entries {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || strconv.Itoa(pid) != ent.Name() {
			continue
		}

		status, err := fs.ReadFile(e.Root, "proc/"+ent.Name()+"/status")
		if err != nil {
			continue // the process has gone away
		}

		ppid, name := parseStatus(string(status))
		if ppid < 0 || withNames && name == "" {
			continue
		}

		if withNames {
			name = e.processName(pid, name)
			t.names[pid] = name
		}

		t.children[ppid] = append(t.children[ppid], pid)
	}

	return t
}

// parseStatus is the PPid and the Name of a /proc/<pid>/status; ppid is -1
// when there is none.
func parseStatus(status string) (ppid int, name string) {
	ppid = -1

	for line := range strings.SplitSeq(status, "\n") {
		if m := statusPPid.FindStringSubmatch(line); m != nil {
			ppid, _ = strconv.Atoi(m[1])
		} else if m := statusName.FindStringSubmatch(line); m != nil {
			name = m[1]
		}
	}

	return ppid, name
}

func (e *Env) processName(pid int, name string) string {
	switch {
	case name == "configd":
		if args := e.cmdline(pid); len(args) > 0 && strings.Contains(args[0], "/opt/zextras/bin/configd") {
			return "zmconfigd"
		}
	case strings.Contains(name, "java"):
		if args := e.cmdline(pid); len(args) > 0 &&
			strings.Contains(args[len(args)-1], "/opt/zextras/mailboxd/etc/jetty.xml") {
			return "zmmailboxd"
		}
	}

	return name
}

// cmdline is the argument vector of a process (nil when unreadable).
func (e *Env) cmdline(pid int) []string {
	data, err := fs.ReadFile(e.Root, "proc/"+strconv.Itoa(pid)+"/cmdline")
	if err != nil || len(data) == 0 {
		return nil
	}

	return strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
}

// collect is collect_stats: the numbers of pid and of all its descendants.
func (e *Env) collect(pid int, t *procTree, out map[int]pidStat) {
	if _, seen := out[pid]; seen {
		return
	}

	st := pidStat{}

	if data, err := fs.ReadFile(e.Root, "proc/"+strconv.Itoa(pid)+"/stat"); err == nil {
		parseStat(string(data), st)
		out[pid] = st
	}

	if data, err := fs.ReadFile(e.Root, "proc/"+strconv.Itoa(pid)+"/io"); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok {
				n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				st[k] = n
			}
		}

		out[pid] = st
	}

	for _, child := range t.children[pid] {
		e.collect(child, t, out)
	}
}

// parseStat reads the fields of /proc/<pid>/stat the collectors use. The
// command is skipped up to its closing parenthesis, so a name with spaces does
// not shift the fields (the Perl splits on blanks and does).
func parseStat(data string, st pidStat) {
	_, after, ok := strings.CutLast(data, ")")
	if !ok {
		return
	}

	rest := strings.Fields(after)
	field := func(i int) int64 { // i counts from the pid as field 0
		if j := i - 2; j >= 0 && j < len(rest) {
			n, _ := strconv.ParseInt(rest[j], 10, 64)

			return n
		}

		return 0
	}

	st["ppid"] = field(3)
	st[colUtime] = field(13)
	st[colStime] = field(14)
	st["threads"] = field(19)
	st["rss"] = field(23) * 4
}

// delta is delta_stat: the change of a number since the previous sample, the
// whole value for a number that was not there before, 0 for one that is not
// there now.
func delta(prev, cur map[int]pidStat, pid int, key string) int64 {
	c, ok := cur[pid][key]
	if !ok {
		return 0
	}

	p, ok := prev[pid][key]
	if !ok {
		return c
	}

	return c - p
}

// usageKeys are the counters summed per process name or tree: CPU always, I/O
// when the kernel has per-process I/O accounting.
func usageKeys(hasIO bool) []string {
	if hasIO {
		return []string{colUtime, colStime, "rchar", "wchar", "read_bytes", "write_bytes"}
	}

	return []string{colUtime, colStime}
}

// hasIOAccounting is -f /proc/self/io.
func (e *Env) hasIOAccounting() bool {
	_, err := fs.Stat(e.Root, "proc/self/io")

	return err == nil
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
