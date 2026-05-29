// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

const ldapServiceName = "ldap"

type ControlCmd struct {
	Start    ControlStartCmd   `cmd:"" help:"Start all enabled services"`
	Startup  ControlStartCmd   `cmd:"" help:"Start all enabled services"`
	Stop     ControlStopCmd    `cmd:"" help:"Stop all services"`
	Shutdown ControlStopCmd    `cmd:"" help:"Stop all services"`
	Restart  ControlRestartCmd `cmd:"" help:"Restart all services"`
	Status   ControlStatusCmd  `cmd:"" help:"Show status of enabled services"`
	Host     string            `name:"host" short:"H" help:"Execute command on remote host via SSH"`
}

type ControlStartCmd struct{}

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

type ControlStopCmd struct{}

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

type ControlRestartCmd struct{}

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

type ControlStatusCmd struct{}

func (c *ControlStatusCmd) Run(parent *ControlCmd) error {
	requireZextras()
	initCLILogging()

	ctx := context.Background()

	if parent.Host != "" {
		_, err := services.RemoteHostStatus(ctx, parent.Host, "all")
		return err
	}

	controlStatus(ctx)

	return nil
}

type VersionCmd struct {
	Packages bool `name:"packages" short:"V" help:"Show installed package versions"`
}

func (c *VersionCmd) Run() error {
	initCLILogging()

	version, err := os.ReadFile(basePath(".version"))
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
	bd := baseDir()

	avail, ok, _ := services.CheckDiskSpace(bd, threshold)
	if !ok {
		cliWarn("Disk space below threshold for %s (%dMB available, %dMB required)", bd, avail, threshold)
	}

	enabledSet := make(map[string]bool, len(enabledList))
	for _, s := range enabledList {
		enabledSet[services.MapLDAPServiceToRegistry(s)] = true
	}

	return startEnabledServices(ctx, enabledSet, threshold)
}

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

	if running, _ := services.ServiceStatus(ctx, componentConfigd); !running {
		def := services.LookupService(componentConfigd)
		done := cliProgress("Starting", def.DisplayName)

		err := services.ServiceStart(ctx, componentConfigd)
		done(err)

		if err != nil {
			return err
		}
	}

	return nil
}

func startEnabledServices(ctx context.Context, enabledSet map[string]bool, threshold int) int {
	rc := 0

	for _, name := range services.AllServiceNames() {
		if name == ldapServiceName || name == componentConfigd {
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
		if name == componentConfigd || name == ldapServiceName {
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
	if running, _ := services.ServiceStatus(ctx, componentConfigd); running {
		def := services.LookupService(componentConfigd)
		done := cliProgress("Stopping", def.DisplayName)
		stopErr := services.ServiceStop(ctx, componentConfigd)
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

// controlStatus prints the status of services that are enabled on this host
// (mirroring legacy `control.pl:doStatus`, which iterated keys of
// getEnabledServices() — i.e. services whose name is in zimbraServiceEnabled
// for the local host, plus zmconfigd which legacy always added). This is the
// CO-3566 fix: the previous implementation iterated the entire registry and
// thus showed phantom rows like `directory server`, `mailbox`, `cbpolicyd`,
// `freshclam`, `saslauthd`, `milter`, `stats` on proxy/MTA-only nodes.
//
// When LDAP is unreachable, services.DiscoverEnabledServices already falls
// back to the on-disk cache; we propagate that opportunistically. As a last
// resort (LDAP and cache both unavailable) we degrade to the full registry
// view rather than printing nothing — operators must still see something.
//
// Exit code: rc=1 if any displayed service is Stopped/Failed, else rc=0.
func controlStatus(ctx context.Context) int { //nolint:unparam
	cliHeader()

	type discResult struct {
		set    map[string]bool
		failed bool
	}

	discCh := make(chan discResult, 1)

	go func() {
		list, err := services.DiscoverEnabledServices(ctx)
		if err != nil {
			discCh <- discResult{set: nil, failed: true}
			return
		}

		s := make(map[string]bool, len(list)+1)
		for _, n := range list {
			s[services.MapLDAPServiceToRegistry(n)] = true
		}

		// Legacy parity: zmcontrol/control.pl always inserted "zmconfigd"
		// regardless of LDAP state, so configd is always shown.
		s[componentConfigd] = true

		discCh <- discResult{set: s, failed: false}
	}()

	type svcRow struct {
		services.ServiceInfo
		detail string
	}

	var rows []svcRow

	stream := services.ServiceListStatusStream(ctx)
	disc := <-discCh

	for info := range stream {
		if disc.set != nil && !disc.set[info.Name] {
			continue
		}

		detail := getServiceDetail(ctx, info.Name, info.Running)
		cliStatus(info.DisplayName, info.Running, detail)
		rows = append(rows, svcRow{ServiceInfo: info, detail: detail})
	}

	if disc.failed {
		logger.WarnContext(ctx, "Service discovery unavailable, displayed full registry as fallback")
	}

	checkAdvancedStatus(ctx)

	for _, r := range rows {
		if !r.Running {
			return 1
		}
	}

	return 0
}

// getServiceDetail returns a short detail string for running services
// (pid, uptime). Bifurcates on IsSystemdMode() to match the orchestration
// layer: in strict systemd mode the authoritative source is systemctl show;
// in legacy mode those values are stale/empty (systemd only sees the units
// if the container bootstrap once touched them, and MainPID stays 0 because
// it never tracked the real processes), so we read PID from the same probes
// used by ServiceStatus and start time from /proc/<pid>'s mtime.
func getServiceDetail(ctx context.Context, name string, running bool) string {
	if !running {
		return ""
	}

	def := services.LookupService(name)
	if def == nil {
		return ""
	}

	if services.IsSystemdMode() {
		return serviceDetailFromSystemd(ctx, def)
	}

	return serviceDetailFromProc(def)
}

// serviceDetailFromSystemd reads MainPID and ActiveEnterTimestamp from
// systemctl show. Correct only when strict systemd mode is active — that's
// the mode in which every start/stop actually went through systemd.
func serviceDetailFromSystemd(ctx context.Context, def *services.ServiceDef) string {
	if len(def.SystemdUnits) == 0 {
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

// serviceDetailFromProc reads PID and start time from /proc directly, so
// legacy-mode status reflects the process configd actually spawned rather
// than whatever systemd last remembered before we bypassed it. The PID
// comes from services.RunningPID (same precedence as ServiceStatus) and
// the "since" timestamp is /proc/<pid>'s mtime, which on Linux is set at
// proc-entry creation (close enough to process start for display).
func serviceDetailFromProc(def *services.ServiceDef) string {
	pid := services.RunningPID(def)
	if pid == 0 {
		return ""
	}

	parts := []string{fmt.Sprintf("pid %d", pid)}

	if info, err := os.Stat("/proc/" + strconv.Itoa(pid)); err == nil {
		parts = append(parts, "since "+info.ModTime().UTC().Format("Mon 2006-01-02 15:04:05 MST"))
	}

	return "(" + strings.Join(parts, ", ") + ")"
}

func checkAdvancedStatus(ctx context.Context) {
	if !advancedJARsPresent() {
		return
	}

	fmt.Printf("\n\t%sCarbonio Advanced%s\n", colorCyan, colorReset)

	cfg, err := localconfig.LoadLocalConfig()
	if err != nil {
		logger.WarnContext(ctx, "Advanced status check: localconfig load failed", "error", err)
		fmt.Printf("\t%smodule status unavailable: %s%s\n", colorDim, shortErr(err), colorReset)

		return
	}

	modules, err := zxadmin.New(cfg).GetAllServicesStatus(ctx)
	if err != nil {
		logger.WarnContext(ctx, "Advanced status check failed", "error", err)
		fmt.Printf("\t%smodule status unavailable: %s%s\n", colorDim, shortErr(err), colorReset)

		return
	}

	for _, m := range modules {
		cliStatus(strings.ToLower(m.Name), m.Running, "")
	}
}

// shortErr returns a single-line, length-capped form of err for inline display.
func shortErr(err error) string {
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 140 {
		s = s[:137] + "..."
	}

	return s
}

func advancedJARsPresent() bool {
	matches, _ := os.ReadDir(basePath("lib", "ext", "carbonio"))
	for _, m := range matches {
		if strings.HasPrefix(m.Name(), "carbonio-advanced-") && strings.HasSuffix(m.Name(), ".jar") {
			return true
		}
	}

	return false
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
