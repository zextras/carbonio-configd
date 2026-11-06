// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zextras/carbonio-configd/internal/cli/service"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const norewriteArg = "norewrite"

// ServiceCmd handles the "configd service" subcommand. The verbs are those of
// the legacy zm*ctl scripts: the core wrappers exec
// `configd service "${1:-}" <name> "${@:2}"`, so every verb accepts trailing
// legacy arguments (norewrite, ...) and an empty or unknown verb ends in the
// usage message instead of a parse error.
type ServiceCmd struct {
	List           ServiceListCmd
	Start          ServiceStartCmd
	Stop           ServiceStopCmd
	Kill           ServiceKillCmd
	Restart        ServiceRestartCmd
	Reload         ServiceReloadCmd
	Status         ServiceStatusCmd
	Refresh        ServiceRefreshCmd
	Rotate         ServiceRotateCmd
	StartSystemd   ServiceStartSystemdCmd
	StopSystemd    ServiceStopSystemdCmd
	RestartSystemd ServiceRestartSystemdCmd
	Usage          ServiceUsageCmd
}

// serviceCmd builds "configd service". The command itself is what an empty or
// unknown verb falls through to, and swallows whatever flags such a call has.
func (a *app) serviceCmd() *cobra.Command {
	s := &a.cli.Service

	cmd := &cobra.Command{
		Use:                "service <command>",
		Short:              "Manage system services",
		Args:               cobra.ArbitraryArgs,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		PreRunE: func(_ *cobra.Command, args []string) error {
			s.Usage.Args = args

			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			a.started = true

			return s.Usage.Run(a.env, a.std)
		},
	}

	list := a.leaf("list", "List all services with status", posArgs(nil, nil, false), nil,
		func() error { return s.List.Run(a.env, a.std) })

	cmd.AddCommand(
		list,
		a.serviceVerb("start", "Start a service", false, &s.Start.serviceArgs, &s.Start.NoRewrite,
			func() error { return s.Start.Run(a.env, a.std) }),
		a.serviceVerb("stop", "Stop a service", false, &s.Stop.serviceArgs, nil,
			func() error { return s.Stop.Run(a.env, a.std) }),
		a.serviceVerb("kill", "Stop a service (legacy alias of stop)", false, &s.Kill.serviceArgs, nil,
			func() error { return s.Kill.Run(a.env, a.std) }),
		a.serviceVerb("restart", "Restart a service", false, &s.Restart.serviceArgs, &s.Restart.NoRewrite,
			func() error { return s.Restart.Run(a.env, a.std) }),
		a.serviceVerb("reload", "Reload a service", false, &s.Reload.serviceArgs, nil,
			func() error { return s.Reload.Run(a.env, a.std) }),
		a.serviceVerb("status", "Show service status", false, &s.Status.serviceArgs, nil,
			func() error { return s.Status.Run(a.env, a.std) }),
		a.serviceVerb("refresh", "Make a service re-read its configuration (milter)", false, &s.Refresh.serviceArgs, nil,
			func() error { return s.Refresh.Run(a.env, a.std) }),
		a.serviceVerb("rotate", "Make a service reopen its logs (stats)", false, &s.Rotate.serviceArgs, nil,
			func() error { return s.Rotate.Run(a.env, a.std) }),
		a.serviceVerb("start-systemd", "systemd ExecStart leaf", true, &s.StartSystemd.serviceArgs, nil,
			func() error { return s.StartSystemd.Run(a.env, a.std) }),
		a.serviceVerb("stop-systemd", "systemd ExecStop leaf", true, &s.StopSystemd.serviceArgs, nil,
			func() error { return s.StopSystemd.Run(a.env, a.std) }),
		a.serviceVerb("restart-systemd", "stop then start leaf", true, &s.RestartSystemd.serviceArgs, nil,
			func() error { return s.RestartSystemd.Run(a.env, a.std) }),
	)

	return cmd
}

// serviceVerb builds one service verb: "<verb> <name> [<extra> ...]", with
// --no-rewrite when noRewrite is set. Flags are parsed up to the first extra
// argument; from there on everything is handed over as it is.
func (a *app) serviceVerb(
	verb, short string, hidden bool, args *serviceArgs, noRewrite *bool, run func() error,
) *cobra.Command {
	validate := posArgs([]string{"<name>"}, []string{"[<extra> ...]"}, true)
	cmd := a.leaf(verb+" <name> [<extra> ...]", short, validate, args.bind, run)
	cmd.Hidden = hidden
	cmd.Flags().SetInterspersed(false)
	cmd.ValidArgsFunction = a.completeFirstService

	if noRewrite != nil {
		cmd.Flags().BoolVar(noRewrite, "no-rewrite", false, "Skip config regeneration")
	}

	return describeArgs(cmd, [2]string{"<name>", "Service name"}, [2]string{"[<extra> ...]", "Legacy wrapper arguments"})
}

// runService executes one "configd service" action.
func runService(env sys.System, std *stdio, opts *service.Options) error {
	return exitWith(service.Run(context.Background(), env, std.Out, std.Err, opts))
}

// serviceArgs are the positional arguments every service verb takes: the
// service and whatever the legacy wrapper forwards after it. Extra swallows
// anything, flags included, so a wrapper call never fails on a stray argument.
type serviceArgs struct {
	Name  string
	Extra []string
}

// bind takes the service and the extras from the arguments cobra left after
// the leading flags. A flag the verb knows right after the service
// ("start proxy --no-rewrite") is still a flag; the first other argument starts
// the extras, which are kept as given, flags included.
func (a *serviceArgs) bind(cmd *cobra.Command, rest []string) error {
	a.Name = rest[0]
	a.Extra = peelKnownFlags(cmd.Flags(), rest[1:])

	if len(a.Extra) == 0 {
		a.Extra = nil
	}

	return nil
}

// peelKnownFlags parses the leading arguments of rest that are flags of fs and
// returns the others. Help and version are left in: after the service name they
// belong to the legacy wrapper.
func peelKnownFlags(fs *pflag.FlagSet, rest []string) []string {
	for len(rest) > 0 {
		n := knownFlagLen(fs, rest)
		if n == 0 || fs.Parse(rest[:n]) != nil {
			break
		}

		rest = rest[n:]
	}

	return rest
}

// isWrapperFlag reports whether f is help or version, which the legacy
// wrapper's extras may spell.
func isWrapperFlag(f *pflag.Flag) bool { return f.Name == "help" || f.Name == "version" }

// knownFlagLen is how many arguments of rest the leading flag takes: 0 when it
// is not a flag of fs, 1 for a switch or "--flag=value", 2 for a flag with a
// separate value.
func knownFlagLen(fs *pflag.FlagSet, rest []string) int {
	tok := rest[0]

	var f *pflag.Flag

	switch {
	case strings.HasPrefix(tok, "--"):
		name, _, hasValue := strings.Cut(tok[2:], "=")

		f = fs.Lookup(name)
		if f != nil && hasValue && !isWrapperFlag(f) {
			return 1
		}
	case len(tok) == 2 && tok[0] == '-':
		f = fs.ShorthandLookup(tok[1:])
	}

	switch {
	case f == nil, isWrapperFlag(f):
		return 0
	case f.NoOptDefVal != "":
		return 1
	case len(rest) > 1:
		return 2
	default:
		return 0
	}
}

// options builds the service.Options of one verb invocation.
func (a *serviceArgs) options(action service.Action) *service.Options {
	return &service.Options{
		Action:    action,
		Name:      a.Name,
		Extra:     a.Extra,
		NoRewrite: slices.Contains(a.Extra, norewriteArg),
	}
}

// ServiceListCmd lists all services with their status.
type ServiceListCmd struct{}

// Run executes the service list command.
func (c *ServiceListCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, &service.Options{Action: service.ActionList})
}

// ServiceStartCmd starts a service.
type ServiceStartCmd struct {
	serviceArgs
	NoRewrite bool
}

// Run executes the service start command. Both the --no-rewrite flag and the
// legacy "norewrite" positional argument are honored.
func (c *ServiceStartCmd) Run(env sys.System, std *stdio) error {
	opts := c.options(service.ActionStart)
	opts.NoRewrite = opts.NoRewrite || c.NoRewrite

	return runService(env, std, opts)
}

// ServiceStartSystemdCmd is the systemd ExecStart leaf: it launches the service
// in-process and never calls systemctl, so it is safe to invoke from the
// carbonio-<name>.service unit without re-entering systemctl.
type ServiceStartSystemdCmd struct {
	serviceArgs
}

// Run executes the systemd-leaf start command.
func (c *ServiceStartSystemdCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionStartSystemd))
}

// ServiceStopSystemdCmd is the systemd ExecStop leaf: it stops the service
// in-process and never calls systemctl.
type ServiceStopSystemdCmd struct {
	serviceArgs
}

// Run executes the systemd-leaf stop command.
func (c *ServiceStopSystemdCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionStopSystemd))
}

// ServiceRestartSystemdCmd stops then starts the service in-process, never
// calling systemctl (legacy zmstatctl restart-systemd).
type ServiceRestartSystemdCmd struct {
	serviceArgs
}

// Run executes the systemd-leaf restart command.
func (c *ServiceRestartSystemdCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionRestartSystemd))
}

// ServiceStopCmd stops a service.
type ServiceStopCmd struct {
	serviceArgs
}

// Run executes the service stop command.
func (c *ServiceStopCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionStop))
}

// ServiceKillCmd stops a service; legacy zm*ctl scripts treat kill and stop
// alike.
type ServiceKillCmd struct {
	serviceArgs
}

// Run executes the service kill command.
func (c *ServiceKillCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionKill))
}

// ServiceRestartCmd restarts a service.
type ServiceRestartCmd struct {
	serviceArgs
	NoRewrite bool
}

// Run executes the service restart command.
func (c *ServiceRestartCmd) Run(env sys.System, std *stdio) error {
	opts := c.options(service.ActionRestart)
	opts.NoRewrite = opts.NoRewrite || c.NoRewrite

	return runService(env, std, opts)
}

// ServiceReloadCmd reloads a service.
type ServiceReloadCmd struct {
	serviceArgs
}

// Run executes the service reload command.
func (c *ServiceReloadCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionReload))
}

// ServiceStatusCmd shows detailed status for a service.
type ServiceStatusCmd struct {
	serviceArgs
}

// Run executes the service status command.
func (c *ServiceStatusCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionStatus))
}

// ServiceRefreshCmd signals a service to re-read its configuration.
type ServiceRefreshCmd struct {
	serviceArgs
}

// Run executes the service refresh command.
func (c *ServiceRefreshCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionRefresh))
}

// ServiceRotateCmd signals a service to reopen its logs.
type ServiceRotateCmd struct {
	serviceArgs
}

// Run executes the service rotate command.
func (c *ServiceRotateCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, c.options(service.ActionRotate))
}

// ServiceUsageCmd catches an empty or unknown verb (the wrappers forward
// "${1:-}" unchecked) and prints the legacy usage line.
type ServiceUsageCmd struct {
	Args []string
}

// Run prints the usage and exits 1.
func (c *ServiceUsageCmd) Run(env sys.System, std *stdio) error {
	return runService(env, std, &service.Options{Action: service.ActionUsage, Extra: c.Args})
}
