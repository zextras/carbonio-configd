// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/control"
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
func runControl(action control.Action, host string) error {
	return exitWith(control.Run(context.Background(), os.Stdout, os.Stderr, &control.Options{Action: action, Host: host}))
}

type ControlStartCmd struct{}

func (c *ControlStartCmd) Run(parent *ControlCmd) error {
	return runControl(control.ActionStart, parent.Host)
}

type ControlStopCmd struct{}

func (c *ControlStopCmd) Run(parent *ControlCmd) error {
	return runControl(control.ActionStop, parent.Host)
}

type ControlRestartCmd struct{}

func (c *ControlRestartCmd) Run(parent *ControlCmd) error {
	return runControl(control.ActionRestart, parent.Host)
}

type ControlStatusCmd struct{}

func (c *ControlStatusCmd) Run(parent *ControlCmd) error {
	return runControl(control.ActionStatus, parent.Host)
}
