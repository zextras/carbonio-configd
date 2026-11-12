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
	"github.com/zextras/carbonio-configd/internal/bootstrap/tui"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// RunOptions are the options of "carbonio-bootstrap run".
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

// RunSetup executes "carbonio-bootstrap run" and returns the exit status.
func RunSetup(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer, opts *RunOptions) int {
	if os.Geteuid() != 0 && !opts.DryRun {
		_, _ = fmt.Fprintln(stderr, "Error: carbonio-bootstrap run must run as root")

		return 1
	}

	paths := setup.DefaultPaths()
	deps := realDeps(ctx, env, stdin, stdout)
	screens := useScreens(&opts.Options, &deps) && !opts.DryRun
	notice := setup.Notice(env.FS, paths)

	var (
		logFile io.Writer
		logOut  = stdout
		session *Session
	)

	if screens {
		session = &Session{Notice: notice, Feed: tui.NewFeed()}
		logOut = session.Feed
	} else {
		setup.WriteNotice(stdout, notice)
	}

	deps.Session = session

	if !opts.DryRun {
		f, err := setup.OpenLogFile(paths.Path("log"), env.Clock.Now())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

			return 1
		}
		defer func() { _ = f.Close() }()

		logFile = f

		if screens {
			session.LogPath = f.Name()
		} else {
			_, _ = fmt.Fprintf(stdout, "Logging to %s\n", f.Name())
		}
	}

	e := &setup.Env{
		Ctx:   ctx,
		Sys:   env,
		Log:   setup.NewLog(logOut, logFile),
		Ctl:   services.NewController(env),
		Paths: paths,
	}

	return runSetup(e, stdin, stdout, stderr, opts, deps, setup.Steps())
}

func runSetup(e *setup.Env, stdin io.Reader, stdout, stderr io.Writer, opts *RunOptions,
	deps Deps, steps []setup.Step,
) int {
	if !loadConfig(e, stdin, stdout, stderr, opts, &deps) {
		return 1
	}

	var err error

	if deps.Session != nil && useScreens(&opts.Options, &deps) {
		err = applyScreen(e, stdin, stdout, &deps, steps)
	} else {
		var results []setup.Result

		results, err = setup.Run(e, steps, opts.DryRun)
		if opts.DryRun {
			printPlan(stdout, results)
		}

		if err == nil && !opts.DryRun {
			e.Log.Info("Configuration complete.")
		}
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\nFix the cause and run carbonio-bootstrap again: "+
			"the steps already done are skipped.\n", err)

		return 1
	}

	return 0
}

// applyScreen runs the steps while the apply page shows their progress.
func applyScreen(e *setup.Env, stdin io.Reader, stdout io.Writer, deps *Deps, steps []setup.Step) error {
	s := deps.Session

	// The plan is what a dry run says; its log lines are not the run's.
	live := e.Log
	e.Log = setup.NewLog(io.Discard, nil)
	results, _ := setup.Run(e, steps, true)
	e.Log = live

	var plan []tui.PlanStep

	for i, res := range results {
		if res.Outcome != setup.Skipped {
			plan = append(plan, tui.PlanStep{Index: i, Title: res.Step.Title, Done: res.Outcome == setup.AlreadyDone})
		}
	}

	ao := &tui.ApplyOptions{
		Plan:      plan,
		Feed:      s.Feed,
		Hostname:  e.Cfg.Host.Hostname,
		Info:      tui.Info{OS: osName(e.Sys), Install: s.Install},
		SavedPath: s.SavedPath,
		LogPath:   s.LogPath,
		NoColor:   noColor(os.Environ()),
		Preflight: func() []tui.Check {
			return tui.Preflight(e.Ctx, &tui.PreflightInput{
				Cfg: e.Cfg, Host: deps.Host, Resolver: deps.Resolver, Root: os.Geteuid() == 0, PortFree: portFree,
			})
		},
		Run: func(obs setup.Observer, stop func() bool) error {
			e.Observe, e.Stop = obs, stop

			if _, err := setup.Run(e, steps, false); err != nil {
				return err
			}

			e.Log.Info("Configuration complete.")

			return nil
		},
	}

	if !s.NoticeShown {
		ao.Notice = s.Notice
	}

	return deps.Screens.Apply(e.Ctx, stdin, stdout, ao)
}

// loadConfig reads the node and resolves the configuration into e.Cfg
// (saving it to --output); false: the problem is already reported.
func loadConfig(e *setup.Env, stdin io.Reader, stdout, stderr io.Writer, opts *RunOptions, deps *Deps) bool {
	facts, err := setup.DetectFacts(e)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: reading the node: %v\n", err)

		return false
	}

	e.Facts = facts

	if deps.Session != nil {
		deps.Session.Install = "existing install"
		if facts.NewInstall {
			deps.Session.Install = "new install"
		}
	}

	deps.Host = enabledHost{Host: deps.Host, roles: facts.EnabledRoles()}

	// setup.pl reads LDAP with the operator's settings already loaded: a
	// new node needs them to find its master. Resolve the operator layers
	// alone (no wizard, no validation) for that lookup.
	quiet := opts.Options
	quiet.NonInteractive = true
	quiet.Output = ""

	if pre, err := resolve(e.Ctx, e.Sys, stdin, io.Discard, io.Discard, &quiet, *deps); err == nil {
		e.Cfg = &pre.Config
	}

	// Joining a master writes its connection data to localconfig: not in a
	// dry run.
	if !opts.DryRun || !facts.NewInstall {
		existing, err := setup.ExistingValues(e)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: reading the existing configuration: %v\n", err)

			return false
		}

		deps.Existing = existing
	}

	r, ok := resolveValid(e.Ctx, e.Sys, stdin, stdout, stderr, &opts.Options, *deps)
	if !ok {
		return false
	}

	e.Cfg = &r.Config

	if opts.Output != "" {
		if err := save(e.Sys, r, opts.Output); err != nil {
			printErrors(stderr, err)

			return false
		}
	}

	return true
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
