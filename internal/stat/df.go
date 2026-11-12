// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

const dfUsage = `Usage: zmstat-df [options]
Monitor disk usage
-i, --interval: output a line every N seconds
-l, --log:      log file (default is /opt/zextras/zmstat/df.csv)
-c, --console:  output to stdout

If logging to a file, rotation occurs when a HUP signal is sent or when
the date changes.  The current log is renamed to <dir>/YYY-MM-DD/df.csv
and a new file is created.
`

// dfSkipTypes are the file system types of "df -x ..." in stat-df.pl.
var dfSkipTypes = []string{"squashfs", "udev", "devtmpfs", "overlay", "efivarfs"}

func dfSpec() *spec {
	return &spec{
		name:  "df",
		files: fixedFiles("df.csv", "df.pid"),
		usage: dfUsage,
		disk:  true,
		build: func(env *Env, _ *options) (Collector, error) { return newDF(env), nil },
	}
}

// df is stat-df.pl: the usage of each mounted file system that is backed by a
// device, with a syslog warning for the ones that are too full.
type df struct {
	env        *Env
	hostname   string
	crit, warn uint64
	excludes   []string
}

func newDF(env *Env) *df {
	threshold := func(key string, def uint64) uint64 {
		if v, err := strconv.ParseUint(env.LC[key], 10, 64); err == nil && v != 0 {
			return v
		}

		return def
	}

	return &df{
		env:      env,
		hostname: env.LC["zimbra_server_hostname"],
		crit:     threshold("zmdisklog_critical_threshold", 95),
		warn:     threshold("zmdisklog_warn_threshold", 85),
		excludes: slices.DeleteFunc(strings.Split(env.LC["zmstat_df_excludes"], ":"),
			func(s string) bool { return s == "" }),
	}
}

// Heading implements Collector.
func (d *df) Heading() string { return "timestamp, path, disk, disk_use, disk_space, disk_pct_used" }

// dfStat is a line of df -k.
type dfStat struct {
	path, disk   string
	use, space   uint64 // KiB
	pct          uint64
	major, minor string
}

// Sample implements Collector.
func (d *df) Sample(t Tick) ([]string, error) {
	stats, err := d.stats()
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, len(stats))

	for _, s := range stats {
		d.warnIfFull(&s)

		lines = append(lines, fmt.Sprintf("%s, %s, %s, %d, %d, %d", t.Stamp, s.path, s.disk, s.use, s.space, s.pct))
	}

	return lines, nil
}

// warnIfFull logs the file systems over the thresholds; the excluded ones are
// only reported at info level.
func (d *df) warnIfFull(s *dfStat) {
	if d.env.Syslog == nil {
		return
	}

	msg := fmt.Sprintf("Disk warning: %s: %s on device %s at %d%%", d.hostname, s.path, s.disk, s.pct)
	skip := slices.Contains(d.excludes, s.path) || slices.Contains(d.excludes, s.disk)

	switch {
	case skip && s.pct >= d.warn:
		d.env.Syslog.Log("info", msg)
	case s.pct >= d.crit:
		d.env.Syslog.Log("crit", msg)
	case s.pct >= d.warn:
		d.env.Syslog.Log("err", msg)
	}
}

// stats is get_df_stat: "df -k -x squashfs -x udev -x devtmpfs -x overlay -x
// efivarfs", computed from the mount table and statfs, for the mounts whose
// source is a path (a device).
func (d *df) stats() ([]dfStat, error) {
	data, err := fs.ReadFile(d.env.Root, "proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("can't read /proc/self/mountinfo: %w", err)
	}

	var out []dfStat

	for line := range strings.SplitSeq(string(data), "\n") {
		m, ok := parseMountinfo(line)
		if !ok || !strings.HasPrefix(m.source, "/") || slices.Contains(dfSkipTypes, m.fstype) {
			continue
		}

		st, err := d.env.Statfs(m.mount)
		if err != nil || st.Blocks == 0 {
			continue
		}

		s, ok := dfFigures(&st)
		if !ok {
			continue
		}

		s.path, s.disk, s.major, s.minor = m.mount, m.source, m.major, m.minor

		out = append(out, s)
	}

	return dedupe(out), nil
}

// dfFigures are the sizes df prints for a file system: KiB rounded up, and
// the share of the space available to non-root users used, rounded up. ok is
// false for a file system with no such space (df prints "-").
func dfFigures(st *Statfs) (s dfStat, ok bool) {
	used := st.Blocks - st.Bfree
	if used > st.Blocks { // Bfree above Blocks
		used = 0
	}

	nonRoot := used + st.Bavail
	if nonRoot == 0 {
		return s, false
	}

	kib := func(blocks uint64) uint64 { return (blocks*st.Bsize + 1023) / 1024 }

	s.space, s.use = kib(st.Blocks), kib(used)
	s.pct = (used*100 + nonRoot - 1) / nonRoot

	return s, true
}

// dedupe keeps one mount per device, as df lists a device once: the one
// closest to the root of the file system tree.
func dedupe(in []dfStat) []dfStat {
	var out []dfStat

	for _, s := range in {
		j := slices.IndexFunc(out, func(o dfStat) bool { return o.major == s.major && o.minor == s.minor })
		if j < 0 {
			out = append(out, s)

			continue
		}

		if len(s.path) < len(out[j].path) {
			out[j] = s
		}
	}

	return out
}

// mountEntry is a line of /proc/self/mountinfo.
type mountEntry struct {
	major, minor, mount, fstype, source string
}

// parseMountinfo reads "36 35 98:0 /root /mnt rw,noatime master:1 - ext3 /dev/x rw".
func parseMountinfo(line string) (mountEntry, bool) {
	f := strings.Fields(line)

	sep := slices.Index(f, "-")
	if sep < 6 || len(f) < sep+3 {
		return mountEntry{}, false
	}

	major, minor, _ := strings.Cut(f[2], ":")

	return mountEntry{
		major: major, minor: minor, mount: unescapeMount(f[4]), fstype: f[sep+1], source: unescapeMount(f[sep+2]),
	}, true
}

// unescapeMount decodes the octal escapes (\040 for a blank) of the mount table.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}

	var b strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))

				i += 3

				continue
			}
		}

		b.WriteByte(s[i])
	}

	return b.String()
}
