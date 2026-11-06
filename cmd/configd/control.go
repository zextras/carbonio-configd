// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

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

func (a *app) controlCmd() *cobra.Command {
	c := &a.cli.Control

	verb := func(use, short string, run func(sys.System, *stdio) error) *cobra.Command {
		return a.leaf(use, short, posArgs(nil, nil, false), nil, func() error { return run(a.env, a.std) })
	}

	return group("control <command>", "Orchestrate all services",
		verb("start", "Start all enabled services", c.Start.Run),
		verb("startup", "Start all enabled services", c.Startup.Run),
		verb("stop", "Stop all services", c.Stop.Run),
		verb("shutdown", "Stop all services", c.Shutdown.Run),
		verb("restart", "Restart all services", c.Restart.Run),
		verb("status", "Show status of enabled services", c.Status.Run),
	)
}

// runControl executes one "configd control" action.
func runControl(env sys.System, std *stdio, action control.Action) error {
	opts := &control.Options{Action: action}

	return exitWith(control.Run(context.Background(), env, std.Out, std.Err, opts))
}

type ControlStartCmd struct{}

func (c *ControlStartCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStart)
}

type ControlStopCmd struct{}

func (c *ControlStopCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStop)
}

type ControlRestartCmd struct{}

func (c *ControlRestartCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionRestart)
}

type ControlStatusCmd struct{}

func (c *ControlStatusCmd) Run(env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStatus)
}
