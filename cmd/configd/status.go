// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// StatusCmd handles the "configd status [name]" subcommand.
type StatusCmd struct {
	Name string `arg:"" optional:"" help:"Service name for detailed status"`
}

// Run executes the status subcommand.
func (c *StatusCmd) Run(env sys.System, std *stdio) error {
	return exitWith(status.Run(context.Background(), env, std.Out, std.Err, &status.Options{Name: c.Name}))
}
