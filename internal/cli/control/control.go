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
	"github.com/zextras/carbonio-configd/internal/services"
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

// Overridable in tests: the real implementation probes live services.
var controlStatusFn = controlStatus

// Run executes "configd control" and returns the exit status. Every action
// requires the zextras user.
func Run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	if !cliutil.RequireZextras(stderr) {
		return 1
	}

	return run(ctx, stdout, stderr, opts)
}

func run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	c := cliutil.NewConsole(stdout, stderr)

	if opts.Action == ActionStatus && opts.Host == "" {
		// Legacy parity: control.pl:doStatus returns 1 when a service is
		// stopped. Return it directly so stderr stays free of an "Error:"
		// line legacy never printed.
		return controlStatusFn(ctx, c)
	}

	if err := dispatch(ctx, c, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}

func dispatch(ctx context.Context, c *cliutil.Console, opts *Options) error {
	switch opts.Action {
	case ActionStart:
		return start(ctx, c, opts.Host)
	case ActionStop:
		return stop(ctx, c, opts.Host)
	case ActionRestart:
		return restart(ctx, c, opts.Host)
	case ActionStatus:
		_, err := services.RemoteHostStatus(ctx, opts.Host, "all")

		return err
	default:
		return fmt.Errorf("unknown control action: %q", opts.Action)
	}
}

func start(ctx context.Context, c *cliutil.Console, host string) error {
	if host != "" {
		return services.RemoteHostStart(ctx, host, "all")
	}

	if controlStart(ctx, c) != 0 {
		return errors.New("some services failed to start")
	}

	return nil
}

func stop(ctx context.Context, c *cliutil.Console, host string) error {
	if host != "" {
		return services.RemoteHostStop(ctx, host, "all")
	}

	if controlStop(ctx, c) != 0 {
		return errors.New("some services failed to stop")
	}

	return nil
}

func restart(ctx context.Context, c *cliutil.Console, host string) error {
	if host != "" {
		if err := services.RemoteHostStop(ctx, host, "all"); err != nil {
			return err
		}

		return services.RemoteHostStart(ctx, host, "all")
	}

	// Legacy parity: control.pl doRestart runs doShutdown then doStartup
	// unconditionally; the stop result is deliberately not consulted.
	controlStop(ctx, c)

	return start(ctx, c, "")
}
