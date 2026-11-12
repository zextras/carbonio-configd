// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ioLines(rchar, wchar, rb, wb int) string {
	return "rchar: " + itoa(rchar) + "\nwchar: " + itoa(wchar) + "\nsyscr: 1\nread_bytes: " + itoa(rb) +
		"\nwrite_bytes: " + itoa(wb) + "\n"
}

func itoa(n int) string { return u(uint64(n)) }

// allprocsTree is a machine with systemd, configd, mailboxd and two sleeps
// under configd; step 2 has one more sleep and everything has run a little.
func allprocsTree(step int, withIO bool) files {
	f := files{}
	io := func(r, w, rb, wb int) string {
		if !withIO {
			return ""
		}

		return ioLines(r, w, rb, wb)
	}

	if withIO {
		f["proc/self/io"] = ""
	}

	jetty := "java\x00-cp\x00x\x00/opt/zextras/mailboxd/etc/jetty.xml\x00"

	if step == 1 {
		f.procEntry(1, "systemd", 0, statLine(1, "systemd", 0, 10, 5, 1, 100), "")
		f.procEntry(100, "configd", 1, statLine(100, "configd", 1, 100, 50, 4, 1000), io(1000, 500, 4096, 0))
		f.procEntry(200, "java", 1, statLine(200, "java", 1, 1000, 200, 50, 50000), io(10000, 2000, 0, 8192))
		f.procEntry(300, "sleep", 100, statLine(300, "sleep", 100, 1, 0, 1, 10), io(0, 0, 0, 0))
	} else {
		f.procEntry(1, "systemd", 0, statLine(1, "systemd", 0, 12, 6, 1, 100), "")
		f.procEntry(100, "configd", 1, statLine(100, "configd", 1, 130, 60, 4, 1000), io(1500, 700, 4096, 100))
		f.procEntry(200, "java", 1, statLine(200, "java", 1, 1100, 250, 50, 50000), io(10500, 2300, 0, 9192))
		f.procEntry(300, "sleep", 100, statLine(300, "sleep", 100, 3, 1, 1, 10), io(0, 0, 0, 0))
		f.procEntry(301, "sleep", 100, statLine(301, "sleep", 100, 5, 2, 1, 20), io(0, 0, 0, 0))
	}

	f["proc/100/cmdline"] = "/opt/zextras/bin/configd\x00"
	f["proc/200/cmdline"] = jetty

	return f
}

// stat-allprocs.pl: names get_pid_tree (name of a process; configd and the
// jetty java become zmconfigd and zmmailboxd), collect_stats and the sums per
// name over the interval; a process new in the interval counts in full and the
// first tick prints nothing.
func TestAllprocsGolden(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, allprocsTree(1, true))
	c := &allprocs{env: te.Env, hasIO: te.hasIOAccounting()}

	assert.Equal(t, "timestamp, process, utime, stime, cputime, rchar, wchar, read_bytes, write_bytes, rss, processes, threads",
		c.Heading())
	assert.Empty(t, te.sample(t, c, 0))

	te.setFiles(allprocsTree(2, true))
	assert.Equal(t, []string{
		stamp(1) + ", sleep, 7, 3, 10, 0, 0, 0, 0, 120, 2, 2",
		stamp(1) + ", systemd, 2, 1, 3, 0, 0, 0, 0, 400, 1, 1",
		stamp(1) + ", zmconfigd, 30, 10, 40, 500, 200, 0, 100, 4000, 1, 4",
		stamp(1) + ", zmmailboxd, 100, 50, 150, 500, 300, 0, 1000, 200000, 1, 50",
	}, te.sample(t, c, 1))
}

// Without /proc/self/io (a kernel without task I/O accounting) the I/O
// columns stay 0 whatever the per-process files hold (HAS_IO_ACCT).
func TestAllprocsWithoutIOAccounting(t *testing.T) {
	t.Parallel()

	te := newTestEnv(t, allprocsTree(1, false))
	c := &allprocs{env: te.Env, hasIO: te.hasIOAccounting()}
	te.sample(t, c, 0)

	te.setFiles(allprocsTree(2, false))
	assert.Equal(t, stamp(1)+", zmconfigd, 30, 10, 40, 0, 0, 0, 0, 4000, 1, 4", te.sample(t, c, 1)[2])
}

// A command name with spaces and parentheses does not shift the fields.
func TestParseStatCommandWithSpaces(t *testing.T) {
	t.Parallel()

	st := pidStat{}
	parseStat(statLine(7, "Web Content (x)", 3, 11, 22, 5, 250), st)

	assert.Equal(t, pidStat{"ppid": 3, "utime": 11, "stime": 22, "threads": 5, "rss": 1000}, st)
}

// stat-nginx.pl get_nginx_pid (93-104) and main (122-200): the master named in
// /run/carbonio/nginx.pid and its children, one row per interval; nothing is
// written while there is no pid file.
func TestNginxGolden(t *testing.T) {
	t.Parallel()

	tree := func(step int) files {
		f := files{"proc/self/io": ""}
		f.procEntry(1, "systemd", 0, statLine(1, "systemd", 0, 1, 1, 1, 1), "")
		f.procEntry(500, "nginx", 1, statLine(500, "nginx", 1, 10*step, 5*step, 1, 300), ioLines(100*step, 50*step, 0, 4096*step))
		f.procEntry(501, "nginx", 500, statLine(501, "nginx", 500, 20*step, 4*step, 1, 200), ioLines(200*step, 10*step, 0, 0))

		return f
	}

	te := newTestEnv(t, tree(1))
	c := &nginx{env: te.Env, hasIO: te.hasIOAccounting()}

	assert.Equal(t, "timestamp, utime, stime, cputime, rchar, wchar, read_bytes, write_bytes, rss, processes, threads",
		c.Heading())
	assert.Empty(t, te.sample(t, c, 0), "no pid file yet")

	require.NoError(t, osWriteFile(te.Base+"/nginx.pid", "500\n"))
	assert.Empty(t, te.sample(t, c, 1), "first reading is the reference")

	te.setFiles(tree(2))
	// Both processes were there: 10+20 user, 5+4 system ticks, 100+200 read ...
	assert.Equal(t, []string{stamp(2) + ", 30, 9, 39, 300, 60, 0, 4096, 2000, 2, 2"}, te.sample(t, c, 2))
}
