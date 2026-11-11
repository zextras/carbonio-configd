// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// exitStatus is the error a command returns to make main exit with a specific
// status without printing an "Error:" line: the command already wrote whatever
// it had to say on stderr. run maps it to the process status and main is the
// only place that calls os.Exit.
type exitStatus int

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(int(e)) }

// exitWith turns the exit status returned by an internal/cli Run into the
// error main understands: nil for success.
func exitWith(code int) error {
	if code == 0 {
		return nil
	}

	return exitStatus(code)
}

// stdio is everything a command takes from the process besides the host
// (sys.System): the standard streams and the command line. main binds the
// real ones; a test binds buffers, so no command reaches for os.Stdout,
// os.Stderr or os.Args itself and every exit status and message is observable.
type stdio struct {
	// In is the standard input (nil: none).
	In io.Reader
	// Out and Err are the standard output and error.
	Out, Err io.Writer
	// Args is the command line, os.Args-shaped (the program name first).
	Args []string
}

// standaloneRewrite returns the in-process rewrite the cli commands fall back
// to when no daemon answers on the listener port: one forced cycle of
// RunMainLoop, as the legacy "zmconfigd <configs>" standalone mode does.
func standaloneRewrite(env sys.System, disableRestarts bool) rewrite.Standalone {
	return func(ctx context.Context, configs []string) int {
		return runStandaloneRewrite(ctx, env, &Args{DisableRestarts: disableRestarts, ForcedConfigs: configs})
	}
}

// exitCode is the process exit status for the error a command returned: 0 for
// nil, the command's own status for an exitStatus (it already reported on
// stderr), and otherwise 1 after an "Error: <err>" line on stderr.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	if status, ok := errors.AsType[exitStatus](err); ok {
		return int(status)
	}

	_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

	return 1
}

// exitStatus is the process exit status for the error Execute returned. An
// exitStatus passes through. Any other error is a usage error ("<name>: error:
// <msg>", status 1, the status of the legacy tools) while no command has
// begun to run, and a failure of the command ("Error: <msg>", status 1) after.
func (a *app) exitStatus(err error, name string, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	if _, ok := errors.AsType[exitStatus](err); ok || a.started {
		return exitCode(err, stderr)
	}

	_, _ = fmt.Fprintf(stderr, "%s: error: %v\n", name, err)

	return 1
}
