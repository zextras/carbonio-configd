// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// StatusCmd handles the "configd status [name]" subcommand.
type StatusCmd struct {
	Name string
}

func (a *app) statusCmd() *cobra.Command {
	c := &a.cli.Status

	cmd := a.leaf("status [<name>]", "Show service status", posArgs(nil, []string{"[<name>]"}, false),
		func(_ *cobra.Command, args []string) error {
			c.Name = ""
			if len(args) > 0 {
				c.Name = args[0]
			}

			return nil
		},
		func() error { return c.Run(a.env, a.std) })
	cmd.ValidArgsFunction = a.completeFirstService

	return describeArgs(cmd, [2]string{"[<name>]", "Service name for detailed status"})
}

// Run executes the status subcommand.
func (c *StatusCmd) Run(env sys.System, std *stdio) error {
	return exitWith(status.Run(context.Background(), env, std.Out, std.Err, &status.Options{Name: c.Name}))
}
