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
	"github.com/zextras/carbonio-configd/internal/sys"
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
// message and the Controller method implementing it.
type mutation struct {
	verb string
	fn   func(context.Context, string) error
}

// runner carries what one "configd service" invocation drives: the Controller,
// the status reporter and the mutating-action table bound to that Controller.
type runner struct {
	ctl       *services.Controller
	reporter  *status.Reporter
	mutations map[Action]mutation
}

// noRewrite reports whether the invocation skips the REWRITE round-trip.
// --no-rewrite is read by the Controller's start and restart paths only.
func noRewrite(opts *Options) bool {
	return opts.NoRewrite && (opts.Action == ActionStart || opts.Action == ActionRestart)
}

func newRunner(env sys.System, opts *Options) *runner {
	ctl := services.NewController(env, services.WithNoRewrite(noRewrite(opts)))

	return &runner{
		ctl:      ctl,
		reporter: status.NewReporter(env, ctl),
		mutations: map[Action]mutation{
			ActionStart:        {"start", ctl.ServiceStart},
			ActionStop:         {"stop", ctl.ServiceStop},
			ActionRestart:      {"restart", ctl.ServiceRestart},
			ActionReload:       {"reload", ctl.ServiceReload},
			ActionStartSystemd: {"start", ctl.ServiceStartSystemd},
			ActionStopSystemd:  {"stop", ctl.ServiceStopSystemd},
		},
	}
}

// Run executes "configd service" and returns the exit status. list and status
// have no permission gate; every mutating action requires the zextras user.
func Run(ctx context.Context, env sys.System, stdout, stderr io.Writer, opts *Options) int {
	r := newRunner(env, opts)

	if _, mutating := r.mutations[opts.Action]; mutating && !cliutil.RequireZextras(stderr) {
		return 1
	}

	return r.run(ctx, stdout, stderr, opts)
}

func (r *runner) run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	c := cliutil.NewConsole(stdout, stderr)

	switch opts.Action {
	case ActionList:
		r.list(ctx, c)

		return 0
	case ActionStatus:
		return report(stderr, r.reporter.PrintServiceStatus(ctx, c, opts.Name))
	default:
		return report(stderr, r.mutate(ctx, opts))
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
func (r *runner) mutate(ctx context.Context, opts *Options) error {
	m, ok := r.mutations[opts.Action]
	if !ok {
		return fmt.Errorf("unknown service action: %q", opts.Action)
	}

	if err := m.fn(ctx, opts.Name); err != nil {
		return fmt.Errorf("failed to %s service %s: %w", m.verb, opts.Name, err)
	}

	return nil
}

func (r *runner) list(ctx context.Context, c *cliutil.Console) {
	for _, info := range r.ctl.ServiceListStatus(ctx) {
		c.Status(info.DisplayName, info.Running, "")
	}
}
