// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
)

// processPid is the pid file of a process running several collectors, under
// the pid directory: the one a service manager supervises (the unit's
// PIDFile). Each collector besides keeps the pid file of a process of its own.
const processPid = "stats.pid"

// gcPercent is the garbage collection target of a process that runs the
// collectors: they allocate a little at each tick and keep a small live heap,
// so collecting at 25% growth instead of 100% keeps the resident set about 10%
// smaller (23.8 MB instead of 26.7 MB for the eight collectors of a node,
// measured over 60 s) for 0.04 s of CPU per minute more.
const gcPercent = 25

// TuneMemory sets the garbage collection target of the long-running process
// of "setup stat run".
func TuneMemory() { debug.SetGCPercent(gcPercent) }

// job is one collector of "setup stat run": its spec and parsed command line.
type job struct {
	sp  *spec
	o   *options
	pid string
}

// member is a job that is running: the runner, and the channel that delivers
// the rotate requests to it.
type member struct {
	r   *runner
	pid string
	hup chan os.Signal
}

// lockedWriter serialises the writes of the collectors of one process to a
// stream they share (stderr, stdout with -c).
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.Write(p) //nolint:wrapcheck // the stream's own error
}

// runMany is "setup stat run <collector>...": every argument is the command
// line of one collector as the zmstat-<name> script takes it ("proc", "io -x",
// "mtaqueue --max-qsize 5000"), all of them run in this process, each in its
// own goroutine with its own interval, log and rotation and, as in a process
// of its own, the pid file <name>.pid (naming this process, so a collector run
// by hand finds it already running); the process has processPid too, and a
// second "run" finds the first one running. SIGHUP rotates every log and SIGTERM or
// SIGINT stops all of them. A collector that cannot start, or that panics, is
// reported on stderr and the others keep running; the status is 1 then.
func (e *Env) runMany(ctx context.Context, cmds []string) int {
	if len(cmds) == 0 {
		_, _ = fmt.Fprintf(e.Stderr, "Usage: setup stat %s <collector> [<collector>...]\nCollectors: %s\n",
			cmdRun, strings.Join(Names(), ", "))

		return exitError
	}

	jobs, ok := e.plan(cmds)
	if !ok {
		return exitError
	}

	// One stream of output for all of them.
	shared := *e
	mu := &sync.Mutex{}
	shared.Stdout = lockedWriter{mu: mu, w: e.Stdout}
	shared.Stderr = lockedWriter{mu: mu, w: e.Stderr}

	// Before any log or collector opens, so no signal meets Go's default.
	sigs, stop := shared.signals()
	defer stop()

	if err := shared.createPid(processPid); err != nil {
		if errors.Is(err, errAlreadyRunning) {
			return exitOK
		}

		_, _ = fmt.Fprintln(shared.Stderr, err)

		return exitError
	}

	defer shared.removePid(processPid)

	members, failed := shared.join(jobs)
	if len(members) == 0 {
		if failed {
			return exitError
		}

		return exitOK
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dispatched := make(chan struct{})

	go func() {
		defer close(dispatched)

		dispatch(ctx, cancel, sigs, members)
	}()

	codes := make([]int, len(members))

	var wg sync.WaitGroup

	for i, m := range members {
		wg.Go(func() { codes[i] = m.run(ctx) })
	}

	wg.Wait()
	cancel()
	<-dispatched

	for _, code := range codes {
		if code != exitOK {
			failed = true
		}
	}

	if failed {
		return exitError
	}

	return exitOK
}

// plan parses and checks the command line of every collector before anything
// starts, so a typo does not leave half of them running.
func (e *Env) plan(cmds []string) ([]job, bool) {
	var jobs []job

	seen := map[string]bool{}

	for _, cmd := range cmds {
		j, ok := e.parseJob(cmd)
		if !ok {
			return nil, false
		}

		if seen[j.pid] {
			_, _ = fmt.Fprintf(e.Stderr, "Collector %q is given twice\n", cmd)

			return nil, false
		}

		seen[j.pid] = true

		jobs = append(jobs, j)
	}

	return jobs, true
}

// parseJob is the checks of Run for one collector of the command line.
func (e *Env) parseJob(cmd string) (job, bool) {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		_, _ = fmt.Fprintln(e.Stderr, "Empty collector")

		return job{}, false
	}

	sp := findSpec(fields[0])
	if sp == nil {
		_, _ = fmt.Fprintf(e.Stderr, "Unknown collector %q; one of: %s\n", fields[0], strings.Join(Names(), ", "))

		return job{}, false
	}

	if !e.userAllowed(sp) {
		_, _ = fmt.Fprintf(e.Stderr, "Must be user %s to run this command\n", e.zimbraUser())

		return job{}, false
	}

	o, ok := e.parse(sp, fields[1:])
	if !ok {
		return job{}, false
	}

	if len(o.args) > 0 {
		_, _ = fmt.Fprintf(e.Stderr, "Collector %q: unexpected argument %q\n", cmd, o.args[0])

		return job{}, false
	}

	if sp.validate != nil {
		if err := sp.validate(e, o); err != nil {
			_, _ = fmt.Fprint(e.Stdout, err.Error())

			return job{}, false
		}
	}

	_, pid := sp.files(o)

	return job{sp: sp, o: o, pid: pid}, true
}

// join creates the pid file and opens the log of each job. A collector that is
// already running (another process holds its pid file) is left to it; one that
// cannot start is reported and counted in failed.
func (e *Env) join(jobs []job) (members []*member, failed bool) {
	for _, j := range jobs {
		if err := e.createPid(j.pid); err != nil {
			if !errors.Is(err, errAlreadyRunning) {
				_, _ = fmt.Fprintf(e.Stderr, "stat %s: %v\n", j.sp.name, err)
				failed = true
			}

			continue
		}

		r, err := e.start(j.sp, j.o)
		if err != nil {
			_, _ = fmt.Fprintf(e.Stderr, "stat %s: %v\n", j.sp.name, err)
			e.removePid(j.pid)

			failed = true

			continue
		}

		members = append(members, &member{r: r, pid: j.pid, hup: make(chan os.Signal, 1)})
	}

	return members, failed
}

// run samples until ctx is done, then closes the log and removes the pid file.
func (m *member) run(ctx context.Context) int {
	defer m.r.env.removePid(m.pid)
	defer m.r.lf.close()

	return m.r.guarded(ctx, m.hup)
}

// guarded is loop that turns a panic of the collector into an error on
// stderr and a status of 1, so one collector cannot take the process down.
func (r *runner) guarded(ctx context.Context, sigs <-chan os.Signal) (code int) {
	defer func() {
		if v := recover(); v != nil {
			r.report(fmt.Errorf("panic: %v\n%s", v, debug.Stack()))

			code = exitError
		}
	}()

	return r.loop(ctx, sigs)
}

// dispatch hands the process's signals to the collectors: SIGHUP to each of
// them (one rotation is pending at most), any other signal stops all.
func dispatch(ctx context.Context, cancel context.CancelFunc, sigs <-chan os.Signal, members []*member) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigs:
			if sig != syscall.SIGHUP {
				cancel()

				return
			}

			for _, m := range members {
				select {
				case m.hup <- sig:
				default: // a rotation is already waiting
				}
			}
		}
	}
}
