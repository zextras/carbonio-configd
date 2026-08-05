// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"

	"github.com/zextras/carbonio-configd/internal/services"
)

// StatusCmd handles the "configd status [name]" subcommand.
type StatusCmd struct {
	Name string `arg:"" optional:"" help:"Service name for detailed status"`
}

// Run executes the status subcommand.
func (c *StatusCmd) Run() error {
	initCLILogging()

	ctx := context.Background()

	// Single service detail mode — same output as `configd service status`.
	if c.Name != "" {
		return printServiceStatus(ctx, c.Name)
	}

	// System-wide summary
	cliHeader()

	allRunning := true

	infos := services.ServiceListStatus(ctx)
	for _, info := range infos {
		cliStatus(info.DisplayName, info.Running, "")

		if !info.Running {
			allRunning = false
		}
	}

	checkAdvancedStatus(ctx)

	if !allRunning {
		return fmt.Errorf("some services are not running")
	}

	return nil
}
