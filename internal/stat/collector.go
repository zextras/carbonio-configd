// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package stat is the native port of the zmstat-* statistics collectors
// (libexec/stat-*.pl and Zextras::Mon::Stat of carbonio-core). Each collector
// appends one CSV line per interval to /opt/zextras/zmstat/<name>.csv with the
// heading, timestamp, column order, number format and rotation of the Perl
// scripts, so zmstat-chart and the log shippers keep working. The numbers come
// from /proc and /sys instead of vmstat, iostat, ps, df and pgrep.
package stat

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// Tick is one sampling instant.
type Tick struct {
	// Now is the wall-clock time of the sample.
	Now time.Time
	// Stamp is Now as the CSV timestamp column (Stat.pm getTstamp).
	Stamp string
}

// Collector is one statistics collector. Its spec builds it, so the heading
// (which several collectors read from the system once, at start) is fixed for
// the life of the process.
type Collector interface {
	// Heading is the CSV heading line, without the newline.
	Heading() string
	// Sample reads the system and returns the CSV lines of the tick (each
	// without the newline). No lines means nothing is written this tick.
	Sample(t Tick) ([]string, error)
}

// Paced is a Collector whose interval changes while it runs (zmstat-mtaqueue
// backs off while the queue is big). Interval is read after every Sample.
type Paced interface {
	Interval() time.Duration
}

// Statfs is the part of statfs(2) the df collector needs.
type Statfs struct {
	Blocks, Bfree, Bavail uint64
	// Bsize is the fragment size (f_frsize, or f_bsize when it is zero).
	Bsize uint64
}

// Syslog is Zextras::Mon::Logger: Log goes to facility local0, Stats to
// local1, with ident zimbramon. Levels are "debug", "info", "err" and "crit".
type Syslog interface {
	Log(level, msg string)
	Stats(level, msg string)
}

// Names that several collectors share.
const (
	colTimestamp = "timestamp"
	colStime     = "stime"
	colUtime     = "utime"
	colCPU       = "cpu"
	colSystem    = "system"
	procLDAP     = "ldap"
	procMySQL    = "mysql"
	procStat     = "zmstat"
	defaultUser  = "zextras"
)

// u32 and i32 are C's conversions to a 32-bit integer: sysstat and procps keep
// some counters in an int or unsigned int and rely on its wrap-around.
func u32(v uint64) uint32 { return uint32(v) } //nolint:gosec // wrap-around is the point

func i64(v uint64) int64 { return int64(v) } //nolint:gosec // counters fit

func i32(v int64) int32 { return int32(v) } //nolint:gosec // wrap-around is the point

// MonitorQuery is one cn=Monitor attribute to read.
type MonitorQuery struct{ DN, Attr string }

// ErrBind marks a Monitor error that is a refused bind (as opposed to no
// connection).
var ErrBind = errors.New("bind failed")

// Env is everything a collector touches outside its own memory; tests
// replace the filesystem, the clock and the command runner.
type Env struct {
	Sys sys.System
	// Root is the filesystem root: collectors read "proc/stat", "sys/block".
	Root fs.FS
	// Statfs is statfs(2) of a mount point.
	Statfs func(path string) (Statfs, error)
	// LC is the effective localconfig (zmlocalconfig -q -x).
	LC map[string]string
	// User is the login name the process runs as (id -un); Pid is its pid.
	User string
	Pid  int
	// PageSize is the memory page size swap counters are in (0: the system's).
	PageSize int
	// Loc is the time zone of the timestamps (nil: time.Local).
	Loc *time.Location
	// Base is the installation root (/opt/zextras), Dir the directory of the
	// CSV files and PidDir the one of the pid files.
	Base, Dir, PidDir string
	// Chown hands a directory or a log file to zextras (nil: not done).
	Chown          func(path string) error
	Stdout, Stderr io.Writer
	Syslog         Syslog
	// Signals delivers SIGHUP (rotate) and SIGTERM/SIGINT (stop); nil: the
	// process signals.
	Signals <-chan os.Signal
	// PidAlive reports whether a pid file names a running collector (nil:
	// the process exists and its command line mentions zmstat).
	PidAlive func(pid int) bool
	// UserName maps a uid to a login name, "" when unknown (nil: the
	// system's user database).
	UserName func(uid int) string
	// Enabled returns the zimbraServiceEnabled values of a server (nil: read
	// from LDAP with the credentials of the localconfig).
	Enabled func(ctx context.Context, host string) ([]string, error)
	// Monitor binds to slapd, reads the attribute of each query (the empty
	// string when an entry has none) and unbinds; it is how the ldap collector
	// reads cn=Monitor (nil: over the ldapi socket with the credentials of
	// the localconfig). An error wrapping ErrBind is a failed bind.
	Monitor func(ctx context.Context, queries []MonitorQuery) ([]string, error)
}

func (e *Env) loc() *time.Location {
	if e.Loc != nil {
		return e.Loc
	}

	return time.Local
}

// options are the parsed command line of a collector.
type options struct {
	interval time.Duration
	logPath  string
	console  bool
	flags    map[string]bool
	ints     map[string]int
	strs     map[string]string
	args     []string
}

// spec describes one collector of "setup zmstat <name>".
type spec struct {
	name string
	// csv and pid are the file names under the zmstat and the pid directory;
	// they depend on the options (io -x).
	files func(o *options) (csv, pid string)
	// opts are the options besides -i, -l and -c.
	opts []optDef
	// usage is the text zmstat-<name> prints for a bad command line.
	usage string
	// disk selects zmstat_disk_interval as the default interval.
	disk bool
	// noSyslog is for the collectors that do not copy lines to syslog.
	noSyslog bool
	// rootOK lets root run the collector besides zimbra_user (fd).
	rootOK bool
	// control handles the commands that are not a run ("fd stop"); done is
	// true when args were one.
	control func(env *Env, args []string) (code int, done bool)
	// validate vets the parsed command line; its error text goes to stdout
	// and the exit status is 1.
	validate func(env *Env, o *options) error
	// afterOpen is called with the log path each time the file is (re)opened.
	afterOpen func(env *Env, path string)
	build     func(env *Env, o *options) (Collector, error)
}

func fixedFiles(csv, pid string) func(*options) (string, string) {
	return func(*options) (string, string) { return csv, pid }
}

// specs is every collector.
func specs() []*spec {
	return []*spec{
		procSpec(), cpuSpec(), vmSpec(), ioSpec(), dfSpec(), fdSpec(),
		allprocsSpec(), mysqlSpec(), nginxSpec(), mtaqueueSpec(), ldapSpec(),
	}
}

// Names lists the collectors "setup zmstat" runs, sorted.
func Names() []string {
	var names []string

	for _, s := range specs() {
		names = append(names, s.name)
	}

	slices.Sort(names)

	return names
}

func findSpec(name string) *spec {
	for _, s := range specs() {
		if s.name == name {
			return s
		}
	}

	return nil
}
