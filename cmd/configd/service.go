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
	List         ServiceListCmd         `cmd:"" help:"List all services with status"`
	Start        ServiceStartCmd        `cmd:"" help:"Start a service"`
	Stop         ServiceStopCmd         `cmd:"" help:"Stop a service"`
	Restart      ServiceRestartCmd      `cmd:"" help:"Restart a service"`
	Reload       ServiceReloadCmd       `cmd:"" help:"Reload a service"`
	Status       ServiceStatusCmd       `cmd:"" help:"Show service status"`
	StartSystemd ServiceStartSystemdCmd `cmd:"" name:"start-systemd" hidden:"" help:"systemd ExecStart leaf"`
	StopSystemd  ServiceStopSystemdCmd  `cmd:"" name:"stop-systemd" hidden:"" help:"systemd ExecStop leaf (no systemctl)"`
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

// ServiceStartSystemdCmd is the systemd ExecStart leaf: it launches the service
// in-process and never calls systemctl, so it is safe to invoke from the
// carbonio-<name>.service unit without re-entering systemctl.
type ServiceStartSystemdCmd struct {
	Name  string   `arg:"" help:"Service name"`
	Extra []string `arg:"" optional:"" hidden:""`
}

// Run executes the systemd-leaf start command.
func (c *ServiceStartSystemdCmd) Run() error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if err := services.ServiceStartSystemd(ctx, c.Name); err != nil {
		return fmt.Errorf("failed to start service %s: %w", c.Name, err)
	}

	return nil
}

// ServiceStopSystemdCmd is the systemd ExecStop leaf: it stops the service
// in-process and never calls systemctl.
type ServiceStopSystemdCmd struct {
	Name  string   `arg:"" help:"Service name"`
	Extra []string `arg:"" optional:"" hidden:""`
}

// Run executes the systemd-leaf stop command.
func (c *ServiceStopSystemdCmd) Run() error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if err := services.ServiceStopSystemd(ctx, c.Name); err != nil {
		return fmt.Errorf("failed to stop service %s: %w", c.Name, err)
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

	return printServiceStatus(context.Background(), c.Name)
}

// printServiceStatus reports one service's state plus PID/uptime detail.
// Shared by `configd service status <name>` and `configd status <name>`.
func printServiceStatus(ctx context.Context, name string) error {
	running, err := services.ServiceStatus(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get status for service %s: %w", name, err)
	}

	def := services.LookupService(name)
	if def == nil {
		return fmt.Errorf("unknown service: %s", name)
	}

	if !running {
		fmt.Printf("%s is not running.\n", def.DisplayName)

		return fmt.Errorf("service %s is not running", name)
	}

	fmt.Printf("%s is running.\n", def.DisplayName)
	showServiceDetail(ctx, def)

	return nil
}

// showServiceDetail prints PID/uptime, sourced to match the orchestration
// layer: systemctl show in strict systemd mode, /proc in legacy mode. Asking
// systemctl in legacy mode reports the unit's state, not the process configd
// actually spawned — on hosts with no unit at all (ubuntu-jammy, rocky-8)
// that yielded "Since: n/a" for a healthy daemon.
func showServiceDetail(ctx context.Context, def *services.ServiceDef) {
	if services.IsSystemdMode() {
		for _, unit := range def.SystemdUnits {
			showUnitDetail(ctx, unit)
		}

		return
	}

	pid := services.RunningPID(def)
	if pid == 0 {
		return
	}

	fmt.Printf("  PID: %d\n", pid)

	if since, ok := procStartTime(pid); ok {
		fmt.Printf("  Since: %s\n", since)
	}
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

	// systemd renders an unset timestamp as "n/a" — never print that as a
	// start time.
	if ts, ok := props["ActiveEnterTimestamp"]; ok && ts != "" && ts != "n/a" {
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
