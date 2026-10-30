// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package status implements "configd status [name]" and the service-status
// rendering shared with "configd service status" and "configd control status":
// per-service PID/uptime detail and the Carbonio Advanced module section.
package status

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Options are the parsed arguments of "configd status".
type Options struct {
	// Name selects the single-service detail mode; empty means the
	// system-wide summary.
	Name string
}

// Run executes "configd status [name]" and returns the exit status. There is
// no permission gate: status is readable by any user.
func Run(ctx context.Context, env sys.System, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	c := cliutil.NewConsole(stdout, stderr)

	ctl := services.NewController(env)

	if err := run(ctx, ctl, NewReporter(env, ctl), c, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}

func run(ctx context.Context, ctl *services.Controller, r *Reporter, c *cliutil.Console, opts *Options) error {
	// Single service detail mode — same output as `configd service status`.
	if opts.Name != "" {
		return r.PrintServiceStatus(ctx, c, opts.Name)
	}

	// System-wide summary
	c.Header()

	allRunning := true

	infos := ctl.ServiceListStatus(ctx)
	for _, info := range infos {
		c.Status(info.DisplayName, info.Running, "")

		if !info.Running {
			allRunning = false
		}
	}

	r.Advanced(ctx, c)

	if !allRunning {
		return errors.New("some services are not running")
	}

	return nil
}
