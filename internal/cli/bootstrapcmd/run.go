// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// RunOptions are the options of "configd bootstrap run".
type RunOptions struct {
	Options
	// DryRun prints the steps that would apply and changes nothing.
	DryRun bool
}

// enabledHost reports the enabled packages of the node as its roles: on an
// existing install a package can be installed and disabled.
type enabledHost struct {
	bootstrap.Host
	roles []string
}

func (h enabledHost) InstalledRoles() []string { return h.roles }

// RunSetup executes "configd bootstrap run" and returns the exit status.
func RunSetup(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer, opts *RunOptions) int {
	if os.Geteuid() != 0 && !opts.DryRun {
		_, _ = fmt.Fprintln(stderr, "Error: configd bootstrap run must run as root")

		return 1
	}

	paths := setup.DefaultPaths()

	var logFile io.Writer

	if !opts.DryRun {
		f, err := setup.OpenLogFile(paths.Path("log"), env.Clock.Now())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

			return 1
		}
		defer func() { _ = f.Close() }()

		logFile = f

		_, _ = fmt.Fprintf(stdout, "Logging to %s\n", f.Name())
	}

	e := &setup.Env{
		Ctx:   ctx,
		Sys:   env,
		Log:   setup.NewLog(stdout, logFile),
		Ctl:   services.NewController(env),
		Paths: paths,
	}

	return runSetup(e, stdin, stdout, stderr, opts, realDeps(ctx, env, stdin, stdout), setup.Steps())
}

func runSetup(e *setup.Env, stdin io.Reader, stdout, stderr io.Writer, opts *RunOptions,
	deps Deps, steps []setup.Step,
) int {
	facts, err := setup.DetectFacts(e)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: reading the node: %v\n", err)

		return 1
	}

	e.Facts = facts
	deps.Host = enabledHost{Host: deps.Host, roles: facts.EnabledRoles()}

	// setup.pl reads LDAP with the operator's settings already loaded: a
	// new node needs them to find its master. Resolve the operator layers
	// alone (no wizard, no validation) for that lookup.
	quiet := opts.Options
	quiet.NonInteractive = true
	quiet.Output = ""

	if pre, err := resolve(e.Ctx, e.Sys, stdin, io.Discard, io.Discard, &quiet, deps); err == nil {
		e.Cfg = &pre.Config
	}

	// Joining a master writes its connection data to localconfig: not in a
	// dry run.
	if !opts.DryRun || !facts.NewInstall {
		existing, err := setup.ExistingValues(e)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: reading the existing configuration: %v\n", err)

			return 1
		}

		deps.Existing = existing
	}

	r, ok := resolveValid(e.Ctx, e.Sys, stdin, stdout, stderr, &opts.Options, deps)
	if !ok {
		return 1
	}

	e.Cfg = &r.Config

	if opts.Output != "" {
		if err := save(e.Sys, r, opts.Output); err != nil {
			printErrors(stderr, err)

			return 1
		}
	}

	results, err := setup.Run(e, steps, opts.DryRun)
	if opts.DryRun {
		printPlan(stdout, results)
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\nFix the cause and run configd bootstrap run again: "+
			"the steps already done are skipped.\n", err)

		return 1
	}

	if !opts.DryRun {
		e.Log.Info("Configuration complete.")
	}

	return 0
}

func printPlan(w io.Writer, results []setup.Result) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "STEP\tSTATUS")

	for _, r := range results {
		status := r.Outcome.String()
		if r.Err != nil {
			status += ": " + r.Err.Error()
		}

		_, _ = fmt.Fprintf(tw, "%s\t%s\n", r.Step.Title, status)
	}

	_ = tw.Flush()
}
