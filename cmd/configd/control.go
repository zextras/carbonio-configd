// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/cli/control"
	"github.com/zextras/carbonio-configd/internal/sys"
)

type ControlCmd struct {
	Start    ControlStartCmd   `cmd:"" help:"Start all enabled services"`
	Startup  ControlStartCmd   `cmd:"" help:"Start all enabled services"`
	Stop     ControlStopCmd    `cmd:"" help:"Stop all services"`
	Shutdown ControlStopCmd    `cmd:"" help:"Stop all services"`
	Restart  ControlRestartCmd `cmd:"" help:"Restart all services"`
	Status   ControlStatusCmd  `cmd:"" help:"Show status of enabled services"`
}

// runControl executes one "configd control" action.
func runControl(env sys.System, std *stdio, action control.Action) error {
	opts := &control.Options{Action: action}

	return exitWith(control.Run(context.Background(), env, std.Out, std.Err, opts))
}

type ControlStartCmd struct{}

func (c *ControlStartCmd) Run(_ *ControlCmd, env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStart)
}

type ControlStopCmd struct{}

func (c *ControlStopCmd) Run(_ *ControlCmd, env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStop)
}

type ControlRestartCmd struct{}

func (c *ControlRestartCmd) Run(_ *ControlCmd, env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionRestart)
}

type ControlStatusCmd struct{}

func (c *ControlStatusCmd) Run(_ *ControlCmd, env sys.System, std *stdio) error {
	return runControl(env, std, control.ActionStatus)
}
