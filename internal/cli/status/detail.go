// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package status

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

// Reporter renders service status for the CLI. It owns the host boundary
// (systemctl show, /proc, the Advanced extension directory) and the Controller
// that answers status/PID probes.
type Reporter struct {
	env sys.System
	ctl *services.Controller
	// loadLocalConfig reads the raw localconfig for the Advanced status probe.
	loadLocalConfig func() (map[string]string, error)
}

// NewReporter returns a Reporter that probes services through ctl and the host
// through env.
func NewReporter(env sys.System, ctl *services.Controller) *Reporter {
	return &Reporter{env: env, ctl: ctl, loadLocalConfig: localconfig.LoadLocalConfig}
}

// PrintServiceStatus reports one service's state plus PID/uptime detail.
// Shared by `configd service status <name>` and `configd status <name>`.
func (r *Reporter) PrintServiceStatus(ctx context.Context, c *cliutil.Console, name string) error {
	// The status verdict and the PID shown beside it come from one snapshot.
	ctx = r.ctl.WithProcessSnapshot(ctx)

	running, err := r.ctl.ServiceStatus(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get status for service %s: %w", name, err)
	}

	def := r.ctl.LookupService(name)
	if def == nil {
		return fmt.Errorf("unknown service: %s", name)
	}

	if !running {
		c.Printf("%s is not running.\n", def.DisplayName)

		return fmt.Errorf("service %s is not running", name)
	}

	c.Printf("%s is running.\n", def.DisplayName)
	r.showServiceDetail(ctx, c, def)

	return nil
}

// showServiceDetail prints PID/uptime, sourced to match the orchestration
// layer: systemctl show in strict systemd mode, /proc in legacy mode. Asking
// systemctl in legacy mode reports the unit's state, not the process configd
// actually spawned — on hosts with no unit at all (ubuntu-jammy, rocky-8)
// that yielded "Since: n/a" for a healthy daemon.
func (r *Reporter) showServiceDetail(ctx context.Context, c *cliutil.Console, def *services.ServiceDef) {
	if r.ctl.IsSystemdMode() {
		for _, unit := range def.SystemdUnits {
			r.showUnitDetail(ctx, c, unit)
		}

		return
	}

	pid := r.ctl.RunningPID(ctx, def)
	if pid == 0 {
		return
	}

	c.Printf("  PID: %d\n", pid)

	if since, ok := r.procStartTime(pid); ok {
		c.Printf("  Since: %s\n", since)
	}
}

func (r *Reporter) showUnitDetail(ctx context.Context, c *cliutil.Console, unit string) {
	// Get MainPID and ActiveEnterTimestamp from systemctl show
	// #nosec G702 - unit name comes from internal registry, not user input
	out, err := sys.Output(ctx, r.env.Exec, "systemctl", "show", unit,
		"--property=MainPID,ActiveEnterTimestamp,MemoryCurrent")
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
// (pid, uptime). Bifurcates on Controller.IsSystemdMode() to match the orchestration
// layer: in strict systemd mode the authoritative source is systemctl show;
// in legacy mode those values are stale/empty (systemd only sees the units
// if the container bootstrap once touched them, and MainPID stays 0 because
// it never tracked the real processes), so we read PID from the same probes
// used by ServiceStatus and start time from /proc/<pid>'s mtime.
func (r *Reporter) ServiceDetail(ctx context.Context, name string, running bool) string {
	if !running {
		return ""
	}

	def := r.ctl.LookupService(name)
	if def == nil {
		return ""
	}

	if r.ctl.IsSystemdMode() {
		return r.serviceDetailFromSystemd(ctx, def)
	}

	return r.serviceDetailFromProc(ctx, def)
}

// serviceDetailFromSystemd reads MainPID and ActiveEnterTimestamp from
// systemctl show. Correct only when strict systemd mode is active — that's
// the mode in which every start/stop actually went through systemd.
func (r *Reporter) serviceDetailFromSystemd(ctx context.Context, def *services.ServiceDef) string {
	if len(def.SystemdUnits) == 0 {
		return ""
	}

	// #nosec G204 — unit from internal registry
	out, err := sys.Output(ctx, r.env.Exec, "systemctl", "show", def.SystemdUnits[0],
		"--property=MainPID,ActiveEnterTimestamp")
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
// comes from Controller.RunningPID (the same probe as ServiceStatus, over the
// process snapshot ctx carries) and the "since" timestamp is /proc/<pid>'s
// mtime, which on Linux is set at proc-entry creation (close enough to
// process start for display).
func (r *Reporter) serviceDetailFromProc(ctx context.Context, def *services.ServiceDef) string {
	pid := r.ctl.RunningPID(ctx, def)
	if pid == 0 {
		return ""
	}

	parts := []string{fmt.Sprintf("pid %d", pid)}

	if since, ok := r.procStartTime(pid); ok {
		parts = append(parts, "since "+since)
	}

	return "(" + strings.Join(parts, ", ") + ")"
}

// procStartTime formats /proc/<pid>'s mtime, which Linux sets when the
// proc entry is created — close enough to process start for display.
func (r *Reporter) procStartTime(pid int) (string, bool) {
	info, err := r.env.FS.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return "", false
	}

	return info.ModTime().UTC().Format("Mon 2006-01-02 15:04:05 MST"), true
}

// Advanced prints the Carbonio Advanced module section when the Advanced
// extension is installed, and nothing otherwise.
func (r *Reporter) Advanced(ctx context.Context, c *cliutil.Console) {
	if !r.advancedJARsPresent() {
		return
	}

	c.Heading("Carbonio Advanced")

	cfg, err := r.loadLocalConfig()
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

func (r *Reporter) advancedJARsPresent() bool {
	matches, _ := r.env.FS.ReadDir(cliutil.BasePath("lib", "ext", "carbonio"))
	for _, m := range matches {
		if strings.HasPrefix(m.Name(), "carbonio-advanced-") && strings.HasSuffix(m.Name(), ".jar") {
			return true
		}
	}

	return false
}
