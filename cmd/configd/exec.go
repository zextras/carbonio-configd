// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/execcmd"
)

// ExecCmd handles the "configd exec" subcommand.
type ExecCmd struct {
	Args []string
}

// argsPlaceholder is the usage placeholder of trailing program arguments.
const argsPlaceholder = "[<args> ...]"

func (a *app) execCmd() *cobra.Command {
	c := &a.cli.Exec

	cmd := a.leaf("exec [--] <program> [<args> ...]",
		"Run a program with the launch variables derived from localconfig",
		posArgs([]string{"<program>"}, []string{argsPlaceholder}, true),
		func(_ *cobra.Command, args []string) error {
			c.Args = args

			return nil
		},
		func() error { return c.Run(a.std) })
	// Everything from the program name on belongs to the program.
	cmd.Flags().SetInterspersed(false)

	return describeArgs(cmd,
		[2]string{"<program>", "Program to exec (looked up in PATH unless it has a slash)"},
		[2]string{argsPlaceholder, `Arguments; "${name}" and "$name" expand over the launch variables, "$$" is "$"`})
}

// Run loads localconfig and execs the program; it only returns on failure.
func (c *ExecCmd) Run(std *stdio) error {
	return exitWith(execcmd.Run(&execcmd.Options{Args: c.Args, Stderr: std.Err}))
}
