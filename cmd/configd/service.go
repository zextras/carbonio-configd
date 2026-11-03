// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"slices"

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
	List           ServiceListCmd           `cmd:"" help:"List all services with status"`
	Start          ServiceStartCmd          `cmd:"" help:"Start a service"`
	Stop           ServiceStopCmd           `cmd:"" help:"Stop a service"`
	Kill           ServiceKillCmd           `cmd:"" help:"Stop a service (legacy alias of stop)"`
	Restart        ServiceRestartCmd        `cmd:"" help:"Restart a service"`
	Reload         ServiceReloadCmd         `cmd:"" help:"Reload a service"`
	Status         ServiceStatusCmd         `cmd:"" help:"Show service status"`
	Refresh        ServiceRefreshCmd        `cmd:"" help:"Make a service re-read its configuration (milter)"`
	Rotate         ServiceRotateCmd         `cmd:"" help:"Make a service reopen its logs (stats)"`
	StartSystemd   ServiceStartSystemdCmd   `cmd:"" name:"start-systemd" hidden:"" help:"systemd ExecStart leaf"`
	StopSystemd    ServiceStopSystemdCmd    `cmd:"" name:"stop-systemd" hidden:"" help:"systemd ExecStop leaf"`
	RestartSystemd ServiceRestartSystemdCmd `cmd:"" name:"restart-systemd" hidden:"" help:"stop then start leaf"`
	Usage          ServiceUsageCmd          `cmd:"" default:"withargs" hidden:"" help:"Print the verb list"`
}

// runService executes one "configd service" action.
func runService(env sys.System, opts *service.Options) error {
	return exitWith(service.Run(context.Background(), env, os.Stdout, os.Stderr, opts))
}

// serviceArgs are the positional arguments every service verb takes: the
// service and whatever the legacy wrapper forwards after it. Extra swallows
// anything, flags included, so a wrapper call never fails on a stray argument.
type serviceArgs struct {
	Name  string   `arg:"" help:"Service name"`
	Extra []string `arg:"" optional:"" passthrough:"" hidden:"" help:"Legacy wrapper arguments"`
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
func (c *ServiceListCmd) Run(env sys.System) error {
	return runService(env, &service.Options{Action: service.ActionList})
}

// ServiceStartCmd starts a service.
type ServiceStartCmd struct {
	serviceArgs
	NoRewrite bool `name:"no-rewrite" help:"Skip config regeneration"`
}

// Run executes the service start command. Both the --no-rewrite flag and the
// legacy "norewrite" positional argument are honored.
func (c *ServiceStartCmd) Run(env sys.System) error {
	opts := c.options(service.ActionStart)
	opts.NoRewrite = opts.NoRewrite || c.NoRewrite

	return runService(env, opts)
}

// ServiceStartSystemdCmd is the systemd ExecStart leaf: it launches the service
// in-process and never calls systemctl, so it is safe to invoke from the
// carbonio-<name>.service unit without re-entering systemctl.
type ServiceStartSystemdCmd struct {
	serviceArgs
}

// Run executes the systemd-leaf start command.
func (c *ServiceStartSystemdCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionStartSystemd))
}

// ServiceStopSystemdCmd is the systemd ExecStop leaf: it stops the service
// in-process and never calls systemctl.
type ServiceStopSystemdCmd struct {
	serviceArgs
}

// Run executes the systemd-leaf stop command.
func (c *ServiceStopSystemdCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionStopSystemd))
}

// ServiceRestartSystemdCmd stops then starts the service in-process, never
// calling systemctl (legacy zmstatctl restart-systemd).
type ServiceRestartSystemdCmd struct {
	serviceArgs
}

// Run executes the systemd-leaf restart command.
func (c *ServiceRestartSystemdCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionRestartSystemd))
}

// ServiceStopCmd stops a service.
type ServiceStopCmd struct {
	serviceArgs
}

// Run executes the service stop command.
func (c *ServiceStopCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionStop))
}

// ServiceKillCmd stops a service; legacy zm*ctl scripts treat kill and stop
// alike.
type ServiceKillCmd struct {
	serviceArgs
}

// Run executes the service kill command.
func (c *ServiceKillCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionKill))
}

// ServiceRestartCmd restarts a service.
type ServiceRestartCmd struct {
	serviceArgs
	NoRewrite bool `name:"no-rewrite" help:"Skip config regeneration"`
}

// Run executes the service restart command.
func (c *ServiceRestartCmd) Run(env sys.System) error {
	opts := c.options(service.ActionRestart)
	opts.NoRewrite = opts.NoRewrite || c.NoRewrite

	return runService(env, opts)
}

// ServiceReloadCmd reloads a service.
type ServiceReloadCmd struct {
	serviceArgs
}

// Run executes the service reload command.
func (c *ServiceReloadCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionReload))
}

// ServiceStatusCmd shows detailed status for a service.
type ServiceStatusCmd struct {
	serviceArgs
}

// Run executes the service status command.
func (c *ServiceStatusCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionStatus))
}

// ServiceRefreshCmd signals a service to re-read its configuration.
type ServiceRefreshCmd struct {
	serviceArgs
}

// Run executes the service refresh command.
func (c *ServiceRefreshCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionRefresh))
}

// ServiceRotateCmd signals a service to reopen its logs.
type ServiceRotateCmd struct {
	serviceArgs
}

// Run executes the service rotate command.
func (c *ServiceRotateCmd) Run(env sys.System) error {
	return runService(env, c.options(service.ActionRotate))
}

// ServiceUsageCmd catches an empty or unknown verb (the wrappers forward
// "${1:-}" unchecked) and prints the legacy usage line.
type ServiceUsageCmd struct {
	Args []string `arg:"" optional:"" passthrough:"" hidden:""`
}

// Run prints the usage and exits 1.
func (c *ServiceUsageCmd) Run(env sys.System) error {
	return runService(env, &service.Options{Action: service.ActionUsage, Extra: c.Args})
}
