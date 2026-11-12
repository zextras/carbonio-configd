// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"slices"
	"strconv"
	"strings"
)

const allprocsUsage = `Usage: zmstat-allprocs [options]
Monitor all process cpu and I/O statistics
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/allprocs.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when a HUP signal is sent or when
the date changes.  The current log is renamed to <dir>/YYY-MM-DD/allprocs.csv
and a new file is created.
`

// treeColumns are the columns zmstat-allprocs and zmstat-nginx report after
// the timestamp (and the process name).
var treeColumns = []string{
	colUtime, colStime, "cputime", "rchar", "wchar", "read_bytes", "write_bytes", "rss", "processes", "threads",
}

func allprocsSpec() *spec {
	return &spec{
		name:     "allprocs",
		files:    fixedFiles("allprocs.csv", "allprocs.pid"),
		usage:    allprocsUsage,
		noSyslog: true,
		build: func(env *Env, _ *options) (Collector, error) {
			return &allprocs{env: env, hasIO: env.hasIOAccounting()}, nil
		},
	}
}

// allprocs is stat-allprocs.pl: CPU and I/O of the processes under init, summed
// by process name, over the interval since the previous sample (so the first
// tick writes nothing).
type allprocs struct {
	env   *Env
	hasIO bool
	prev  map[int]pidStat
}

// Heading implements Collector.
func (a *allprocs) Heading() string {
	return strings.Join(append([]string{colTimestamp, "process"}, treeColumns...), ", ")
}

// Sample implements Collector. Process names come out sorted; the Perl lists
// them in hash order.
func (a *allprocs) Sample(t Tick) ([]string, error) {
	tree := a.env.scanTree(true)
	cur := map[int]pidStat{}
	a.env.collect(1, tree, cur)

	prev := a.prev
	a.prev = cur

	if prev == nil {
		return nil, nil
	}

	byName := map[string][]int{}
	for pid, name := range tree.names {
		byName[name] = append(byName[name], pid)
	}

	keys := usageKeys(a.hasIO)
	lines := make([]string, 0, len(byName))

	for _, name := range sortedKeys(byName) {
		pids := byName[name]
		slices.Sort(pids)

		sum := treeSum(prev, cur, pids, keys)
		lines = append(lines, t.Stamp+", "+name+", "+sum.row())
	}

	return lines, nil
}

// usageSum is the counters of a set of processes.
type usageSum struct {
	vals                       map[string]int64
	rss, threads, processCount int64
}

// treeSum adds up the counters of pids: the change since prev for a process
// seen before (and still readable), the full count for a new one.
func treeSum(prev, cur map[int]pidStat, pids []int, keys []string) usageSum {
	s := usageSum{vals: map[string]int64{}, processCount: int64(len(pids))}

	for _, pid := range pids {
		_, wasThere := prev[pid]
		_, isThere := cur[pid]

		for _, k := range keys {
			if wasThere && isThere {
				s.vals[k] += delta(prev, cur, pid, k)
			} else {
				s.vals[k] += cur[pid][k]
			}
		}

		s.rss += cur[pid]["rss"]
		s.threads += cur[pid]["threads"]
	}

	return s
}

// row is the values after the name: treeColumns in order.
func (s usageSum) row() string {
	out := make([]string, 0, len(treeColumns))

	for _, c := range treeColumns {
		var v int64

		switch c {
		case "cputime":
			v = s.vals[colUtime] + s.vals[colStime]
		case "rss":
			v = s.rss
		case "processes":
			v = s.processCount
		case "threads":
			v = s.threads
		default:
			v = s.vals[c]
		}

		out = append(out, strconv.FormatInt(v, 10))
	}

	return strings.Join(out, ", ")
}
