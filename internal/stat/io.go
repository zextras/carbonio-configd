// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

const ioUsage = `Usage: zmstat-io [options]
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/io[-x].csv)
-x, --xtended:  extended output
-c, --console:  output to stdout

Default log file name is io.csv, or io-x.csv if -x option is used.
On Macs, the -x option must be specified.

If logging to a file, rotation occurs when HUP signal is sent or when
date changes.  Current log is renamed to <dir>/YYYY-MM-DD/io[-x].csv
and a new file is created.
`

// ioBasicColumns and ioExtColumns are the per-device columns of
// "iostat -d -k" and "iostat -d -k -x" (sysstat 12.5 and later).
var (
	ioBasicColumns = []string{"tps", "kB_read/s", "kB_wrtn/s", "kB_dscd/s", "kB_read", "kB_wrtn", "kB_dscd"}
	ioExtColumns   = []string{
		"r/s", "rkB/s", "rrqm/s", "%rrqm", "r_await", "rareq-sz",
		"w/s", "wkB/s", "wrqm/s", "%wrqm", "w_await", "wareq-sz",
		"d/s", "dkB/s", "drqm/s", "%drqm", "d_await", "dareq-sz",
		"f/s", "f_await", "aqu-sz", "%util",
	}
)

func ioSpec() *spec {
	return &spec{
		name: "io",
		files: func(o *options) (string, string) {
			if o.flags["xtended"] {
				return "io-x.csv", "io-x.pid"
			}

			return "io.csv", "io.pid"
		},
		opts:  []optDef{{"xtended", kindBool}},
		usage: ioUsage,
		build: func(env *Env, o *options) (Collector, error) { return newIO(env, o.flags["xtended"]) },
	}
}

// diskStats is one reading of /sys/block/<dev>/stat, with the integer widths of
// sysstat's struct io_stats: the tick counters are 32 bits wide.
type diskStats struct {
	rdIOs, rdMerges, rdSectors uint64
	wrIOs, wrMerges, wrSectors uint64
	dcIOs, dcMerges, dcSectors uint64
	flIOs                      uint64
	rdTicks, wrTicks, dcTicks  uint32
	flTicks, totTicks, rqTicks uint32
}

// ios is the I/O count iostat reports as tps.
func (s *diskStats) ios() uint64 { return s.rdIOs + s.wrIOs + s.dcIOs }

// anyIO reports whether the device did any I/O (iostat hides the others).
func (s *diskStats) anyIO() bool { return s.rdIOs != 0 || s.wrIOs != 0 || s.dcIOs != 0 || s.flIOs != 0 }

// diskIO is stat-io.pl with iostat -d -k [-x] computed from /sys/block and
// /proc/uptime. The first line is the average since boot, as iostat's first
// report is; the next ones cover one interval each.
type diskIO struct {
	env     *Env
	ext     bool
	devs    []string
	prev    map[string]diskStats
	prevUp  uint64
	heading string
}

func newIO(env *Env, ext bool) (*diskIO, error) {
	c := &diskIO{env: env, ext: ext, prev: map[string]diskStats{}}

	names, err := fs.ReadDir(env.Root, "sys/block")
	if err != nil {
		return nil, fmt.Errorf("unable to list /sys/block: %w", err)
	}

	for _, n := range names {
		if st, ok := c.readDev(n.Name()); ok && st.anyIO() {
			c.devs = append(c.devs, n.Name())
		}
	}

	cols := ioBasicColumns
	if ext {
		cols = ioExtColumns
	}

	heading := []string{colTimestamp}

	for _, d := range c.devs {
		for _, col := range cols {
			heading = append(heading, d+":"+col)
		}
	}

	c.heading = strings.Join(heading, ", ")

	return c, nil
}

// Heading implements Collector.
func (c *diskIO) Heading() string { return c.heading }

// readDev parses /sys/block/<dev>/stat; the fields a kernel does not have are 0.
func (c *diskIO) readDev(dev string) (diskStats, bool) {
	data, err := fs.ReadFile(c.env.Root, "sys/block/"+dev+"/stat")
	if err != nil {
		return diskStats{}, false
	}

	f := strings.Fields(string(data))
	n := func(i int) uint64 {
		if i >= len(f) {
			return 0
		}

		v, _ := strconv.ParseUint(f[i], 10, 64)

		return v
	}

	return diskStats{
		rdIOs: n(0), rdMerges: n(1), rdSectors: n(2), rdTicks: u32(n(3)),
		wrIOs: n(4), wrMerges: n(5), wrSectors: n(6), wrTicks: u32(n(7)),
		totTicks: u32(n(9)), rqTicks: u32(n(10)),
		dcIOs: n(11), dcMerges: n(12), dcSectors: n(13), dcTicks: u32(n(14)),
		flIOs: n(15), flTicks: u32(n(16)),
	}, true
}

// uptimeCS is /proc/uptime in hundredths of a second.
func (c *diskIO) uptimeCS() (uint64, error) {
	data, err := fs.ReadFile(c.env.Root, "proc/uptime")
	if err != nil {
		return 0, fmt.Errorf("can't open /proc/uptime: %w", err)
	}

	first, _, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	sec, cent, _ := strings.Cut(first, ".")

	s, err := strconv.ParseUint(sec, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad /proc/uptime %q", data)
	}

	cs, _ := strconv.ParseUint(cent, 10, 64)

	return s*100 + cs, nil
}

// Sample implements Collector.
func (c *diskIO) Sample(t Tick) ([]string, error) {
	up, err := c.uptimeCS()
	if err != nil {
		return nil, err
	}

	itv := up - c.prevUp
	if itv == 0 {
		itv = 1
	}

	vals := make([]string, 0, len(c.devs)*len(ioExtColumns))

	for _, d := range c.devs {
		cur, _ := c.readDev(d)
		old := c.prev[d]
		c.prev[d] = cur

		// A device that was removed and came back starts from zero.
		if cur.rdIOs+cur.wrIOs+cur.dcIOs+cur.flIOs < old.rdIOs+old.wrIOs+old.dcIOs+old.flIOs &&
			(old.rdSectors == 0 || cur.rdSectors < old.rdSectors) &&
			(old.wrSectors == 0 || cur.wrSectors < old.wrSectors) &&
			(old.dcSectors == 0 || cur.dcSectors < old.dcSectors) {
			old = diskStats{}
		}

		if c.ext {
			vals = append(vals, extStats(&cur, &old, itv)...)
		} else {
			vals = append(vals, basicStats(&cur, &old, itv)...)
		}
	}

	c.prevUp = up

	return []string{t.Stamp + ", " + strings.Join(vals, ", ")}, nil
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// sValue is sysstat's S_VALUE: the rate of a counter per interval, scaled by
// 100 because the interval is in hundredths of a second.
func sValue(prev, cur, itv uint64) float64 { return float64(cur-prev) / float64(itv) * 100 }

// sValue32 is S_VALUE for the 32-bit tick counters.
func sValue32(prev, cur uint32, itv uint64) float64 { return float64(cur-prev) / float64(itv) * 100 }

// sectorDelta is the sectors moved, with iostat's repair of a 32-bit counter
// that wrapped.
func sectorDelta(cur, prev uint64) uint64 {
	d := cur - prev
	if cur < prev && prev <= 0xffffffff {
		d &= 0xffffffff
	}

	return d
}

// basicStats are the columns of "iostat -d -k" of a device (write_basic_stat).
func basicStats(cur, old *diskStats, itv uint64) []string {
	rsec := sValue(old.rdSectors, cur.rdSectors, itv) / 2
	wsec := sValue(old.wrSectors, cur.wrSectors, itv) / 2
	dsec := sValue(old.dcSectors, cur.dcSectors, itv) / 2
	u64 := func(v uint64) string { return strconv.FormatUint(v, 10) }

	return []string{
		f2(sValue(old.ios(), cur.ios(), itv)),
		f2(rsec), f2(wsec), f2(dsec),
		u64(sectorDelta(cur.rdSectors, old.rdSectors) / 2),
		u64(sectorDelta(cur.wrSectors, old.wrSectors) / 2),
		u64(sectorDelta(cur.dcSectors, old.dcSectors) / 2),
	}
}

// rate is a per-second counter that reads 0 when it went backwards.
func rate(prev, cur, itv uint64) float64 {
	if cur < prev {
		return 0
	}

	return sValue(prev, cur, itv)
}

// opCounters are the counters of one operation kind (reads, writes or
// discards) of a device.
type opCounters struct {
	ios, merges, sectors uint64
	ticks                uint32
}

func (s *diskStats) reads() opCounters {
	return opCounters{s.rdIOs, s.rdMerges, s.rdSectors, s.rdTicks}
}
func (s *diskStats) writes() opCounters {
	return opCounters{s.wrIOs, s.wrMerges, s.wrSectors, s.wrTicks}
}
func (s *diskStats) discards() opCounters {
	return opCounters{s.dcIOs, s.dcMerges, s.dcSectors, s.dcTicks}
}

// opStats are the six columns of one operation kind of "iostat -x": rate, kB
// rate, merge rate, merged share, await and request size.
func opStats(prev, cur opCounters, itv uint64) []string {
	dIOs := cur.ios - prev.ios
	dMerges := cur.merges - prev.merges

	var pc, await, arq float64

	if dMerges+dIOs != 0 {
		pc = float64(dMerges) / float64(dMerges+dIOs) * 100
	}

	if cur.ios > prev.ios {
		await = float64(cur.ticks-prev.ticks) / float64(dIOs)
		arq = float64(cur.sectors-prev.sectors) / float64(dIOs)
	}

	secRate := 0.0
	if cur.sectors >= prev.sectors {
		secRate = sValue(prev.sectors, cur.sectors, itv)
	}

	return []string{
		f2(rate(prev.ios, cur.ios, itv)), f2(secRate / 2), f2(rate(prev.merges, cur.merges, itv)),
		f2(pc), f2(await), f2(arq / 2),
	}
}

// extStats are the columns of "iostat -d -k -x" of a device (write_ext_stat).
func extStats(cur, old *diskStats, itv uint64) []string {
	out := opStats(old.reads(), cur.reads(), itv)
	out = append(out, opStats(old.writes(), cur.writes(), itv)...)
	out = append(out, opStats(old.discards(), cur.discards(), itv)...)

	var fAwait, aqu, util float64

	if cur.flIOs > old.flIOs {
		fAwait = float64(cur.flTicks-old.flTicks) / float64(cur.flIOs-old.flIOs)
	}

	if cur.rqTicks >= old.rqTicks {
		aqu = sValue32(old.rqTicks, cur.rqTicks, itv) / 1000.0
	}

	if cur.totTicks >= old.totTicks {
		util = sValue32(old.totTicks, cur.totTicks, itv) / 10.0
	}

	return append(out, f2(rate(old.flIOs, cur.flIOs, itv)), f2(fAwait), f2(aqu), f2(util))
}
