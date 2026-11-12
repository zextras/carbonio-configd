// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zextras/carbonio-configd/internal/getopt"
)

const (
	exitOK    = 0
	exitError = 1

	optInterval = "interval"
	optConsole  = "console"
)

// Run executes "setup stat <name> [options]": the collector of that name
// with the command line of the zmstat-<name> script. It returns the exit
// status; it runs until SIGTERM, SIGINT or ctx is done.
func Run(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(env.Stderr, "Usage: setup stat <name> [options]\nCollectors: %s\n", strings.Join(Names(), ", "))

		return exitError
	}

	sp := findSpec(args[0])
	if sp == nil {
		_, _ = fmt.Fprintf(env.Stderr, "Unknown collector %q; one of: %s\n", args[0], strings.Join(Names(), ", "))

		return exitError
	}

	if !env.userAllowed(sp) {
		_, _ = fmt.Fprintf(env.Stderr, "Must be user %s to run this command\n", env.zimbraUser())

		return exitError
	}

	if sp.control != nil {
		if code, done := sp.control(env, args[1:]); done {
			return code
		}
	}

	o, ok := env.parse(sp, args[1:])
	if !ok {
		return exitError
	}

	if sp.validate != nil {
		if err := sp.validate(env, o); err != nil {
			_, _ = fmt.Fprint(env.Stdout, err.Error())

			return exitError
		}
	}

	return env.serve(ctx, sp, o)
}

// zimbraUser is the user the collectors run as (zimbra_user).
func (e *Env) zimbraUser() string {
	if u := e.LC["zimbra_user"]; u != "" {
		return u
	}

	return defaultUser
}

// userAllowed is Stat.pm userCheck: only zimbra_user may run a collector (root
// too for fd, which the Perl ran through sudo).
func (e *Env) userAllowed(sp *spec) bool {
	return e.User == e.zimbraUser() || sp.rootOK && e.User == "root"
}

// parse is GetOptions plus the defaults every script applies.
func (e *Env) parse(sp *spec, args []string) (*options, bool) {
	specs := append([]getopt.Spec{
		{Names: []string{optInterval}, Kind: getopt.Int},
		{Names: []string{"log"}, Kind: getopt.String},
		{Names: []string{optConsole}},
	}, sp.opts...)

	po := getopt.Parse(args, specs, e.Stderr)
	if !po.AllGood {
		_, _ = fmt.Fprintln(e.Stderr)
		_, _ = fmt.Fprint(e.Stderr, sp.usage)

		return nil, false
	}

	o := &options{
		interval: e.defaultInterval(sp.disk),
		logPath:  po.Strs["log"],
		console:  po.Has(optConsole),
		flags:    po.Given,
		ints:     po.Ints,
		strs:     po.Strs,
		args:     po.Rest,
	}

	// A zero or negative interval would spin; the default stands in.
	if n, set := po.Ints[optInterval]; set && n >= 1 {
		o.interval = time.Duration(n) * time.Second
	}

	csv, _ := sp.files(o)

	switch o.logPath {
	case "":
		o.logPath = filepath.Join(e.Dir, csv)
	case "-":
		o.console = true
	}

	if o.console {
		o.logPath = "-"
	}

	return o, true
}

// serve is the part every script shares: pid file, collector, log, then one
// sample per interval on a round second until stopped.
func (e *Env) serve(ctx context.Context, sp *spec, o *options) int {
	csv, pid := sp.files(o)

	if err := e.createPid(pid); err != nil {
		if errors.Is(err, errAlreadyRunning) {
			return exitOK
		}

		_, _ = fmt.Fprintln(e.Stderr, err)

		return exitError
	}

	defer e.removePid(pid)

	// Before the log and the collector open, so no signal meets Go's default.
	sigs, stop := e.signals()
	defer stop()

	c, err := sp.build(e, o)
	if err != nil {
		_, _ = fmt.Fprintln(e.Stderr, err)

		return exitError
	}

	date := e.date(e.Sys.Clock.Now())

	lf, err := openLog(e, o.logPath, c.Heading(), func() string { return date })
	if err != nil {
		_, _ = fmt.Fprintln(e.Stderr, err)

		return exitError
	}

	defer lf.close()

	if sp.afterOpen != nil && !o.console {
		sp.afterOpen(e, o.logPath)
	}

	r := &runner{env: e, sp: sp, c: c, lf: lf, csv: csv, date: date, interval: o.interval, console: o.console}

	return r.loop(ctx, sigs)
}

// runner is a collector running.
type runner struct {
	env      *Env
	sp       *spec
	c        Collector
	lf       *logFile
	csv      string
	date     string
	interval time.Duration
	console  bool
}

func (e *Env) signals() (sigs <-chan os.Signal, stop func()) {
	if e.Signals != nil {
		return e.Signals, func() {
			// The channel is the caller's: there is no subscription to cancel.
		}
	}

	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)

	return ch, func() { signal.Stop(ch) }
}

// loop waits for a round second, then samples every interval (aligned to the
// first sample, so the interval does not drift with the work).
func (r *runner) loop(ctx context.Context, sigs <-chan os.Signal) int {
	if wait := r.env.niceRoundWait(r.interval); wait > 0 && !r.sleep(ctx, sigs, wait) {
		return exitOK
	}

	next := r.env.Sys.Clock.Now()

	for {
		r.tick(next)

		if p, ok := r.c.(Paced); ok {
			r.interval = p.Interval()
		}

		next = next.Add(r.interval)

		wait := next.Sub(r.env.Sys.Clock.Now())
		if wait < 0 {
			wait = 0
			next = r.env.Sys.Clock.Now()
		}

		if !r.sleep(ctx, sigs, wait) {
			return exitOK
		}
	}
}

// sleep waits for d; SIGHUP rotates the log meanwhile. It reports false when
// the collector must stop.
func (r *runner) sleep(ctx context.Context, sigs <-chan os.Signal, d time.Duration) bool {
	timer := r.env.Sys.Clock.After(d)

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer:
			return true
		case sig := <-sigs:
			if sig != syscall.SIGHUP {
				return false
			}

			r.rotate(r.env.date(r.env.Sys.Clock.Now()))
		}
	}
}

// tick takes one sample and writes it, rolling the log over at midnight.
func (r *runner) tick(sched time.Time) {
	now := r.env.Sys.Clock.Now()
	if now.Before(sched) {
		now = sched
	}

	tk := Tick{Now: now, Stamp: r.env.tstamp(now)}

	lines, err := r.c.Sample(tk)
	if err != nil {
		r.report(err)

		return
	}

	if today := r.env.date(now); today != r.date {
		r.rotate(r.date)
		r.date = today
	}

	if len(lines) == 0 {
		return
	}

	if err := r.lf.write(lines); err != nil {
		r.report(err)
	}

	if r.env.Syslog == nil || r.sp.noSyslog {
		return
	}

	for _, l := range lines {
		r.env.Syslog.Stats("info", "zmstat "+r.csv+": "+r.c.Heading()+":: "+l)
	}
}

// report prints a collector error as "stat <name>: <error>".
func (r *runner) report(err error) {
	_, _ = fmt.Fprintf(r.env.Stderr, "stat %s: %v\n", r.sp.name, err)
}

// rotate moves the log of date away and starts a new one.
func (r *runner) rotate(date string) {
	if r.console {
		return
	}

	if err := r.lf.rotate(date); err != nil {
		r.report(err)
	}

	if r.sp.afterOpen != nil {
		r.sp.afterOpen(r.env, r.lf.path)
	}
}
