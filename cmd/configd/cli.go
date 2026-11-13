// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/plancmd"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// CLI holds the parsed command line: the global flags and one struct per
// command. newRootCmd binds the flags to its fields.
type CLI struct {
	Version         bool
	DisableRestarts bool

	ProfilingArgs
	TracingArgs

	Localconfig LocalconfigCmd
	Service     ServiceCmd
	Status      StatusCmd
	Control     ControlCmd
	Init        InitCmd
	Proxy       ProxyCmd
	TLS         TLSCmd
	LDAP        LDAPCmd
	Rewrite     RewriteCmd
	Plan        PlanCmd
	Shadow      ShadowCmd
	Release     ReleaseCmd
	Stat        StatCmd
	Daemon      DaemonCmd
}

// newRootCmd builds the command tree of the binary named name, binding its
// flags and arguments to cli and running the commands on env with std. Without
// a subcommand the root runs the daemon.
func newRootCmd(name string, cli *CLI, env sys.System, std *stdio) (*cobra.Command, *app) {
	a := &app{cli: cli, env: env, std: std}

	configureCobra()

	root := &cobra.Command{
		Use:               name + " <command>",
		Short:             "Configuration management daemon for Carbonio",
		Args:              posArgs(nil, nil, false),
		SilenceUsage:      true,
		SilenceErrors:     true,
		TraverseChildren:  true,
		PersistentPreRunE: a.printVersion,
		RunE: func(*cobra.Command, []string) error {
			a.started = true

			return cli.Daemon.Run(cli, env, std)
		},
	}

	root.SetOut(std.Out)
	root.SetErr(std.Err)
	root.SetHelpTemplate(helpTemplate)
	root.SetUsageTemplate(helpTemplate)
	root.SetHelpCommand(a.helpCommand())
	root.SetFlagErrorFunc(flagError)

	pf := root.PersistentFlags()
	pf.SortFlags = false
	pf.BoolP("help", "h", false, "Show context-sensitive help.")
	pf.BoolVarP(&cli.Version, "version", "v", false, "Print version information and exit")
	pf.BoolVar(&cli.DisableRestarts, "disable-restarts", false, "Disable all service restarts (dry-run mode)")
	cli.ProfilingArgs.register(pf)
	cli.TracingArgs.register(pf)

	root.AddCommand(
		a.localconfigCmd(),
		a.serviceCmd(),
		a.statusCmd(),
		a.controlCmd(),
		a.initCmd(),
		a.proxyCmd(),
		a.tlsCmd(),
		a.ldapCmd(),
		a.rewriteCmd(),
		a.planCmd(),
		a.shadowCmd(),
		a.releaseCmd(),
		a.statCmd(),
		a.daemonCmd(),
	)

	// Traversal reads the flag sets before cobra merges the inherited flags
	// into them; merge now so "--disable-restarts <verb>" is not read as a flag
	// that takes the verb as its value.
	mergeInheritedFlags(root)

	return root, a
}

var cobraSetup sync.Once

// configureCobra sets what cobra keeps process-wide: the commands are listed in
// the order they are declared, not alphabetically, and the help template can
// format the flags.
func configureCobra() {
	cobraSetup.Do(func() {
		cobra.EnableCommandSorting = false

		cobra.AddTemplateFunc("flagUsages", flagUsages)
	})
}

// mergeInheritedFlags makes every command's flag set include the persistent
// flags of its ancestors.
func mergeInheritedFlags(cmd *cobra.Command) {
	_ = cmd.InheritedFlags()

	for _, sub := range cmd.Commands() {
		mergeInheritedFlags(sub)
	}
}

// flagError words a flag parsing error the way the command line always has:
// "unknown flag --x" and "unknown flag -x".
func flagError(_ *cobra.Command, err error) error {
	msg := err.Error()

	if flag, ok := strings.CutPrefix(msg, "unknown flag: "); ok {
		return errors.New("unknown flag " + flag)
	}

	if rest, ok := strings.CutPrefix(msg, "unknown shorthand flag: '"); ok {
		if letter, _, found := strings.Cut(rest, "'"); found {
			return errors.New("unknown flag -" + letter)
		}
	}

	return err
}

// printVersion is the root PersistentPreRunE: -v/--version, accepted before or
// after any command, prints the version and ends the run with status 0.
func (a *app) printVersion(*cobra.Command, []string) error {
	if !a.cli.Version {
		return nil
	}

	_, _ = fmt.Fprintln(a.std.Out, formatVersion())

	return exitStatus(0)
}

// helpCommand is the hidden "help [command]" cobra would otherwise list: the
// command tree never had one, but the usual spelling keeps working.
func (a *app) helpCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "help [command]",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, _, err := cmd.Root().Find(args)
			if err != nil || target == nil {
				return fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
			}

			return target.Help()
		},
	}
}

// helpTemplate is the layout of --help: the usage line first, then the
// description, the commands and the flags.
const helpTemplate = `Usage: {{.UseLine}}
{{with (or .Long .Short)}}
{{. | trimTrailingWhitespaces}}
{{end}}{{if .HasAvailableSubCommands}}
Commands:
{{range .Commands}}{{if .IsAvailableCommand}}  {{rpad .Name .NamePadding}}  {{.Short}}
{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}
Flags:
{{flagUsages .LocalFlags | trimTrailingWhitespaces}}
{{end}}{{if .HasAvailableInheritedFlags}}
Global flags:
{{flagUsages .InheritedFlags | trimTrailingWhitespaces}}
{{end}}{{if .HasAvailableSubCommands}}
Run "{{.CommandPath}} <command> --help" for more information on a command.
{{end}}`

// daemonCmd is the hidden "daemon" command: what the bare command runs.
func (a *app) daemonCmd() *cobra.Command {
	cmd := a.leaf("daemon", "Run the daemon", posArgs(nil, nil, false), nil, func() error {
		return a.cli.Daemon.Run(a.cli, a.env, a.std)
	})
	cmd.Hidden = true

	return cmd
}

// DaemonCmd handles the default behavior: run the daemon.
type DaemonCmd struct{}

// Run executes the daemon command.
func (c *DaemonCmd) Run(cli *CLI, env sys.System, std *stdio) error {
	if !cliutil.RequireZextras(std.Err) {
		return exitStatus(1)
	}

	ctx := cliutil.InitLogging(std.Err)
	ctx = logger.ContextWithComponent(ctx, "main")

	notifier, err := sdnotify.New()
	if err != nil {
		logger.ErrorContext(ctx, "Failed to initialize sd_notify", "error", err)
	}

	if notifier != nil {
		logger.InfoContext(ctx, "SD notify enabled, will notify systemd on readiness")
	}

	args := cli.toArgs()

	profilingConfig, tracingConfig, err := setupProfilingAndTracing(ctx, args)
	if err != nil {
		return exitStatus(1)
	}

	rt, err := buildRuntime(ctx, env, &runtimeOpts{disableRestarts: args.DisableRestarts})
	if err != nil {
		logger.FatalContext(ctx, "Failed to initialize config", "error", err)
	}

	exitCode := RunMainLoop(ctx, rt, args, notifier)

	logger.InfoContext(ctx, "Process exited", "program", rt.cfg.Progname)

	// Stop profiling/tracing explicitly before returning the exit status so the
	// profile is flushed before main exits the process. Both helpers are nil-safe.
	StopProfiling(profilingConfig)
	StopTracing(tracingConfig)

	return exitWith(exitCode)
}

// RewriteCmd sends a forced configuration rewrite to the running daemon.
type RewriteCmd struct {
	ConfigNames []string
}

func (a *app) rewriteCmd() *cobra.Command {
	c := &a.cli.Rewrite

	cmd := a.leaf("rewrite <config-names> ...", "Force configuration rewrite",
		posArgs([]string{"<config-names>"}, nil, true),
		func(_ *cobra.Command, args []string) error {
			c.ConfigNames = args

			return nil
		},
		func() error { return c.Run(a.cli, a.env, a.std) })
	cmd.ValidArgsFunction = a.completeSections(true)

	return describeArgs(cmd, [2]string{"<config-names> ...", "Config names to force rewrite (e.g., mta, proxy, all)"})
}

// Run executes the rewrite command.
func (c *RewriteCmd) Run(_ *CLI, env sys.System, std *stdio) error {
	return exitWith(rewrite.Run(context.Background(), std.Out, std.Err, &rewrite.Options{
		Configs:    c.ConfigNames,
		Standalone: standaloneRewrite(env, false),
	}))
}

// PlanCmd shows what a forced rewrite would change on this host.
type PlanCmd struct {
	Sections []string
	JSON     bool
}

func (a *app) planCmd() *cobra.Command {
	c := &a.cli.Plan

	cmd := a.leaf("plan [<sections> ...]", "Show what a rewrite would change, without changing anything",
		posArgs(nil, nil, true),
		func(_ *cobra.Command, args []string) error {
			c.Sections = args

			return nil
		},
		func() error { return c.Run(a.cli, a.env, a.std) })
	cmd.Flags().BoolVar(&c.JSON, "json", false, "Print the plan as JSON")
	cmd.ValidArgsFunction = a.completeSections(false)

	return describeArgs(cmd, [2]string{"[<sections> ...]", "Sections to plan (default: all)"})
}

// Run executes the plan command. The exit status is 0 when nothing would
// change, 1 when a rewrite would change something and 2 on error.
func (c *PlanCmd) Run(_ *CLI, env sys.System, std *stdio) error {
	return exitWith(plancmd.Run(context.Background(), std.Out, std.Err, &plancmd.Options{
		Sections: c.Sections,
		JSON:     c.JSON,
		Planner:  planOnHost(env),
	}))
}

// toArgs converts the parsed CLI globals to an Args struct.
func (c *CLI) toArgs() *Args {
	args := &Args{
		DisableRestarts: c.DisableRestarts,
	}

	c.ProfilingArgs.applyTo(args)
	c.TracingArgs.applyTo(args)

	return args
}

// formatVersion builds a version string from build metadata.
func formatVersion() string {
	vi := GetVersionInfo()

	version := fmt.Sprintf("configd %s", vi.Version)

	if vi.Revision != "" {
		short := vi.Revision
		if len(short) > 12 {
			short = short[:12]
		}

		version += fmt.Sprintf(" (%s", short)

		if vi.Modified {
			version += ", dirty"
		}

		if vi.Time != "" {
			version += fmt.Sprintf(", %s", vi.Time)
		}

		version += ")"
	}

	return version
}
