// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
)

const ldapServiceName = "ldap"

// ControlCmd is the top-level control command.
type ControlCmd struct {
	Start    ControlStartCmd   `cmd:"" help:"Start all enabled services"`
	Startup  ControlStartCmd   `cmd:"" help:"Start all enabled services"`
	Stop     ControlStopCmd    `cmd:"" help:"Stop all services"`
	Shutdown ControlStopCmd    `cmd:"" help:"Stop all services"`
	Restart  ControlRestartCmd `cmd:"" help:"Restart all services"`
	Status   ControlStatusCmd  `cmd:"" help:"Show status of enabled services"`
	Host     string            `name:"host" short:"H" help:"Execute command on remote host via SSH"`
}

// ControlStartCmd starts all enabled services.
type ControlStartCmd struct{}

// Run executes control start.
func (c *ControlStartCmd) Run(parent *ControlCmd) error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if parent.Host != "" {
		return services.RemoteHostStart(ctx, parent.Host, "all")
	}

	if controlStart(ctx) != 0 {
		return fmt.Errorf("some services failed to start")
	}

	return nil
}

// ControlStopCmd stops all services.
type ControlStopCmd struct{}

// Run executes control stop.
func (c *ControlStopCmd) Run(parent *ControlCmd) error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if parent.Host != "" {
		return services.RemoteHostStop(ctx, parent.Host, "all")
	}

	if controlStop(ctx) != 0 {
		return fmt.Errorf("some services failed to stop")
	}

	return nil
}

// ControlRestartCmd restarts all services.
type ControlRestartCmd struct{}

// Run executes control restart.
func (c *ControlRestartCmd) Run(parent *ControlCmd) error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if parent.Host != "" {
		if err := services.RemoteHostStop(ctx, parent.Host, "all"); err != nil {
			return err
		}

		return services.RemoteHostStart(ctx, parent.Host, "all")
	}

	controlStop(ctx)

	if controlStart(ctx) != 0 {
		return fmt.Errorf("some services failed to start")
	}

	return nil
}

// ControlStatusCmd shows status of all enabled services.
type ControlStatusCmd struct{}

// Run executes control status.
func (c *ControlStatusCmd) Run(parent *ControlCmd) error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if parent.Host != "" {
		requireZextras()
		// For remote status, query each service and display results
		// This is a simplified version - full implementation would enumerate services
		_, err := services.RemoteHostStatus(ctx, parent.Host, "all")

		return err
	}

	controlStatus(ctx)

	return nil
}

// VersionCmd shows Carbonio release and package versions.
type VersionCmd struct {
	Packages bool `name:"packages" short:"V" help:"Show installed package versions"`
}

// Run executes the version-info command.
func (c *VersionCmd) Run() error {
	initCLILogging()

	version, err := os.ReadFile("/opt/zextras/.version")
	if err != nil {
		fmt.Println("Carbonio Release unknown")
	} else {
		fmt.Printf("Carbonio Release %s\n", strings.TrimSpace(string(version)))
	}

	if !c.Packages {
		return err
	}

	ctx := context.Background()
	distroID := getDistroID()

	var out []byte

	switch distroID {
	case "ubuntu", "debian":
		out, _ = exec.CommandContext(ctx, "dpkg-query", "-W", "-f", "${Package} ${Version}\n", "carbonio*").Output()
	default:
		out, _ = exec.CommandContext(ctx, "rpm", "-qa", "--qf", "%{NAME} %{VERSION}-%{RELEASE}\n", "carbonio*").Output()
	}

	if len(out) > 0 {
		fmt.Println("\nInstalled packages:")
		fmt.Print(string(out))
	}

	return nil
}

// controlStart implements the legacy `zmcontrol start` orchestration:
//  1. If LDAP is local and stopped, start it FIRST (single-server hosts always;
//     multi-server LDAP-replica hosts; multi-server non-LDAP nodes skip this).
//  2. Start configd next so REWRITE-protocol calls in subsequent service
//     starts have a daemon to talk to. Without this every dependent service
//     would fail to regenerate its config.
//  3. Then iterate the rest of the registered services in legacy %startorder
//     (mailbox, memcached, proxy, amavis, ..., mta, stats), filtering by what
//     LDAP says is enabled on this host.
func controlStart(ctx context.Context) int {
	cliHeader()

	if err := startBootstrapServices(ctx); err != nil {
		return 1
	}

	enabledList, err := services.DiscoverEnabledServices(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine services: %v\n", err)

		return 1
	}

	threshold := services.GetDiskThreshold()

	avail, ok, _ := services.CheckDiskSpace("/opt/zextras", threshold)
	if !ok {
		cliWarn("Disk space below threshold for /opt/zextras (%dMB available, %dMB required)", avail, threshold)
	}

	enabledSet := make(map[string]bool, len(enabledList))
	for _, s := range enabledList {
		enabledSet[services.MapLDAPServiceToRegistry(s)] = true
	}

	return startEnabledServices(ctx, enabledSet, threshold)
}

// startBootstrapServices starts LDAP (if local) and configd before all other services.
func startBootstrapServices(ctx context.Context) error {
	if services.IsLDAPLocal() {
		running, _ := services.ServiceStatus(ctx, ldapServiceName)
		if !running {
			def := services.LookupService(ldapServiceName)
			done := cliProgress("Starting", def.DisplayName)

			err := services.ServiceStart(ctx, ldapServiceName)
			done(err)

			if err != nil {
				return err
			}
		}
	}

	if running, _ := services.ServiceStatus(ctx, "configd"); !running {
		def := services.LookupService("configd")
		done := cliProgress("Starting", def.DisplayName)

		err := services.ServiceStart(ctx, "configd")
		done(err)

		if err != nil {
			return err
		}
	}

	return nil
}

// startEnabledServices starts all LDAP-enabled (and custom-enabled) services in order.
func startEnabledServices(ctx context.Context, enabledSet map[string]bool, threshold int) int {
	rc := 0

	for _, name := range services.AllServiceNames() {
		if name == ldapServiceName || name == "configd" {
			continue
		}

		if !enabledSet[name] && !services.IsCustomEnabled(ctx, name) {
			continue
		}

		def := services.LookupService(name)
		if def == nil {
			continue
		}

		services.CheckServiceDiskSpace(name, threshold)

		done := cliProgress("Starting", def.DisplayName)
		startErr := services.ServiceStart(ctx, name)
		done(startErr)

		if startErr != nil {
			rc = 1
		}
	}

	return rc
}

func controlStop(ctx context.Context) int {
	cliHeader()

	rc := 0
	ordered := services.AllServiceNames()

	// Reverse order for stop
	for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
		ordered[i], ordered[j] = ordered[j], ordered[i]
	}

	for _, name := range ordered {
		if name == "configd" || name == ldapServiceName {
			continue
		}

		def := services.LookupService(name)
		if def == nil {
			continue
		}

		running, _ := services.ServiceStatus(ctx, name)
		if !running {
			continue
		}

		done := cliProgress("Stopping", def.DisplayName)
		stopErr := services.ServiceStop(ctx, name)
		done(stopErr)

		if stopErr != nil {
			rc = 1
		}
	}

	// Stop LDAP last if local
	rc |= stopLDAPIfLocal(ctx, nil)

	// Stop the configd daemon itself — it must be last so it can handle
	// rewriteConfigs calls from other services during their own shutdown.
	if running, _ := services.ServiceStatus(ctx, "configd"); running {
		def := services.LookupService("configd")
		done := cliProgress("Stopping", def.DisplayName)
		stopErr := services.ServiceStop(ctx, "configd")
		done(stopErr)

		if stopErr != nil {
			rc = 1
		}
	}

	return rc
}

// stopLDAPIfLocal stops LDAP if it's local and was enabled (or LDAP discovery failed).
func stopLDAPIfLocal(ctx context.Context, enabledSet map[string]bool) int {
	if enabledSet != nil && !enabledSet[ldapServiceName] {
		return 0
	}

	if !services.IsLDAPLocal() {
		return 0
	}

	running, _ := services.ServiceStatus(ctx, ldapServiceName)
	if !running {
		return 0
	}

	def := services.LookupService(ldapServiceName)
	done := cliProgress("Stopping", def.DisplayName)
	stopErr := services.ServiceStop(ctx, ldapServiceName)
	done(stopErr)

	if stopErr != nil {
		return 1
	}

	return 0
}

// controlStatus prints the status of every registered service, matching the
// behavior of legacy `zmcontrol status` which always displayed the full list
// (rather than filtering to LDAP-enabled services). The "running" judgment for
// a row is only counted against the exit code if the service is actually
// LDAP-enabled — so an unconfigured host doesn't get rc=1 just because clamd
// is not started.
func controlStatus(ctx context.Context) int {
	cliHeader()

	// Optional LDAP discovery — used only to decide which services SHOULD be
	// running for the exit-code judgment, not to filter the displayed list.
	enabledList, err := services.DiscoverEnabledServices(ctx)

	var enabledSet map[string]bool
	if err == nil {
		enabledSet = make(map[string]bool, len(enabledList))
		for _, s := range enabledList {
			mapped := services.MapLDAPServiceToRegistry(s)
			enabledSet[mapped] = true
		}
	}

	allRunning := true

	for _, info := range services.ServiceListStatus(ctx) {
		detail := getServiceDetail(ctx, info.Name, info.Running)
		cliStatus(info.DisplayName, info.Running, detail)

		// Only fail the exit code if an enabled service is down. If LDAP
		// discovery failed (enabledSet == nil), fall back to legacy "any
		// stopped service is a failure" behavior.
		if !info.Running {
			if enabledSet == nil || enabledSet[info.Name] {
				allRunning = false
			}
		}
	}

	checkAdvancedStatus(ctx)

	if !allRunning {
		return 1
	}

	return 0
}

// getServiceDetail returns a short detail string for running services (pid, uptime).
func getServiceDetail(ctx context.Context, name string, running bool) string {
	if !running {
		return ""
	}

	def := services.LookupService(name)
	if def == nil || len(def.SystemdUnits) == 0 {
		return ""
	}

	// #nosec G204 — unit from internal registry
	out, err := exec.CommandContext(ctx, "systemctl", "show", def.SystemdUnits[0],
		"--property=MainPID,ActiveEnterTimestamp").Output()
	if err != nil {
		return ""
	}

	props := make(map[string]string)

	for line := range strings.SplitSeq(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}

	var parts []string

	if pid := props["MainPID"]; pid != "" && pid != "0" {
		parts = append(parts, "pid "+pid)
	}

	if ts := props["ActiveEnterTimestamp"]; ts != "" {
		parts = append(parts, "since "+ts)
	}

	if len(parts) == 0 {
		return ""
	}

	return "(" + strings.Join(parts, ", ") + ")"
}

// checkAdvancedStatus checks if Carbonio Advanced modules are installed and running.
func checkAdvancedStatus(ctx context.Context) {
	matches, _ := os.ReadDir("/opt/zextras/lib/ext/carbonio")
	hasAdvanced := false

	for _, m := range matches {
		if strings.HasPrefix(m.Name(), "carbonio-advanced-") && strings.HasSuffix(m.Name(), ".jar") {
			hasAdvanced = true

			break
		}
	}

	if !hasAdvanced {
		return
	}

	carbonioCLI := "/opt/zextras/bin/carbonio"
	if _, err := os.Stat(carbonioCLI); err != nil {
		return
	}

	fmt.Printf("\n\t%sCarbonio Advanced installed.%s\n", colorCyan, colorReset)

	// #nosec G204 - fixed binary path
	out, err := exec.CommandContext(ctx, carbonioCLI, "--json", "core", "getAllServicesStatus").Output()
	if err != nil {
		logger.DebugContext(ctx, "Advanced status check failed", "error", err)
		fmt.Printf("\t  %sAdvanced modules status unavailable.%s\n", colorDim, colorReset)

		return
	}

	parseAdvancedStatus(string(out))
}

func parseAdvancedStatus(jsonOutput string) {
	for line := range strings.SplitSeq(jsonOutput, "},") {
		line = strings.TrimLeft(line, "[{ \n\r\t")

		nameIdx := strings.Index(line, `"commercialName":"`)
		if nameIdx < 0 {
			continue
		}

		nameStart := nameIdx + len(`"commercialName":"`)
		nameEnd := strings.Index(line[nameStart:], `"`)

		if nameEnd < 0 {
			continue
		}

		name := line[nameStart : nameStart+nameEnd]
		running := strings.Contains(line, `"running":true`)

		if running {
			fmt.Printf("\t  %-20s %s%s%s\n", name, colorGreen, "running", colorReset)
		} else {
			fmt.Printf("\t  %-20s %s%s%s\n", name, colorRed, "NOT running", colorReset)
		}
	}
}

func getDistroID() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if val, ok := strings.CutPrefix(line, "ID="); ok {
			return strings.Trim(val, "\"")
		}
	}

	return ""
}
