// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/release"
)

// ReleaseCmd handles the "configd release" subcommand.
type ReleaseCmd struct {
	Packages bool `name:"packages" short:"V" help:"Show installed package versions"`
}

// Run executes the release command.
func (c *ReleaseCmd) Run() error {
	return exitWith(release.Run(context.Background(), os.Stdout, os.Stderr, &release.Options{Packages: c.Packages}))
}
