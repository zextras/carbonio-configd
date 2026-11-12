// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const vmUsage = `Usage: zmstat-vm [options]
Monitor vmstat info
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/vm.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when HUP signal is sent or when
date changes.  Current log is renamed to <dir>/YYYY-MM-DD/vm.csv
and a new file is created.
`

// vmstatColumns are the columns of "vmstat -n -S K" (procps 3.3, without the
// guest column of procps 4), which zmstat-vm took from its heading line.
var vmstatColumns = []string{
	"r", "b", "swpd", "free", "buff", "cache", "si", "so", "bi", "bo", "in", "cs", "us", "sy", "id", "wa", "st",
}

func vmSpec() *spec {
	return &spec{
		name:  "vm",
		files: fixedFiles("vm.csv", "vm.pid"),
		usage: vmUsage,
		build: func(env *Env, o *options) (Collector, error) { return newVM(env, o) },
	}
}

// vmSample is one reading of the counters vmstat differentiates.
type vmSample struct {
	// cpu is user+nice, system+irq+softirq, idle, iowait, steal ticks.
	use, sys, idle, iow, steal int64
	intr, ctxt                 uint64
	pgpgin, pgpgout            uint64
	pswpin, pswpout            uint64
}

// vm is stat-vm.pl with the numbers of "vmstat -n -S K <interval>" computed
// from /proc (procps 3.3.17 vmstat.c): rates are per interval seconds, rounded
// to nearest, the CPU columns are percentages of the interval's ticks.
type vm struct {
	env      *Env
	interval uint64
	pageKB   uint64
	memKeys  []string
	heading  string
	prev     *vmSample
	// debt is the idle time that ran backwards in the previous interval.
	debt int32
}

func newVM(env *Env, o *options) (*vm, error) {
	v := &vm{env: env, interval: uint64(o.interval.Seconds()), pageKB: uint64(max(env.pageSize()/1024, 0))}

	keys, _, err := readMeminfo(env)
	if err != nil {
		return nil, err
	}

	v.memKeys = keys
	v.heading = strings.Join(append(append([]string{colTimestamp}, vmstatColumns...), append(keys, "loadavg")...), ", ")

	return v, nil
}

// pageSize is the page size swap rates are counted in.
func (e *Env) pageSize() int {
	if e.PageSize > 0 {
		return e.PageSize
	}

	return os.Getpagesize()
}

// Heading implements Collector.
func (v *vm) Heading() string { return v.heading }

// Sample implements Collector. The first tick only takes the reference
// reading: the Perl discards the since-boot line vmstat prints first.
func (v *vm) Sample(t Tick) ([]string, error) {
	cur, procs, err := v.read()
	if err != nil {
		return nil, err
	}

	prev := v.prev
	v.prev = cur

	if prev == nil {
		return nil, nil
	}

	_, mem, err := readMeminfo(v.env)
	if err != nil {
		return nil, err
	}

	load, err := readLoad(v.env)
	if err != nil {
		return nil, err
	}

	cols := append(procs.columns(mem), v.rates(prev, cur)...)

	for _, k := range v.memKeys {
		val := mem[k]
		if val == "" {
			val = "0"
		}

		cols = append(cols, val)
	}

	return []string{t.Stamp + ", " + strings.Join(append(cols, load), ", ")}, nil
}

// vmProcs are the instantaneous values of the line: procs running and blocked.
type vmProcs struct{ running, blocked uint64 }

// columns are r, b and the memory columns (KiB) of the line.
func (p vmProcs) columns(mem map[string]string) []string {
	m := func(k string) uint64 { n, _ := strconv.ParseUint(mem[k], 10, 64); return n }

	var swpd uint64
	if m("SwapFree") < m("SwapTotal") {
		swpd = m("SwapTotal") - m("SwapFree")
	}

	return []string{
		u(p.running), u(p.blocked), u(swpd), u(m("MemFree")), u(m("Buffers")), u(m("Cached") + m("SReclaimable")),
	}
}

func u(n uint64) string { return strconv.FormatUint(n, 10) }

// read takes the counters from /proc/stat and /proc/vmstat.
func (v *vm) read() (*vmSample, vmProcs, error) {
	stat, err := fs.ReadFile(v.env.Root, "proc/stat")
	if err != nil {
		return nil, vmProcs{}, fmt.Errorf("can't open /proc/stat: %w", err)
	}

	vmstat, err := fs.ReadFile(v.env.Root, "proc/vmstat")
	if err != nil {
		return nil, vmProcs{}, fmt.Errorf("can't open /proc/vmstat: %w", err)
	}

	s, p := parseProcStat(string(stat))
	s.parseVMStat(string(vmstat))

	return s, p, nil
}

// parseProcStat reads /proc/stat: the cpu line, the interrupt and context switch
// counters and the number of processes running and blocked.
func parseProcStat(stat string) (*vmSample, vmProcs) {
	s := &vmSample{}

	var p vmProcs

	for line := range strings.SplitSeq(stat, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}

		switch f[0] {
		case colCPU:
			s.setCPU(f[1:])
		case "intr":
			s.intr = num(f[1])
		case "ctxt":
			s.ctxt = num(f[1])
		case "procs_running":
			// vmstat does not count itself.
			if p.running = num(f[1]); p.running > 0 {
				p.running--
			}
		case "procs_blocked":
			p.blocked = num(f[1])
		}
	}

	return s, p
}

// parseVMStat reads the "name value" lines of /proc/vmstat.
func (s *vmSample) parseVMStat(vmstat string) {
	for line := range strings.SplitSeq(vmstat, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			s.setVM(f[0], num(f[1]))
		}
	}
}

func num(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)

	return n
}

func (s *vmSample) setCPU(f []string) {
	t := func(i int) int64 {
		if i < len(f) {
			return i64(num(f[i]))
		}

		return 0
	}

	s.use = t(0) + t(1)
	s.sys = t(2) + t(5) + t(6)
	s.idle = t(3)
	s.iow = t(4)
	s.steal = t(7)
}

func (s *vmSample) setVM(key string, n uint64) {
	switch key {
	case "pgpgin":
		s.pgpgin = n
	case "pgpgout":
		s.pgpgout = n
	case "pswpin":
		s.pswpin = n
	case "pswpout":
		s.pswpout = n
	}
}

// rates are si, so, bi, bo, in, cs, us, sy, id, wa and st of the interval
// between prev and cur.
func (v *vm) rates(prev, cur *vmSample) []string {
	half := v.interval / 2
	per := func(delta uint64) string { return u(uint64(u32((delta + half) / v.interval))) }

	out := []string{
		per((cur.pswpin - prev.pswpin) * v.pageKB),
		per((cur.pswpout - prev.pswpout) * v.pageKB),
		per(cur.pgpgin - prev.pgpgin),
		per(cur.pgpgout - prev.pgpgout),
		per(uint64(u32(cur.intr - prev.intr))),
		per(uint64(u32(cur.ctxt - prev.ctxt))),
	}

	use, sys, idle, iow, steal := cur.use-prev.use, cur.sys-prev.sys, cur.idle-prev.idle, cur.iow-prev.iow,
		cur.steal-prev.steal

	// Idle ticks can run backwards for a moment: the shortfall is taken off
	// the next interval.
	if v.debt != 0 {
		idle = int64(i32(idle) + v.debt)
		v.debt = 0
	}

	if i32(idle) < 0 {
		v.debt = i32(idle)
		idle = 0
	}

	div := use + sys + idle + iow + steal
	if div == 0 {
		div, idle = 1, 1
	}

	for _, x := range []int64{use, sys, idle, iow, steal} {
		out = append(out, strconv.FormatInt((100*x+div/2)/div, 10))
	}

	return out
}

var meminfoSplit = regexp.MustCompile(`[:\s]+`)

// readMeminfo returns the keys of /proc/meminfo in file order and their first
// value, as zmstat-vm splits each line on [:\s]+.
func readMeminfo(env *Env) (keys []string, vals map[string]string, err error) {
	data, err := fs.ReadFile(env.Root, "proc/meminfo")
	if err != nil {
		return nil, nil, fmt.Errorf("can't open /proc/meminfo: %w", err)
	}

	vals = map[string]string{}

	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}

		f := meminfoSplit.Split(line, -1)
		keys = append(keys, f[0])

		if len(f) > 1 {
			vals[f[0]] = f[1]
		}
	}

	return keys, vals, nil
}

// readLoad is the one-minute load average, the first field of /proc/loadavg.
func readLoad(env *Env) (string, error) {
	data, err := fs.ReadFile(env.Root, "proc/loadavg")
	if err != nil {
		return "", fmt.Errorf("can't open /proc/loadavg: %w", err)
	}

	f := strings.Fields(string(data))
	if len(f) == 0 {
		return "", fmt.Errorf("empty /proc/loadavg")
	}

	return f[0], nil
}
