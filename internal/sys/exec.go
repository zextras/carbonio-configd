// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Cmd describes one external command. Timeouts are the caller's context
// deadline; commands run as the calling user (privilege changes go through an
// explicit sudo argv, exactly as before).
type Cmd struct {
	// Path is the program to run (looked up in PATH when it has no slash).
	Path string
	// Args are the arguments after argv[0].
	Args []string
	// Env holds NAME=value pairs layered over the inherited environment: an
	// entry replaces any inherited variable of the same name. Nil inherits
	// the environment unchanged.
	Env []string
	// Dir is the working directory ("" keeps the caller's).
	Dir string
	// Stdin feeds the child's standard input (nil: /dev/null).
	Stdin io.Reader
	// Stdout and Stderr redirect the child's output. A nil writer is
	// captured into Result instead (Run) or discarded (Start). A writer that
	// holds an *os.File (as returned by FS.OpenFile) is handed to the child
	// as that very descriptor, so a detached daemon keeps logging after the
	// parent exits.
	Stdout, Stderr io.Writer
	// Combined merges stderr into the captured stdout, in arrival order, like
	// exec.Cmd.CombinedOutput. Ignored when Stdout or Stderr is set.
	Combined bool
	// Setsid starts the child in a new session (PGID = PID), severing the
	// controlling terminal so a daemon survives the parent's exit.
	Setsid bool
}

// Argv returns the command as one argv-style list, Path first.
func (c *Cmd) Argv() []string {
	return append([]string{c.Path}, c.Args...)
}

// String renders the command for logs and recordings.
func (c *Cmd) String() string {
	return strings.Join(c.Argv(), " ")
}

// Result is the outcome of a completed command.
type Result struct {
	// Stdout is the captured standard output (also stderr when Cmd.Combined).
	Stdout []byte
	// Stderr is the captured standard error.
	Stderr []byte
	// ExitCode is the process exit status; -1 when it died from a signal or
	// never ran.
	ExitCode int
}

// ExitError reports a command that ran and exited unsuccessfully (non-zero
// status or death by signal). Failing to start the command at all (binary
// missing, permission denied, context cancelled before start) is reported as
// the underlying error instead, never an ExitError — callers tell "ran and
// said no" from "could not run".
type ExitError struct {
	// Code is the exit status, -1 when terminated by a signal.
	Code int
	// Stderr is the captured standard error, when it was captured.
	Stderr []byte
	// Err is the underlying cause (an *exec.ExitError in production).
	Err error
}

// Error implements error with the same text os/exec uses ("exit status 1").
func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}

	return "exit status " + strconv.Itoa(e.Code)
}

// Unwrap exposes the underlying cause.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode returns the exit status.
func (e *ExitError) ExitCode() int { return e.Code }

// Process is a started, not yet reaped child.
type Process interface {
	// Pid is the child's process id.
	Pid() int
	// Wait blocks until the child exits and reports a non-zero exit as
	// *ExitError.
	Wait() error
	// Release frees the handle without waiting: the child is left to init.
	Release() error
}

// Exec runs external commands.
type Exec interface {
	// Run starts the command, waits for it, and returns what it captured. A
	// non-zero exit is returned as *ExitError alongside the Result.
	Run(ctx context.Context, cmd *Cmd) (Result, error)
	// Start starts the command and returns without waiting. Cancelling ctx
	// kills the child; pass context.Background() for a daemon that must
	// outlive the request.
	Start(ctx context.Context, cmd *Cmd) (Process, error)
}

// Output runs name with args and returns its standard output. Stderr is
// captured into the returned *ExitError on failure.
func Output(ctx context.Context, x Exec, name string, args ...string) ([]byte, error) {
	res, err := x.Run(ctx, &Cmd{Path: name, Args: args})

	return res.Stdout, err
}

// CombinedOutput runs name with args and returns stdout and stderr merged.
func CombinedOutput(ctx context.Context, x Exec, name string, args ...string) ([]byte, error) {
	res, err := x.Run(ctx, &Cmd{Path: name, Args: args, Combined: true})

	return res.Stdout, err
}

// RealExec runs commands with os/exec.
type RealExec struct{}

func (RealExec) command(ctx context.Context, c *Cmd) *exec.Cmd {
	//nolint:gosec // G204: every caller passes a registry-fixed binary and argv
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr

	if c.Env != nil {
		cmd.Env = mergeEnv(os.Environ(), c.Env)
	}

	if c.Setsid {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	return cmd
}

// mergeEnv returns base without the variables overridden by extra, followed
// by extra.
func mergeEnv(base, extra []string) []string {
	over := make(map[string]struct{}, len(extra))

	for _, kv := range extra {
		name, _, _ := strings.Cut(kv, "=")
		over[name] = struct{}{}
	}

	env := make([]string, 0, len(base)+len(extra))

	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if _, replaced := over[name]; !replaced {
			env = append(env, kv)
		}
	}

	return append(env, extra...)
}

// Run implements Exec.
func (r RealExec) Run(ctx context.Context, c *Cmd) (Result, error) {
	cmd := r.command(ctx, c)

	var out, errOut bytes.Buffer

	switch {
	case c.Stdout == nil && c.Stderr == nil && c.Combined:
		cmd.Stdout, cmd.Stderr = &out, &out
	default:
		if c.Stdout == nil {
			cmd.Stdout = &out
		}

		if c.Stderr == nil {
			cmd.Stderr = &errOut
		}
	}

	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errOut.Bytes()}

	if err == nil {
		return res, nil
	}

	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		res.ExitCode = ee.ExitCode()

		return res, &ExitError{Code: res.ExitCode, Stderr: res.Stderr, Err: ee}
	}

	res.ExitCode = -1

	return res, err
}

// Start implements Exec.
func (r RealExec) Start(ctx context.Context, c *Cmd) (Process, error) {
	cmd := r.command(ctx, c)

	if err := cmd.Start(); err != nil {
		return nil, err //nolint:wrapcheck // callers add the service context
	}

	return &realProcess{cmd: cmd}, nil
}

type realProcess struct{ cmd *exec.Cmd }

func (p *realProcess) Pid() int { return p.cmd.Process.Pid }

func (p *realProcess) Wait() error {
	err := p.cmd.Wait()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return &ExitError{Code: ee.ExitCode(), Err: ee}
	}

	return err
}

func (p *realProcess) Release() error { return p.cmd.Process.Release() }
