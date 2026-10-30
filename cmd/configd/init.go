// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/initcmd"
)

// InitCmd handles the "configd init <component>" subcommand.
type InitCmd struct {
	Component string `arg:"" help:"Component to initialize (mta, proxy)"`
	Force     bool   `short:"f" name:"force" help:"Allow reinitialization even if configs exist"`
}

// Run executes the init subcommand.
func (c *InitCmd) Run() error {
	return exitWith(initcmd.Run(context.Background(), os.Stdout, os.Stderr, &initcmd.Options{
		Component: c.Component,
		Force:     c.Force,
	}))
}
