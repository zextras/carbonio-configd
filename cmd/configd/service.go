// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"slices"

	"github.com/zextras/carbonio-configd/internal/cli/service"
)

const norewriteArg = "norewrite"

// ServiceCmd handles the "configd service" subcommand.
type ServiceCmd struct {
	List         ServiceListCmd         `cmd:"" help:"List all services with status"`
	Start        ServiceStartCmd        `cmd:"" help:"Start a service"`
	Stop         ServiceStopCmd         `cmd:"" help:"Stop a service"`
	Restart      ServiceRestartCmd      `cmd:"" help:"Restart a service"`
	Reload       ServiceReloadCmd       `cmd:"" help:"Reload a service"`
	Status       ServiceStatusCmd       `cmd:"" help:"Show service status"`
	StartSystemd ServiceStartSystemdCmd `cmd:"" name:"start-systemd" hidden:"" help:"systemd ExecStart leaf"`
	StopSystemd  ServiceStopSystemdCmd  `cmd:"" name:"stop-systemd" hidden:"" help:"systemd ExecStop leaf (no systemctl)"`
}

// runService executes one "configd service" action.
func runService(opts *service.Options) error {
	return exitWith(service.Run(context.Background(), os.Stdout, os.Stderr, opts))
}

// ServiceListCmd lists all services with their status.
type ServiceListCmd struct{}

// Run executes the service list command.
func (c *ServiceListCmd) Run() error {
	return runService(&service.Options{Action: service.ActionList})
}

// ServiceStartCmd starts a service.
type ServiceStartCmd struct {
	Name      string   `arg:"" help:"Service name"`
	NoRewrite bool     `name:"no-rewrite" help:"Skip config regeneration"`
	Extra     []string `arg:"" optional:"" hidden:""`
}

// Run executes the service start command.
func (c *ServiceStartCmd) Run() error {
	// Supports both --no-rewrite flag and legacy "norewrite" positional arg.
	return runService(&service.Options{
		Action:    service.ActionStart,
		Name:      c.Name,
		NoRewrite: c.NoRewrite || slices.Contains(c.Extra, norewriteArg),
	})
}

// ServiceStartSystemdCmd is the systemd ExecStart leaf: it launches the service
// in-process and never calls systemctl, so it is safe to invoke from the
// carbonio-<name>.service unit without re-entering systemctl.
type ServiceStartSystemdCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the systemd-leaf start command.
func (c *ServiceStartSystemdCmd) Run() error {
	return runService(&service.Options{Action: service.ActionStartSystemd, Name: c.Name})
}

// ServiceStopSystemdCmd is the systemd ExecStop leaf: it stops the service
// in-process and never calls systemctl.
type ServiceStopSystemdCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the systemd-leaf stop command.
func (c *ServiceStopSystemdCmd) Run() error {
	return runService(&service.Options{Action: service.ActionStopSystemd, Name: c.Name})
}

// ServiceStopCmd stops a service.
type ServiceStopCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the service stop command.
func (c *ServiceStopCmd) Run() error {
	return runService(&service.Options{Action: service.ActionStop, Name: c.Name})
}

// ServiceRestartCmd restarts a service.
type ServiceRestartCmd struct {
	Name      string   `arg:"" help:"Service name"`
	NoRewrite bool     `name:"no-rewrite" help:"Skip config regeneration"`
	Extra     []string `arg:"" optional:"" hidden:""`
}

// Run executes the service restart command.
func (c *ServiceRestartCmd) Run() error {
	return runService(&service.Options{
		Action:    service.ActionRestart,
		Name:      c.Name,
		NoRewrite: c.NoRewrite || slices.Contains(c.Extra, norewriteArg),
	})
}

// ServiceReloadCmd reloads a service.
type ServiceReloadCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the service reload command.
func (c *ServiceReloadCmd) Run() error {
	return runService(&service.Options{Action: service.ActionReload, Name: c.Name})
}

// ServiceStatusCmd shows detailed status for a service.
type ServiceStatusCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the service status command.
func (c *ServiceStatusCmd) Run() error {
	return runService(&service.Options{Action: service.ActionStatus, Name: c.Name})
}
