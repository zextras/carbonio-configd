// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package plancmd implements "configd plan": it shows what a forced rewrite of
// the configuration would change on this host, and why, without changing
// anything.
package plancmd

import (
	"context"
	"fmt"
	"io"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/plan"
)

// Exit statuses of "configd plan".
const (
	// ExitNoChanges means the host already matches the configuration.
	ExitNoChanges = 0
	// ExitChanges means a rewrite would change the host.
	ExitChanges = 1
	// ExitError means the plan could not be computed.
	ExitError = 2
)

// Planner computes the read-only plan of sections (every section when empty)
// against the files and state on disk.
type Planner func(ctx context.Context, sections []string) (*plan.Plan, error)

// Options are the parsed arguments of "configd plan".
type Options struct {
	// Sections limits the plan to these configd.cf sections.
	Sections []string
	// JSON selects the machine-readable output.
	JSON bool
	// Planner computes the plan.
	Planner Planner
}

// Run checks the environment, then plans and prints. It returns the process
// exit status.
func Run(_ context.Context, stdout, stderr io.Writer, opts *Options) int {
	if !cliutil.RequireZextras(stderr) {
		return ExitError
	}

	return Execute(cliutil.InitLogging(stderr), stdout, stderr, opts)
}

// Execute computes the plan and prints it on stdout (diagnostics go to
// stderr). It returns ExitNoChanges for an empty plan, ExitChanges otherwise
// and ExitError when the plan cannot be computed or printed.
func Execute(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	pl, err := opts.Planner(ctx, opts.Sections)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return ExitError
	}

	if err := write(stdout, pl, opts.JSON); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return ExitError
	}

	if pl.IsEmpty() {
		return ExitNoChanges
	}

	return ExitChanges
}

func write(w io.Writer, pl *plan.Plan, asJSON bool) error {
	if !asJSON {
		return pl.WriteText(w)
	}

	data, err := pl.JSON()
	if err != nil {
		return err
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}

	return nil
}
