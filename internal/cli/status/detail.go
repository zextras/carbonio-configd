// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// Overridable in tests, mirroring the isSystemdModeFn seam in internal/services:
// both probes hit the live host, so CLI rendering is otherwise untestable.
var (
	serviceStatusFn = services.ServiceStatus
	isSystemdModeFn = services.IsSystemdMode
)

// Overridable in tests: the real loader reads /opt/zextras/conf/localconfig.xml.
var loadLocalConfigFn = localconfig.LoadLocalConfig

// PrintServiceStatus reports one service's state plus PID/uptime detail.
// Shared by `configd service status <name>` and `configd status <name>`.
func PrintServiceStatus(ctx context.Context, c *cliutil.Console, name string) error {
	running, err := serviceStatusFn(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get status for service %s: %w", name, err)
	}

	def := services.LookupService(name)
	if def == nil {
		return fmt.Errorf("unknown service: %s", name)
	}

	if !running {
		c.Printf("%s is not running.\n", def.DisplayName)

		return fmt.Errorf("service %s is not running", name)
	}

	c.Printf("%s is running.\n", def.DisplayName)
	showServiceDetail(ctx, c, def)

	return nil
}

// showServiceDetail prints PID/uptime, sourced to match the orchestration
// layer: systemctl show in strict systemd mode, /proc in legacy mode. Asking
// systemctl in legacy mode reports the unit's state, not the process configd
// actually spawned — on hosts with no unit at all (ubuntu-jammy, rocky-8)
// that yielded "Since: n/a" for a healthy daemon.
func showServiceDetail(ctx context.Context, c *cliutil.Console, def *services.ServiceDef) {
	if isSystemdModeFn() {
		for _, unit := range def.SystemdUnits {
			showUnitDetail(ctx, c, unit)
		}

		return
	}

	pid := services.RunningPID(def)
	if pid == 0 {
		return
	}

	c.Printf("  PID: %d\n", pid)

	if since, ok := procStartTime(pid); ok {
		c.Printf("  Since: %s\n", since)
	}
}

func showUnitDetail(ctx context.Context, c *cliutil.Console, unit string) {
	// Get MainPID and ActiveEnterTimestamp from systemctl show
	// #nosec G702 - unit name comes from internal registry, not user input
	out, err := exec.CommandContext(ctx, "systemctl", "show", unit,
		"--property=MainPID,ActiveEnterTimestamp,MemoryCurrent").Output()
	if err != nil {
		return
	}

	props := parseSystemctlShow(string(out))

	if pid, ok := props["MainPID"]; ok && pid != "" && pid != "0" {
		c.Printf("  PID: %s\n", pid)
	}

	// systemd renders an unset timestamp as "n/a" — never print that as a
	// start time.
	if ts, ok := props["ActiveEnterTimestamp"]; ok && ts != "" && ts != "n/a" {
		c.Printf("  Since: %s\n", ts)
	}

	if mem, ok := props["MemoryCurrent"]; ok && mem != "" && mem != "[not set]" {
		c.Printf("  Memory: %s\n", mem)
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

// ServiceDetail returns a short detail string for running services
// (pid, uptime). Bifurcates on IsSystemdMode() to match the orchestration
// layer: in strict systemd mode the authoritative source is systemctl show;
// in legacy mode those values are stale/empty (systemd only sees the units
// if the container bootstrap once touched them, and MainPID stays 0 because
// it never tracked the real processes), so we read PID from the same probes
// used by ServiceStatus and start time from /proc/<pid>'s mtime.
func ServiceDetail(ctx context.Context, name string, running bool) string {
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

	if since, ok := procStartTime(pid); ok {
		parts = append(parts, "since "+since)
	}

	return "(" + strings.Join(parts, ", ") + ")"
}

// procStartTime formats /proc/<pid>'s mtime, which Linux sets when the
// proc entry is created — close enough to process start for display.
func procStartTime(pid int) (string, bool) {
	info, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return "", false
	}

	return info.ModTime().UTC().Format("Mon 2006-01-02 15:04:05 MST"), true
}

// Advanced prints the Carbonio Advanced module section when the Advanced
// extension is installed, and nothing otherwise.
func Advanced(ctx context.Context, c *cliutil.Console) {
	if !advancedJARsPresent() {
		return
	}

	c.Heading("Carbonio Advanced")

	cfg, err := loadLocalConfigFn()
	if err != nil {
		logger.WarnContext(ctx, "Advanced status check: localconfig load failed", "error", err)
		c.Note(advancedErrorLine(err))

		return
	}

	modules, err := zxadmin.New(cfg).GetAllServicesStatus(ctx)
	if err != nil {
		logger.WarnContext(ctx, "Advanced status check failed", "error", err)
		c.Note(advancedErrorLine(err))

		return
	}

	for _, m := range modules {
		c.Status(strings.ToLower(m.Name), m.Running, "")
	}
}

// advancedErrorLine renders a failed Advanced status probe. A 404 from
// mailboxd means the extension registered no handler, i.e. Advanced failed to
// boot — report that like legacy advanced_status.sh did, instead of echoing
// mailboxd's HTML error page.
func advancedErrorLine(err error) string {
	if errors.Is(err, zxadmin.ErrAdvancedNotRunning) {
		return "Carbonio Advanced is not running"
	}

	return "module status unavailable: " + shortErr(err)
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
	matches, _ := os.ReadDir(cliutil.BasePath("lib", "ext", "carbonio"))
	for _, m := range matches {
		if strings.HasPrefix(m.Name(), "carbonio-advanced-") && strings.HasSuffix(m.Name(), ".jar") {
			return true
		}
	}

	return false
}
