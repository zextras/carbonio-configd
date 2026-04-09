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

	// Single service detail mode - delegate to service status
	if c.Name != "" {
		running, err := services.ServiceStatus(ctx, c.Name)
		if err != nil {
			return fmt.Errorf("failed to get status for service %s: %w", c.Name, err)
		}

		def := services.LookupService(c.Name)

		if !running {
			fmt.Printf("%s is not running.\n", def.DisplayName)
			return fmt.Errorf("service %s is not running", c.Name)
		}

		fmt.Printf("%s is running.\n", def.DisplayName)

		// Show systemd unit details
		for _, unit := range def.SystemdUnits {
			showUnitDetail(ctx, unit)
		}

		return nil
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
