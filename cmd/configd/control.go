// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/control"
	"github.com/zextras/carbonio-configd/internal/sys"
)

type ControlCmd struct {
	Start    ControlStartCmd
	Startup  ControlStartCmd
	Stop     ControlStopCmd
	Shutdown ControlStopCmd
	Restart  ControlRestartCmd
	Status   ControlStatusCmd
}

// ControlTarget is the optional "<module> [<service>]" of the start, stop and
// restart verbs: one Carbonio Advanced module, or one of its services,
// instead of every service of the host.
type ControlTarget struct {
	Module  string
	Service string
}

func (t *ControlTarget) bind(_ *cobra.Command, args []string) error {
	*t = ControlTarget{}

	if len(args) > 0 {
		t.Module = args[0]
	}

	if len(args) > 1 {
		t.Service = args[1]
	}

	return nil
}

func (a *app) controlCmd() *cobra.Command {
	c := &a.cli.Control

	verb := func(use, short string, t *ControlTarget, run func(sys.System, *stdio) error) *cobra.Command {
		cmd := a.leaf(use+" [<module> [<service>]]", short, posArgs(nil, []string{"<module>", "<service>"}, false),
			t.bind, func() error { return run(a.env, a.std) })
		cmd.ValidArgsFunction = staticFirstArg(control.Modules()...)

		return describeArgs(cmd,
			[2]string{"[<module>]", "Carbonio Advanced module to control instead of all services: " +
				strings.Join(control.Modules(), ", ") + " (the core module: restart the mailbox)"},
			[2]string{"[<service>]", "Service of the module (default: the whole module)"},
		)
	}

	status := a.leaf("status", "Show status of enabled services", posArgs(nil, nil, false), nil,
		func() error { return c.Status.Run(a.env, a.std) })

	return group("control <command>", "Orchestrate all services",
		verb("start", "Start all enabled services, or one Advanced module", &c.Start.ControlTarget, c.Start.Run),
		verb("startup", "Start all enabled services, or one Advanced module", &c.Startup.ControlTarget, c.Startup.Run),
		verb("stop", "Stop all services, or one Advanced module", &c.Stop.ControlTarget, c.Stop.Run),
		verb("shutdown", "Stop all services, or one Advanced module", &c.Shutdown.ControlTarget, c.Shutdown.Run),
		verb("restart", "Restart all services, or one Advanced module", &c.Restart.ControlTarget, c.Restart.Run),
		status,
	)
}

// runControl executes one "configd control" action.
func runControl(env sys.System, std *stdio, action control.Action, t ControlTarget) error {
	opts := &control.Options{Action: action, Module: t.Module, Service: t.Service}

	return exitWith(control.Run(context.Background(), env, std.Out, std.Err, opts))
}

type ControlStartCmd struct{ ControlTarget }

func (c *ControlStartCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStart, c.ControlTarget)
}

type ControlStopCmd struct{ ControlTarget }

func (c *ControlStopCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStop, c.ControlTarget)
}

type ControlRestartCmd struct{ ControlTarget }

func (c *ControlRestartCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionRestart, c.ControlTarget)
}

type ControlStatusCmd struct{}

func (c *ControlStatusCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStatus, ControlTarget{})
}
