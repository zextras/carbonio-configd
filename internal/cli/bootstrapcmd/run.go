// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/bootstrap/brand"
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
	deps.Drops = &LegacyDrops{}

	if !opts.DryRun {
		f, err := setup.OpenLogFile(paths.Path("log"), env.Clock.Now())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

			return 1
		}
		defer func() { _ = f.Close() }()

		logFile = f

		deps.LogPath = f.Name()

		if screens {
			session.LogPath = f.Name()
		}
	}

	e := &setup.Env{
		Ctx:   ctx,
		Sys:   env,
		Log:   setup.NewLog(logOut, logFile),
		Ctl:   services.NewController(env),
		Paths: paths,

		Verbose: opts.Verbose,
	}

	return runSetup(e, stdin, stdout, stderr, opts, &deps, setup.Steps())
}

func runSetup(e *setup.Env, stdin io.Reader, stdout, stderr io.Writer, opts *RunOptions,
	deps *Deps, steps []setup.Step,
) int {
	logStart(e, opts, deps)

	if !loadConfig(e, stdin, stdout, stderr, opts, deps) {
		e.Log.Detail("Result: stopped before any step (configuration not loaded)")

		return 1
	}

	if deps.Session == nil || !useScreens(&opts.Options, deps) {
		return runPlain(e, stdout, stderr, opts, deps, steps)
	}

	if err := applyScreen(e, stdin, stdout, deps, steps); err != nil {
		printFailure(stderr, err)

		return 1
	}

	return 0
}

func printFailure(stderr io.Writer, err error) {
	_, _ = fmt.Fprintf(stderr, "Error: %v\n%s\n", err, resumeHint)
}

const resumeHint = "Fix the cause and run carbonio-bootstrap again: the steps already done are skipped."

// runPlain runs the steps with the line output: a header, a line per step
// and a summary; a dry run prints the plan.
func runPlain(e *setup.Env, stdout, stderr io.Writer, opts *RunOptions, deps *Deps, steps []setup.Step) int {
	if opts.DryRun {
		results, err := setup.Run(e, steps, true)
		printPlan(stdout, results)

		if err != nil {
			printFailure(stderr, err)

			return 1
		}

		return 0
	}

	rep := setup.NewReporter(&setup.ReportOptions{
		Out: stdout, Err: stderr, Plan: planOf(planResults(e, steps)),
		Color: deps.Display.Color, Live: deps.Display.Live, Width: deps.Display.Width,
		Dark: brand.DarkBackground(os.Environ()),
	})
	rep.Header(headerOf(e, opts, deps))

	e.Log.OnNote(rep.Note)
	e.Observe = rep.Observe

	start := e.Sys.Clock.Now()
	results, err := setup.Run(e, steps, false)
	setup.LogSummary(e, results, err, e.Sys.Clock.Now().Sub(start))
	rep.Summary(&setup.Summary{Err: err, Next: setup.NextSteps(e)})

	if err != nil {
		return 1
	}

	return 0
}

// planResults says, with a dry run, which steps apply to the node and which
// are already done; the dry run's log lines are not the run's.
func planResults(e *setup.Env, steps []setup.Step) []setup.Result {
	live := e.Log
	e.Log = setup.NewLog(io.Discard, nil)
	results, _ := setup.Run(e, steps, true)
	e.Log = live

	return results
}

// planOf lists the steps that apply to the node.
func planOf(results []setup.Result) []setup.PlanStep {
	var plan []setup.PlanStep

	for i, res := range results {
		if res.Outcome != setup.Skipped {
			plan = append(plan, setup.PlanStep{Index: i, Title: res.Step.Title})
		}
	}

	return plan
}

// installKind names what the node is, for the header and the screens.
func installKind(f *setup.Facts) string {
	if f.NewInstall {
		return "new install"
	}

	return "existing install"
}

func headerOf(e *setup.Env, opts *RunOptions, deps *Deps) *setup.Header {
	h := &setup.Header{Hostname: e.Cfg.Host.Hostname, Install: installKind(e.Facts), LogPath: deps.LogPath}

	if v, err := e.Sys.FS.ReadFile(e.Paths.Path(".version")); err == nil {
		h.Version = strings.TrimSpace(string(v))
	}

	switch {
	case opts.LegacyPath != "":
		h.Config, h.ConfigKind = opts.LegacyPath, "legacy"
	case opts.ConfigPath != "":
		h.Config, h.ConfigKind = opts.ConfigPath, "yaml"
	}

	if deps.Drops == nil {
		return h
	}

	h.Dropped = len(deps.Drops.List)

	if opts.Verbose {
		for _, d := range deps.Drops.List {
			h.DropLines = append(h.DropLines, d.Key+": "+d.Reason)
		}
	}

	return h
}

// applyScreen runs the steps while the apply page shows their progress.
func applyScreen(e *setup.Env, stdin io.Reader, stdout io.Writer, deps *Deps, steps []setup.Step) error {
	s := deps.Session

	var plan []tui.PlanStep

	for i, res := range planResults(e, steps) {
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

			start := e.Sys.Clock.Now()
			results, err := setup.Run(e, steps, false)
			setup.LogSummary(e, results, err, e.Sys.Clock.Now().Sub(start))

			return err
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
		deps.Session.Install = installKind(facts)
	}

	deps.Host = enabledHost{Host: deps.Host, roles: facts.EnabledRoles()}

	// setup.pl reads LDAP with the operator's settings already loaded: a
	// new node needs them to find its master. Resolve the operator layers
	// alone (no wizard, no validation) for that lookup.
	quiet := opts.Options
	quiet.NonInteractive = true
	quiet.Output = ""

	preDeps := *deps
	preDeps.Drops = nil

	if pre, err := resolve(e.Ctx, e.Sys, stdin, io.Discard, io.Discard, &quiet, &preDeps); err == nil {
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

	r, ok := resolveValid(e.Ctx, e.Sys, stdin, stdout, stderr, &opts.Options, deps)
	if !ok {
		return false
	}

	e.Cfg = &r.Config

	logConfig(e, r)

	if deps.Drops != nil {
		logDrops(e.Log, deps.Drops.List)
	}

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

// logDrops records the legacy keys not imported in the log file.
func logDrops(l *setup.Log, drops []bootstrap.LegacyDrop) {
	for _, d := range drops {
		l.Detail("legacy key %s not imported: %s", d.Key, d.Reason)
	}
}
