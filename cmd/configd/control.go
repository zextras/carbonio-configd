// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

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
	Host     string            `name:"host" short:"H" help:"Execute command on remote host via SSH"`
}

// runControl executes one "configd control" action.
func runControl(env sys.System, action control.Action, host string) error {
	opts := &control.Options{Action: action, Host: host}

	return exitWith(control.Run(context.Background(), env, os.Stdout, os.Stderr, opts))
}

type ControlStartCmd struct{}

func (c *ControlStartCmd) Run(parent *ControlCmd, env sys.System) error {
	return runControl(env, control.ActionStart, parent.Host)
}

type ControlStopCmd struct{}

func (c *ControlStopCmd) Run(parent *ControlCmd, env sys.System) error {
	return runControl(env, control.ActionStop, parent.Host)
}

type ControlRestartCmd struct{}

func (c *ControlRestartCmd) Run(parent *ControlCmd, env sys.System) error {
	return runControl(env, control.ActionRestart, parent.Host)
}

type ControlStatusCmd struct{}

func (c *ControlStatusCmd) Run(parent *ControlCmd, env sys.System) error {
	return runControl(env, control.ActionStatus, parent.Host)
}
