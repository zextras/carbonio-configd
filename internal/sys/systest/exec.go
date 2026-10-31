// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systest

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// Handler scripts the outcome of one command. A non-nil error is returned to
// the caller as-is (use &sys.ExitError{Code: 1} for a failing exit).
type Handler func(cmd *sys.Cmd) (sys.Result, error)

type rule struct {
	match   func(*sys.Cmd) bool
	handler Handler
}

// Exec is a scriptable sys.Exec that records every command it is asked to
// run. Commands that match no rule succeed with empty output.
type Exec struct {
	rec *Recorder

	mu       sync.Mutex
	rules    []rule
	calls    []*sys.Cmd
	nextPID  int
	started  map[int]*Process
	startErr func(*sys.Cmd) error
}

// NewExec returns an Exec recording into rec.
func NewExec(rec *Recorder) *Exec {
	return &Exec{rec: rec, nextPID: 1000, started: map[int]*Process{}}
}

// When installs a rule: commands for which match returns true are answered by
// handler. Later rules win over earlier ones.
func (e *Exec) When(match func(*sys.Cmd) bool, handler Handler) {
	e.mu.Lock()
	e.rules = append(e.rules, rule{match, handler})
	e.mu.Unlock()
}

// OnPath answers every command whose Path equals path.
func (e *Exec) OnPath(path string, handler Handler) {
	e.When(func(c *sys.Cmd) bool { return c.Path == path }, handler)
}

// OnCmdline answers every command whose "path args..." line has the prefix.
func (e *Exec) OnCmdline(prefix string, handler Handler) {
	e.When(func(c *sys.Cmd) bool { return strings.HasPrefix(c.String(), prefix) }, handler)
}

// Reply is a Handler returning a fixed stdout (also the combined output) and
// a nil error.
func Reply(stdout string) Handler {
	return func(*sys.Cmd) (sys.Result, error) { return sys.Result{Stdout: []byte(stdout)}, nil }
}

// Fail is a Handler answering with a non-zero exit and the given output.
func Fail(code int, output string) Handler {
	return func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{Stdout: []byte(output), ExitCode: code},
			&sys.ExitError{Code: code, Stderr: []byte(output)}
	}
}

// FailStart makes Start (and Run) return err for matching commands, as if
// the binary could not be spawned.
func (e *Exec) FailStart(match func(*sys.Cmd) bool, err error) {
	e.mu.Lock()
	prev := e.startErr
	e.startErr = func(c *sys.Cmd) error {
		if match(c) {
			return err
		}

		if prev != nil {
			return prev(c)
		}

		return nil
	}
	e.mu.Unlock()
}

// Calls returns every command seen, in order.
func (e *Exec) Calls() []*sys.Cmd {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]*sys.Cmd(nil), e.calls...)
}

// Lines returns every command seen as "path args..." lines, in order.
func (e *Exec) Lines() []string {
	calls := e.Calls()
	out := make([]string, len(calls))

	for i := range calls {
		out[i] = calls[i].String()
	}

	return out
}

func (e *Exec) lookup(c *sys.Cmd) (Handler, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.calls = append(e.calls, c)

	if e.startErr != nil {
		if err := e.startErr(c); err != nil {
			return nil, err
		}
	}

	for _, r := range slices.Backward(e.rules) {
		if r.match(c) {
			return r.handler, nil
		}
	}

	return nil, nil //nolint:nilnil // no rule matched: the caller applies the default success
}

// Run implements sys.Exec.
func (e *Exec) Run(ctx context.Context, c *sys.Cmd) (sys.Result, error) {
	e.rec.Record("exec", c.String())

	h, err := e.lookup(c)
	if err != nil {
		return sys.Result{ExitCode: -1}, err
	}

	if err := ctx.Err(); err != nil {
		return sys.Result{ExitCode: -1}, err //nolint:wrapcheck // mirrors os/exec
	}

	if h == nil {
		return sys.Result{}, nil
	}

	res, err := h(c)
	if !c.Combined && c.Stdout != nil {
		_, _ = c.Stdout.Write(res.Stdout)
		res.Stdout = nil
	}

	if c.Stderr != nil {
		_, _ = c.Stderr.Write(res.Stderr)
		res.Stderr = nil
	}

	return res, err
}

// Start implements sys.Exec. The returned Process reports success on Wait
// unless the matching handler returned an error, which Wait then returns.
func (e *Exec) Start(_ context.Context, c *sys.Cmd) (sys.Process, error) {
	e.rec.Record("exec.start", c.String())

	h, err := e.lookup(c)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	e.nextPID++
	p := &Process{pid: e.nextPID, rec: e.rec, cmd: c}
	e.started[p.pid] = p
	e.mu.Unlock()

	if h != nil {
		res, herr := h(c)
		p.waitErr = herr

		if c.Stdout != nil {
			_, _ = c.Stdout.Write(res.Stdout)
		}
	}

	return p, nil
}

// Started returns the process started with the given pid.
func (e *Exec) Started(pid int) *Process {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.started[pid]
}

// Process is the fake sys.Process.
type Process struct {
	pid     int
	rec     *Recorder
	cmd     *sys.Cmd
	waitErr error
}

// Pid implements sys.Process.
func (p *Process) Pid() int { return p.pid }

// Cmd returns the command the process was started from.
func (p *Process) Cmd() *sys.Cmd { return p.cmd }

// Wait implements sys.Process.
func (p *Process) Wait() error { return p.waitErr }

// Release implements sys.Process.
func (p *Process) Release() error {
	p.rec.Recordf("exec.release", "%d", p.pid)

	return nil
}
