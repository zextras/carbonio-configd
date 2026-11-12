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

// cpuFields are the /proc/stat columns zmstat-cpu reports, in order.
var cpuFields = []string{"user", "nice", "sys", "idle", "iowait", "irq", "softirq"}

const cpuUsage = `Usage: zmstat-cpu [options]
Monitor CPU activity
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/cpu.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when HUP signal is sent or when
date changes.  Current log is renamed to <dir>/YYYY-MM-DD/cpu.csv
and a new file is created.
`

func cpuSpec() *spec {
	return &spec{
		name:  colCPU,
		files: fixedFiles("cpu.csv", "cpu.pid"),
		usage: cpuUsage,
		build: func(env *Env, _ *options) (Collector, error) { return newCPU(env) },
	}
}

// cpu is stat-cpu.pl: the share of each /proc/stat counter, in percent, of the
// total over the interval, for all CPUs and then each CPU. The first line is
// the share since boot.
type cpu struct {
	env     *Env
	cpus    int
	prev    [][8]int64 // per cpu line: total, then the seven fields
	heading string
}

func newCPU(env *Env) (*cpu, error) {
	lines, err := cpuLines(env)
	if err != nil {
		return nil, err
	}

	c := &cpu{env: env}

	for _, l := range lines {
		if len(l) > 0 && strings.HasPrefix(l[0], colCPU) && len(l[0]) > 3 {
			c.cpus++
		}
	}

	c.prev = make([][8]int64, c.cpus+1)

	var b strings.Builder

	b.WriteString(colTimestamp)

	for i := 0; i <= c.cpus; i++ {
		name := colCPU
		if i > 0 {
			name = colCPU + strconv.Itoa(i-1)
		}

		for _, f := range cpuFields {
			b.WriteString(", " + name + ":" + f)
		}
	}

	c.heading = b.String()

	return c, nil
}

// cpuLines are the fields of the leading cpu lines of /proc/stat.
func cpuLines(env *Env) ([][]string, error) {
	data, err := fs.ReadFile(env.Root, "proc/stat")
	if err != nil {
		return nil, fmt.Errorf("can't open /proc/stat: %w", err)
	}

	var out [][]string

	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, colCPU) {
			break
		}

		out = append(out, strings.Fields(line))
	}

	return out, nil
}

// Heading implements Collector.
func (c *cpu) Heading() string { return c.heading }

// Sample implements Collector.
func (c *cpu) Sample(t Tick) ([]string, error) {
	lines, err := cpuLines(c.env)
	if err != nil {
		return nil, err
	}

	utils := make([]string, 0, len(lines)*len(cpuFields))

	for i, fields := range lines {
		if i >= len(c.prev) {
			// A CPU that came online after the heading was fixed has no
			// column; the Perl reads past its array, which gives zeros.
			c.prev = append(c.prev, [8]int64{})
		}

		utils = append(utils, c.share(i, fields)...)
	}

	return []string{t.Stamp + ", " + strings.Join(utils, ", ")}, nil
}

// share is the percentage of each field of cpu line i, and remembers it.
func (c *cpu) share(i int, fields []string) []string {
	var cur [8]int64

	for f := 1; f <= 7; f++ {
		if f < len(fields) {
			cur[f], _ = strconv.ParseInt(fields[f], 10, 64)
		}

		cur[0] += cur[f]
	}

	totalDelta := cur[0] - c.prev[i][0]
	out := make([]string, 0, 7)

	for f := 1; f <= 7; f++ {
		out = append(out, percent(cur[f]-c.prev[i][f], totalDelta))
	}

	c.prev[i] = cur

	return out
}
