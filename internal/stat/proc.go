// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const procUsage = `Usage: zmstat-proc [options]
Monitor various Carbonio processes
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/proc.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when HUP signal is sent or when
date changes.  Current log is renamed to <dir>/YYYY-MM-DD/proc.csv
and a new file is created.
`

const postfixUser = "postfix"

// defaultProcs are monitored when the enabled services cannot be read.
var defaultProcs = []string{"mailbox", procMySQL, procLDAP, "postfix", "amavis", "clam", procStat}

// procMatchers are the command line filters of the monitored process groups
// (the %ps_subs of stat-proc.pl): the user that owns them, the expressions the
// command must match and the one it must not. The zmstat group also matches
// "setup zmstat", how the collectors run now that they are not zmstat-*
// scripts.
var procMatchers = map[string]struct {
	postfix bool
	all     []*regexp.Regexp
	not     *regexp.Regexp
}{
	"mailbox": {all: []*regexp.Regexp{regexp.MustCompile(`jetty_home/start[.]jar`)}},
	procMySQL: {
		all: []*regexp.Regexp{regexp.MustCompile(`/(libexec|bin)/mysqld `), regexp.MustCompile(`/my\.cnf`)},
		not: regexp.MustCompile(`logger`),
	},
	procLDAP:  {all: []*regexp.Regexp{regexp.MustCompile(`slapd`)}},
	"postfix": {postfix: true},
	"amavis":  {all: []*regexp.Regexp{regexp.MustCompile(`amavisd`)}},
	"clam":    {all: []*regexp.Regexp{regexp.MustCompile(`(freshclam|clamd)`)}},
	procStat: {all: []*regexp.Regexp{
		regexp.MustCompile(`(zmstat-|setup zmstat |iostat|vmstat|vm_stat|postqueue)`),
	}},
}

func procSpec() *spec {
	return &spec{
		name:  "proc",
		files: fixedFiles("proc.csv", "proc.pid"),
		usage: procUsage,
		build: func(env *Env, o *options) (Collector, error) { return newProc(env, o.args), nil },
	}
}

// psProc is a line of ps axww -o user,pid,command.
type psProc struct {
	user, command string
	pid           int
}

// procEntry is one monitored process group: the last reading of its CPU
// counters, and the figures of the interval.
type procEntry struct {
	name  string
	cur   [6]float64
	delta [6]float64
}

// proc is stat-proc.pl: CPU share and memory of the Carbonio process groups
// (each of which is a set of processes picked by user and command line), and
// of the whole system.
type proc struct {
	env     *Env
	heading string
	entries []*procEntry
	sys     [5]float64 // user+nice, system, idle, iowait, total jiffies
	sysDiff [5]float64
	started bool
}

func newProc(env *Env, args []string) *proc {
	names := args
	if len(names) == 0 {
		names = env.enabledProcs()
	}

	p := &proc{env: env}

	for _, n := range names {
		if _, ok := procMatchers[n]; !ok {
			_, _ = fmt.Fprintf(env.Stderr, "Warning: Not possible to monitor process %s\n", n)

			continue
		}

		p.entries = append(p.entries, &procEntry{name: n})
	}

	cols := []string{colTimestamp, colSystem, "user", "sys", "idle", "iowait"}

	for _, e := range p.entries {
		cols = append(cols, e.name, e.name+"-total-cpu", e.name+"-utime", e.name+"-stime",
			e.name+"-totalMB", e.name+"-rssMB", e.name+"-sharedMB", e.name+"-process-count")
	}

	p.heading = strings.Join(cols, ", ")

	return p
}

// enabledProcs is getEnabledProcs: the process groups of the services this
// server runs (zimbraServiceEnabled). When the services cannot be read all
// groups are monitored.
func (e *Env) enabledProcs() []string {
	if e.Enabled == nil {
		return defaultProcs
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	svcs, err := e.Enabled(ctx, e.LC["zimbra_server_hostname"])
	if err != nil {
		_, _ = fmt.Fprintf(e.Stderr, "zmstat proc: cannot read the enabled services: %v\n", err)

		return defaultProcs
	}

	has := func(words ...string) bool {
		return slices.ContainsFunc(svcs, func(s string) bool {
			return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(s, w) })
		})
	}

	var procs []string

	if has("mailbox") {
		procs = append(procs, "mailbox", procMySQL)
	}

	if has(procLDAP) {
		procs = append(procs, procLDAP)
	}

	if has("mta") {
		procs = append(procs, "postfix")
	}

	if has("antispam", "antivirus") {
		procs = append(procs, "amavis", "clam")
	}

	return append(procs, procStat)
}

// Heading implements Collector.
func (p *proc) Heading() string { return p.heading }

// snapshot is snapshotProcs: the processes of the Carbonio and postfix users
// by pid, with their command line as ps shows it. (The Perl also meant to
// count the root-owned postfix master, but its pattern is a quoted variable
// that never matches; it is not counted here either.)
func (p *proc) snapshot() []psProc {
	zuser := p.env.LC["zimbra_user"]
	if zuser == "" {
		zuser = defaultUser
	}

	entries, _ := fs.ReadDir(p.env.Root, "proc")

	var out []psProc

	for _, ent := range entries {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || strconv.Itoa(pid) != ent.Name() {
			continue
		}

		user := p.env.owner(pid)
		if user != zuser && user != postfixUser {
			continue
		}

		out = append(out, psProc{user: user, pid: pid, command: p.env.command(pid)})
	}

	slices.SortFunc(out, func(a, b psProc) int { return a.pid - b.pid })

	return out
}

// owner is the user of a process (its effective uid in /proc/<pid>/status).
func (e *Env) owner(pid int) string {
	data, err := fs.ReadFile(e.Root, "proc/"+strconv.Itoa(pid)+"/status")
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}

		f := strings.Fields(rest)
		if len(f) < 2 {
			return ""
		}

		uid, err := strconv.Atoi(f[1])
		if err != nil {
			return ""
		}

		if e.UserName != nil {
			return e.UserName(uid)
		}

		return strconv.Itoa(uid)
	}

	return ""
}

// command is the COMMAND column of ps axww: the arguments, or the process name
// in brackets for a process that has none (a kernel thread).
func (e *Env) command(pid int) string {
	if args := e.cmdline(pid); len(args) > 0 {
		return strings.Join(args, " ")
	}

	data, _ := fs.ReadFile(e.Root, "proc/"+strconv.Itoa(pid)+"/comm")

	return "[" + strings.TrimSpace(string(data)) + "]"
}

// pick is the filterProcs of a group.
func pick(name, zuser string, all []psProc) []psProc {
	m := procMatchers[name]
	user := zuser

	if m.postfix {
		user = postfixUser
	}

	var out []psProc

next:
	for _, ps := range all {
		if ps.user != user {
			continue
		}

		for _, re := range m.all {
			if !re.MatchString(ps.command) {
				continue next
			}
		}

		if m.not != nil && m.not.MatchString(ps.command) {
			continue
		}

		out = append(out, ps)
	}

	return out
}

// Sample implements Collector. The first line has no interval to measure and
// shows zero rates.
func (p *proc) Sample(t Tick) ([]string, error) {
	all := p.snapshot()

	zuser := p.env.LC["zimbra_user"]
	if zuser == "" {
		zuser = defaultUser
	}

	elapsed := p.updateSystem()

	for _, e := range p.entries {
		p.update(e, pick(e.name, zuser, all), elapsed, t.Stamp)
	}

	p.started = true

	vals := append([]string{t.Stamp}, p.systemColumns()...)
	for _, e := range p.entries {
		vals = append(vals, e.columns()...)
	}

	return []string{strings.Join(vals, ", ")}, nil
}

// computeDelta is computeDelta of the Perl: the new reading and its difference
// to the old (0 when the counter went backwards).
func computeDelta(prev, now float64) (cur, d float64) {
	if now >= prev {
		return now, now - prev
	}

	return now, 0
}

// updateSystem reads /proc/stat and returns the jiffies of the interval.
func (p *proc) updateSystem() float64 {
	stat := p.readSystem()

	for i := range stat {
		if !p.started {
			p.sys[i], p.sysDiff[i] = stat[i], 0

			continue
		}

		p.sys[i], p.sysDiff[i] = computeDelta(p.sys[i], stat[i])
	}

	return p.sysDiff[4]
}

// readSystem is _getSystemStat: user+nice, system, idle, iowait and their
// sum from the first line of /proc/stat.
func (p *proc) readSystem() [5]float64 {
	var out [5]float64

	data, err := fs.ReadFile(p.env.Root, "proc/stat")
	if err != nil {
		_, _ = fmt.Fprintf(p.env.Stderr, "ERROR: Can't read /proc/stat\n")

		return out
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}

		f := strings.Fields(line)
		n := func(i int) float64 {
			if i >= len(f) {
				return 0
			}

			v, _ := strconv.ParseFloat(f[i], 64)

			return v
		}

		out = [5]float64{n(1) + n(2), n(3), n(4), n(5), n(1) + n(2) + n(3) + n(4) + n(5)}

		break
	}

	return out
}

// systemColumns is getSystemStat: percentages of the interval, a plain 0 when
// no time elapsed.
func (p *proc) systemColumns() []string {
	total := p.sysDiff[4]
	if total <= 0 {
		return []string{colSystem, "0", "0", "0", "0"}
	}

	pct := func(v float64) string { return strconv.FormatFloat(v*100/total, 'f', 1, 64) }

	return []string{colSystem, pct(p.sysDiff[0]), pct(p.sysDiff[1]), pct(p.sysDiff[2]), pct(p.sysDiff[3])}
}

// update is updateProcessStat.
func (p *proc) update(e *procEntry, procs []psProc, elapsed float64, stamp string) {
	now := p.listStat(procs, stamp)

	for col := range 2 {
		var d float64

		e.cur[col], d = computeDelta(e.cur[col], now[col])
		if !p.started || elapsed <= 0 {
			d = 0
		} else {
			d = d * 100 / elapsed
		}

		e.delta[col] = d
	}

	for col := 2; col < 6; col++ {
		e.cur[col], e.delta[col] = now[col], now[col]
	}
}

// listStat is _getProcessListStat: CPU jiffies summed over the processes, the
// memory of the last of them, and how many there are.
func (p *proc) listStat(procs []psProc, stamp string) [6]float64 {
	var sum [6]float64

	for _, ps := range procs {
		st := p.processStat(ps.pid, stamp)
		sum[0] += st[0]
		sum[1] += st[1]
		sum[2], sum[3], sum[4] = st[2], st[3], st[4]
	}

	sum[5] = float64(len(procs))

	return sum
}

// processStat is _getProcessStat: utime, stime and the sizes in MB from stat
// and statm of a process.
func (p *proc) processStat(pid int, stamp string) [6]float64 {
	var zero [6]float64

	read := func(file string) []string {
		data, err := fs.ReadFile(p.env.Root, "proc/"+strconv.Itoa(pid)+"/"+file)
		if err != nil {
			_, _ = fmt.Fprintf(p.env.Stderr, "%s: ERROR: No such process: '%d'\n", stamp, pid)

			return nil
		}

		if file == "stat" { // skip the command, which may hold blanks
			if rp := strings.LastIndexByte(string(data), ')'); rp >= 0 {
				return append([]string{"", ""}, strings.Fields(string(data)[rp+1:])...)
			}
		}

		return strings.Fields(string(data))
	}

	stat := read("stat")
	if stat == nil {
		return zero
	}

	statm := read("statm")
	if statm == nil {
		return zero
	}

	n := func(f []string, i int) float64 {
		if i >= len(f) {
			return 0
		}

		v, _ := strconv.ParseFloat(f[i], 64)

		return v
	}

	return [6]float64{
		n(stat, 13), n(stat, 14),
		n(statm, 0) * 4 / 1024, n(statm, 1) * 4 / 1024, n(statm, 2) * 4 / 1024, 1,
	}
}

// columns is getProcessStat: the values of a group after the timestamp.
func (e *procEntry) columns() []string {
	f1 := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	utime, stime := f1(e.delta[0]), f1(e.delta[1])
	u, _ := strconv.ParseFloat(utime, 64)
	s, _ := strconv.ParseFloat(stime, 64)

	return []string{
		e.name, f1(u + s), utime, stime,
		f1(e.delta[2]), f1(e.delta[3]), f1(e.delta[4]), strconv.FormatFloat(e.delta[5], 'f', -1, 64),
	}
}
