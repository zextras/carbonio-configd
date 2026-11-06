// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/initcmd"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// InitCmd handles the "configd init <component>" subcommand.
type InitCmd struct {
	Component string
	Force     bool
}

func (a *app) initCmd() *cobra.Command {
	c := &a.cli.Init

	cmd := a.leaf("init <component>", "Initialize components", posArgs([]string{"<component>"}, nil, false),
		func(_ *cobra.Command, args []string) error {
			c.Component = args[0]

			return nil
		},
		func() error { return c.Run(a.env, a.std) })
	cmd.Flags().BoolVarP(&c.Force, "force", "f", false, "Allow reinitialization even if configs exist")
	cmd.ValidArgsFunction = staticFirstArg(initComponentNames...)

	return describeArgs(cmd, [2]string{"<component>", "Component to initialize (mta, proxy)"})
}

// Run executes the init subcommand.
func (c *InitCmd) Run(env sys.System, std *stdio) error {
	return exitWith(initcmd.Run(context.Background(), env, std.Out, std.Err, &initcmd.Options{
		Component: c.Component,
		Force:     c.Force,
	}))
}
