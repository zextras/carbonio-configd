// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package service implements "configd service <action> [name]": listing,
// controlling and probing a single registered service.
package service

import (
	"context"
	"fmt"
	"io"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/services"
)

// Action is the "configd service" subcommand to run.
type Action string

// The service subcommands. StartSystemd and StopSystemd are the hidden
// systemd ExecStart/ExecStop leaves: they act in-process and never call
// systemctl.
const (
	ActionList         Action = "list"
	ActionStart        Action = "start"
	ActionStop         Action = "stop"
	ActionRestart      Action = "restart"
	ActionReload       Action = "reload"
	ActionStatus       Action = "status"
	ActionStartSystemd Action = "start-systemd"
	ActionStopSystemd  Action = "stop-systemd"
)

// Options are the parsed arguments of "configd service".
type Options struct {
	Action Action
	// Name is the service to act on; unused by ActionList.
	Name string
	// NoRewrite skips config regeneration on start and restart.
	NoRewrite bool
}

// mutation describes a service-mutating action: the verb used in the error
// message and the services function implementing it.
type mutation struct {
	verb string
	fn   func(context.Context, string) error
}

var mutations = map[Action]mutation{
	ActionStart:        {"start", services.ServiceStart},
	ActionStop:         {"stop", services.ServiceStop},
	ActionRestart:      {"restart", services.ServiceRestart},
	ActionReload:       {"reload", services.ServiceReload},
	ActionStartSystemd: {"start", services.ServiceStartSystemd},
	ActionStopSystemd:  {"stop", services.ServiceStopSystemd},
}

// Run executes "configd service" and returns the exit status. list and status
// have no permission gate; every mutating action requires the zextras user.
func Run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	if _, mutating := mutations[opts.Action]; mutating && !cliutil.RequireZextras(stderr) {
		return 1
	}

	return run(ctx, stdout, stderr, opts)
}

func run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	c := cliutil.NewConsole(stdout, stderr)

	switch opts.Action {
	case ActionList:
		list(ctx, c)

		return 0
	case ActionStatus:
		return report(stderr, status.PrintServiceStatus(ctx, c, opts.Name))
	default:
		return report(stderr, mutate(ctx, opts))
	}
}

// report prints err as the "Error:" line main used to print for a failing Kong
// Run and returns the matching exit status.
func report(stderr io.Writer, err error) int {
	if err == nil {
		return 0
	}

	_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

	return 1
}

// mutate runs a service-mutating action, wrapping any error with the verb and
// service name to match each command's original user-facing message.
func mutate(ctx context.Context, opts *Options) error {
	m, ok := mutations[opts.Action]
	if !ok {
		return fmt.Errorf("unknown service action: %q", opts.Action)
	}

	if opts.Action == ActionStart || opts.Action == ActionRestart {
		// services.ServiceRestart checks this before rewriting configs.
		services.NoRewrite = opts.NoRewrite
	}

	if err := m.fn(ctx, opts.Name); err != nil {
		return fmt.Errorf("failed to %s service %s: %w", m.verb, opts.Name, err)
	}

	return nil
}

func list(ctx context.Context, c *cliutil.Console) {
	for _, info := range services.ServiceListStatus(ctx) {
		c.Status(info.DisplayName, info.Running, "")
	}
}
