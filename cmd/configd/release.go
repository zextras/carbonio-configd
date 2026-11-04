// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/cli/release"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// ReleaseCmd handles the "configd release" subcommand.
type ReleaseCmd struct {
	Packages bool `name:"packages" short:"V" help:"Show installed package versions"`
}

// Run executes the release command.
func (c *ReleaseCmd) Run(env sys.System, std *stdio) error {
	return exitWith(release.Run(context.Background(), env, std.Out, std.Err, &release.Options{Packages: c.Packages}))
}
