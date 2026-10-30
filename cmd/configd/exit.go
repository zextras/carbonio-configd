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
)

// exitStatus is the error a Kong Run method returns to make main exit with a
// specific status without printing an "Error:" line: the command already wrote
// whatever it had to say on stderr. main is the only place that calls os.Exit.
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

// standaloneRewrite returns the in-process rewrite the cli commands fall back
// to when no daemon answers on the listener port: one forced cycle of
// RunMainLoop, as the legacy "zmconfigd <configs>" standalone mode does.
func standaloneRewrite(disableRestarts bool) rewrite.Standalone {
	return func(ctx context.Context, configs []string) int {
		return runStandaloneRewrite(ctx, &Args{DisableRestarts: disableRestarts, ForcedConfigs: configs})
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
