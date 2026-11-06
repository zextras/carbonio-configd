// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/release"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// ReleaseCmd handles the "configd release" subcommand.
type ReleaseCmd struct {
	Packages bool
}

func (a *app) releaseCmd() *cobra.Command {
	c := &a.cli.Release

	cmd := a.leaf("release", "Show Carbonio release and package versions", posArgs(nil, nil, false), nil,
		func() error { return c.Run(a.env, a.std) })
	cmd.Flags().BoolVarP(&c.Packages, "packages", "V", false, "Show installed package versions")

	return cmd
}

// Run executes the release command.
func (c *ReleaseCmd) Run(env sys.System, std *stdio) error {
	return exitWith(release.Run(context.Background(), env, std.Out, std.Err, &release.Options{Packages: c.Packages}))
}
