// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const nginxUsage = `Usage: zmstat-nginx [options]
Monitor nginx cpu and I/O statistics
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/nginx.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when a HUP signal is sent or when
the date changes.  The current log is renamed to <dir>/YYY-MM-DD/nginx.csv
and a new file is created.
`

func nginxSpec() *spec {
	return &spec{
		name:  "nginx",
		files: fixedFiles("nginx.csv", "nginx.pid"),
		usage: nginxUsage,
		build: func(env *Env, _ *options) (Collector, error) {
			return &nginx{env: env, hasIO: env.hasIOAccounting()}, nil
		},
	}
}

// nginx is stat-nginx.pl: CPU and I/O of the nginx master and its children,
// over the interval since the previous sample. While nginx has no pid file
// nothing is written.
type nginx struct {
	env   *Env
	hasIO bool
	prev  map[int]pidStat
}

// Heading implements Collector.
func (n *nginx) Heading() string { return "timestamp, " + strings.Join(treeColumns, ", ") }

// pid is the master pid of nginx, 0 when it has no pid file.
func (n *nginx) pid() int {
	pid := pidValue(n.env.runFile("nginx.pid"))
	if pid == 0 {
		if st, err := os.Lstat(filepath.Join(n.env.Base, "nginx")); err == nil && st.Mode()&os.ModeSymlink != 0 {
			_, _ = fmt.Fprintf(n.env.Stderr, "zmstat-nginx: cannot open nginx.pid\n")
		}
	}

	return pid
}

// Sample implements Collector.
func (n *nginx) Sample(t Tick) ([]string, error) {
	pid := n.pid()
	if pid == 0 {
		return nil, nil
	}

	tree := n.env.scanTree(false)
	cur := map[int]pidStat{}
	n.env.collect(pid, tree, cur)

	prev := n.prev
	n.prev = cur

	if prev == nil {
		return nil, nil
	}

	pids := make([]int, 0, len(cur))
	for p := range cur {
		pids = append(pids, p)
	}

	slices.Sort(pids)

	sum := treeSum(prev, cur, pids, usageKeys(n.hasIO))

	return []string{t.Stamp + ", " + sum.row()}, nil
}

// pidValue is the number in a pid file, 0 when it cannot be read.
func pidValue(path string) int {
	pid, err := strconv.Atoi(strings.TrimSpace(readPid(path)))
	if err != nil || pid < 0 {
		return 0
	}

	return pid
}

// runFile is a file of the run directory (/run/carbonio), the parent of the
// stats pid directory.
func (e *Env) runFile(name string) string {
	return filepath.Join(filepath.Dir(e.PidDir), name)
}
