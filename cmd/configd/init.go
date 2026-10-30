// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/initcmd"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// InitCmd handles the "configd init <component>" subcommand.
type InitCmd struct {
	Component string `arg:"" help:"Component to initialize (mta, proxy)"`
	Force     bool   `short:"f" name:"force" help:"Allow reinitialization even if configs exist"`
}

// Run executes the init subcommand.
func (c *InitCmd) Run(env sys.System) error {
	return exitWith(initcmd.Run(context.Background(), env, os.Stdout, os.Stderr, &initcmd.Options{
		Component: c.Component,
		Force:     c.Force,
	}))
}
