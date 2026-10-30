// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/tls"
)

// TLSCmd handles "configd tls" — the Go port of legacy zmtlsctl.
type TLSCmd struct {
	// Target zimbraMailMode (both|http|https|mixed|redirect).
	// If omitted, only the config rewrite is performed.
	Mode  string `arg:"" optional:"" help:"Target zimbraMailMode"`
	Force bool   `name:"force" help:"Skip proxy cross-constraint validation"`
	Host  string `name:"host" help:"Override detected hostname (defaults to zimbra_server_hostname)"`
}

// Run executes the tls command.
func (c *TLSCmd) Run(cli *CLI) error {
	return exitWith(tls.Run(context.Background(), os.Stdout, os.Stderr, &tls.Options{
		Mode:       c.Mode,
		Force:      c.Force,
		Host:       c.Host,
		Standalone: standaloneRewrite(cli.DisableRestarts),
	}))
}
