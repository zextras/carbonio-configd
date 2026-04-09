// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/zextras/carbonio-configd/internal/services"
)

const norewriteArg = "norewrite"

// ServiceCmd handles the "configd service" subcommand.
type ServiceCmd struct {
	List    ServiceListCmd    `cmd:"" help:"List all services with status"`
	Start   ServiceStartCmd   `cmd:"" help:"Start a service"`
	Stop    ServiceStopCmd    `cmd:"" help:"Stop a service"`
	Restart ServiceRestartCmd `cmd:"" help:"Restart a service"`
	Reload  ServiceReloadCmd  `cmd:"" help:"Reload a service"`
	Status  ServiceStatusCmd  `cmd:"" help:"Show service status"`
}

// ServiceListCmd lists all services with their status.
type ServiceListCmd struct{}

// Run executes the service list command.
//
//nolint:unparam // Kong interface requires error return
func (c *ServiceListCmd) Run() error {
	initCLILogging()

	ctx := context.Background()
	runServiceList(ctx)

	return nil
}

// ServiceStartCmd starts a service.
type ServiceStartCmd struct {
	Name      string   `arg:"" help:"Service name"`
	NoRewrite bool     `name:"no-rewrite" help:"Skip config regeneration"`
	Extra     []string `arg:"" optional:"" hidden:""`
}

// Run executes the service start command.
func (c *ServiceStartCmd) Run() error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	// Supports both --no-rewrite flag and legacy "norewrite" positional arg
	for _, a := range c.Extra {
		if a == norewriteArg {
			c.NoRewrite = true
		}
	}

	services.NoRewrite = c.NoRewrite

	if err := services.ServiceStart(ctx, c.Name); err != nil {
		return fmt.Errorf("failed to start service %s: %w", c.Name, err)
	}

	return nil
}

// ServiceStopCmd stops a service.
type ServiceStopCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the service stop command.
func (c *ServiceStopCmd) Run() error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if err := services.ServiceStop(ctx, c.Name); err != nil {
		return fmt.Errorf("failed to stop service %s: %w", c.Name, err)
	}

	return nil
}

// ServiceRestartCmd restarts a service.
type ServiceRestartCmd struct {
	Name      string   `arg:"" help:"Service name"`
	NoRewrite bool     `name:"no-rewrite" help:"Skip config regeneration"`
	Extra     []string `arg:"" optional:"" hidden:""`
}

// Run executes the service restart command.
func (c *ServiceRestartCmd) Run() error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	// Parse extra args for --no-rewrite / -R flag, then propagate to the services package.
	// services.NoRewrite is checked by ServiceRestart before rewriting configs.
	for _, a := range c.Extra {
		if a == norewriteArg {
			c.NoRewrite = true
		}
	}

	services.NoRewrite = c.NoRewrite

	if err := services.ServiceRestart(ctx, c.Name); err != nil {
		return fmt.Errorf("failed to restart service %s: %w", c.Name, err)
	}

	return nil
}

// ServiceReloadCmd reloads a service.
type ServiceReloadCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the service reload command.
func (c *ServiceReloadCmd) Run() error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if err := services.ServiceReload(ctx, c.Name); err != nil {
		return fmt.Errorf("failed to reload service %s: %w", c.Name, err)
	}

	return nil
}

// ServiceStatusCmd shows detailed status for a service.
type ServiceStatusCmd struct {
	Name string `arg:"" help:"Service name"`
}

// Run executes the service status command.
func (c *ServiceStatusCmd) Run() error {
	initCLILogging()

	ctx := context.Background()

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

func showUnitDetail(ctx context.Context, unit string) {
	// Get MainPID and ActiveEnterTimestamp from systemctl show
	// #nosec G702 - unit name comes from internal registry, not user input
	out, err := exec.CommandContext(ctx, "systemctl", "show", unit,
		"--property=MainPID,ActiveEnterTimestamp,MemoryCurrent").Output()
	if err != nil {
		return
	}

	props := parseSystemctlShow(string(out))

	if pid, ok := props["MainPID"]; ok && pid != "" && pid != "0" {
		fmt.Printf("  PID: %s\n", pid)
	}

	if ts, ok := props["ActiveEnterTimestamp"]; ok && ts != "" {
		fmt.Printf("  Since: %s\n", ts)
	}

	if mem, ok := props["MemoryCurrent"]; ok && mem != "" && mem != "[not set]" {
		fmt.Printf("  Memory: %s\n", mem)
	}
}

func parseSystemctlShow(output string) map[string]string {
	props := make(map[string]string)

	for line := range strings.SplitSeq(output, "\n") {
		if idx := strings.IndexByte(line, '='); idx > 0 {
			props[line[:idx]] = line[idx+1:]
		}
	}

	return props
}

func runServiceList(ctx context.Context) {
	infos := services.ServiceListStatus(ctx)
	for _, info := range infos {
		cliStatus(info.DisplayName, info.Running, "")
	}
}
