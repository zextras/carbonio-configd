// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package control implements "configd control": the zmcontrol-compatible
// start/stop/restart/status of every service enabled on this host, or of a
// remote host over SSH.
package control

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Action is the "configd control" subcommand to run.
type Action string

// The control subcommands. The startup and shutdown aliases map to ActionStart
// and ActionStop.
const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionStatus  Action = "status"
)

// Options are the parsed arguments of "configd control".
type Options struct {
	Action Action
	// Host, when set, runs the action on that remote host over SSH.
	Host string
}

// orchestrator carries what one "configd control" invocation drives: the
// Controller, the status reporter and the per-service status probe.
type orchestrator struct {
	ctl      *services.Controller
	reporter *status.Reporter
	// svcStatus probes one service; defaults to ctl.ServiceStatus.
	svcStatus func(ctx context.Context, name string) (bool, error)
}

func newOrchestrator(env sys.System) *orchestrator {
	ctl := services.NewController(env)

	return &orchestrator{ctl: ctl, reporter: status.NewReporter(env, ctl), svcStatus: ctl.ServiceStatus}
}

// Run executes "configd control" and returns the exit status. Every action
// requires the zextras user.
func Run(ctx context.Context, env sys.System, stdout, stderr io.Writer, opts *Options) int {
	if !cliutil.RequireZextras(stderr) {
		return 1
	}

	return newOrchestrator(env).run(ctx, stdout, stderr, opts)
}

func (o *orchestrator) run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	c := cliutil.NewConsole(stdout, stderr)

	if opts.Action == ActionStatus && opts.Host == "" {
		// Legacy parity: control.pl:doStatus returns 1 when a service is
		// stopped. Return it directly so stderr stays free of an "Error:"
		// line legacy never printed.
		return o.controlStatus(ctx, c)
	}

	if err := o.dispatch(ctx, c, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}

func (o *orchestrator) dispatch(ctx context.Context, c *cliutil.Console, opts *Options) error {
	switch opts.Action {
	case ActionStart:
		return o.start(ctx, c, opts.Host)
	case ActionStop:
		return o.stop(ctx, c, opts.Host)
	case ActionRestart:
		return o.restart(ctx, c, opts.Host)
	case ActionStatus:
		_, err := o.ctl.RemoteHostStatus(ctx, opts.Host, "all")

		return err
	default:
		return fmt.Errorf("unknown control action: %q", opts.Action)
	}
}

func (o *orchestrator) start(ctx context.Context, c *cliutil.Console, host string) error {
	if host != "" {
		return o.ctl.RemoteHostStart(ctx, host, "all")
	}

	if o.controlStart(ctx, c) != 0 {
		return errors.New("some services failed to start")
	}

	return nil
}

func (o *orchestrator) stop(ctx context.Context, c *cliutil.Console, host string) error {
	if host != "" {
		return o.ctl.RemoteHostStop(ctx, host, "all")
	}

	if o.controlStop(ctx, c) != 0 {
		return errors.New("some services failed to stop")
	}

	return nil
}

func (o *orchestrator) restart(ctx context.Context, c *cliutil.Console, host string) error {
	if host != "" {
		if err := o.ctl.RemoteHostStop(ctx, host, "all"); err != nil {
			return err
		}

		return o.ctl.RemoteHostStart(ctx, host, "all")
	}

	// Legacy parity: control.pl doRestart runs doShutdown then doStartup
	// unconditionally; the stop result is deliberately not consulted.
	o.controlStop(ctx, c)

	return o.start(ctx, c, "")
}
