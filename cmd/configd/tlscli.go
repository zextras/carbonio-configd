// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/tls"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// TLSCmd handles "configd tls" — the Go port of legacy zmtlsctl.
type TLSCmd struct {
	// Target zimbraMailMode (both|http|https|mixed|redirect).
	// If omitted, only the config rewrite is performed.
	Mode  string
	Force bool
	Host  string
}

func (a *app) tlsCmd() *cobra.Command {
	c := &a.cli.TLS

	cmd := a.leaf("tls [<mode>]", "Manage Carbonio mail mode", posArgs(nil, []string{"[<mode>]"}, false),
		func(_ *cobra.Command, args []string) error {
			c.Mode = ""
			if len(args) > 0 {
				c.Mode = args[0]
			}

			return nil
		},
		func() error { return c.Run(a.cli, a.env, a.std) })

	fs := cmd.Flags()
	fs.BoolVar(&c.Force, "force", false, "Skip proxy cross-constraint validation")
	fs.StringVar(&c.Host, "host", "", "Override detected hostname (defaults to zimbra_server_hostname)")

	cmd.ValidArgsFunction = staticFirstArg(mailModeNames()...)
	noFileCompletion(cmd, "host")

	return describeArgs(cmd, [2]string{"[<mode>]", "Target zimbraMailMode"})
}

// Run executes the tls command.
func (c *TLSCmd) Run(cli *CLI, env sys.System, std *stdio) error {
	return exitWith(tls.Run(context.Background(), std.Out, std.Err, &tls.Options{
		Mode:       c.Mode,
		Force:      c.Force,
		Host:       c.Host,
		Standalone: standaloneRewrite(env, cli.DisableRestarts),
	}))
}
