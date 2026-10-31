// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	serviceCliComponent = "service-cli"
	errUnknownService   = "unknown service: %s"
)

// systemctlBin is the absolute systemctl path (no PATH lookup).
const systemctlBin = "/usr/bin/systemctl"

// sudoBin is the absolute sudo path (no PATH lookup).
const sudoBin = "/usr/bin/sudo"

// ErrSystemdNotBooted is returned by Systemctl when /run/systemd/system is
// missing — i.e. the host did not boot with systemd as PID 1.
var ErrSystemdNotBooted = fmt.Errorf("systemd is not the init system on this host")

// ServiceInfo holds service name and status for display.
type ServiceInfo struct {
	Name        string
	DisplayName string
	Running     bool
}

// IsSystemdMode returns true if any Carbonio systemd role target is enabled.
// This is the single orchestration-mode gate — the two modes are mutually
// exclusive:
//
//   - true  → strict systemd: every start/stop/status goes through systemctl;
//     no direct binary spawn, no pkill fallback.
//   - false → pure legacy: direct binary execution, PID/ProcessName probes;
//     systemctl is not invoked at all.
//
// Callers must NOT gate on systemd.IsBooted() — the init system being systemd
// does not imply Carbonio is managed by it. Only target enablement does.
func (c *Controller) IsSystemdMode() bool {
	c.modeOnce.Do(func() {
		c.systemdMode = c.systemd.IsSystemdEnabled(context.Background())
	})

	return c.systemdMode
}

// ServiceStart starts a service by name, handling dependencies, config rewrite, and hooks.
func (c *Controller) ServiceStart(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	// Skip if already running — avoids redundant starts and dependency hangs
	running, statusErr := c.ServiceStatus(ctx, name)
	if statusErr != nil {
		logger.WarnContext(ctx, "Failed to check service status, proceeding with start",
			"service", name, "error", statusErr)
	}

	if running {
		logger.InfoContext(ctx, "Service already running, skipping start",
			"service", name, "pid", c.RunningPID(ctx, def))

		return nil
	}

	if err := c.startEnabledDependencies(ctx, name, def); err != nil {
		return err
	}

	if !c.noRewrite && len(def.ConfigRewrite) > 0 {
		c.rewriteConfigs(ctx, def)
	}

	sm := NewServiceManager(c)

	if err := runPreStartHooks(ctx, name, sm, def); err != nil {
		return err
	}

	if err := c.startService(ctx, name, def); err != nil {
		return fmt.Errorf("start service %s: %w", name, err)
	}

	runPostStartHooks(ctx, name, sm, def)

	return nil
}

// ServiceStartSystemd is the leaf start path invoked from the carbonio-*.service
// unit's ExecStart (via the zm*ctl wrapper -> `configd service start-systemd
// <name>`). It launches the service in-process through the legacy/custom
// launcher (CustomStart, then BinaryPath) and NEVER calls systemctl. This is
// what breaks the otherwise-infinite loop: `configd service start <name>` ->
// `systemctl start carbonio-<name>.service` -> ExecStart -> here, which spawns
// the workers directly instead of re-entering systemctl.
func (c *Controller) ServiceStartSystemd(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	sm := NewServiceManager(c)

	if err := runPreStartHooks(ctx, name, sm, def); err != nil {
		return err
	}

	if err := c.startWithoutSystemd(ctx, name, def); err != nil {
		return fmt.Errorf("start service %s: %w", name, err)
	}

	runPostStartHooks(ctx, name, sm, def)

	return nil
}

func (c *Controller) startEnabledDependencies(ctx context.Context, name string, def *ServiceDef) error {
	for _, dep := range def.Dependencies {
		if !c.isDepEnabled(ctx, dep) {
			logger.DebugContext(ctx, "Skipping disabled dependency", "dependency", dep, "for", name)

			continue
		}

		logger.InfoContext(ctx, "Starting dependency", "dependency", dep, "for", name)

		if err := c.ServiceStart(ctx, dep); err != nil {
			return fmt.Errorf("failed to start dependency %s: %w", dep, err)
		}
	}

	return nil
}

func runPreStartHooks(ctx context.Context, name string, sm *ServiceManager, def *ServiceDef) error {
	for _, hook := range def.PreStart {
		if err := hook(ctx, sm); err != nil {
			return fmt.Errorf("pre-start hook failed for %s: %w", name, err)
		}
	}

	return nil
}

func runPostStartHooks(ctx context.Context, name string, sm *ServiceManager, def *ServiceDef) {
	for _, hook := range def.PostStart {
		if err := hook(ctx, sm); err != nil {
			logger.WarnContext(ctx, "Post-start hook failed", "service", name, "error", err)
		}
	}
}

// ServiceStop stops a service by name, handling hooks and dependencies.
func (c *Controller) ServiceStop(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	sm := NewServiceManager(c)

	for _, hook := range def.PreStop {
		if err := hook(ctx, sm); err != nil {
			logger.WarnContext(ctx, "Pre-stop hook failed", "service", name, "error", err)
		}
	}

	// Stop the service — try systemctl first, fall back to direct process
	if err := c.stopService(ctx, name, def); err != nil {
		return fmt.Errorf("stop service %s: %w", name, err)
	}

	for i := range slices.Backward(def.Dependencies) {
		dep := def.Dependencies[i]

		if !c.isDepEnabled(ctx, dep) {
			continue
		}

		logger.InfoContext(ctx, "Stopping dependency", "dependency", dep, "for", name)

		if err := c.ServiceStop(ctx, dep); err != nil {
			logger.WarnContext(ctx, "Failed to stop dependency", "dependency", dep, "error", err)
		}
	}

	return nil
}

// ServiceStopSystemd is the leaf stop path invoked from the carbonio-*.service
// unit's ExecStop (via `configd service stop-systemd <name>`). It shuts the
// service down in-process via the legacy/custom path (CustomStop, then pkill by
// ProcessName) and NEVER calls systemctl, mirroring ServiceStartSystemd.
func (c *Controller) ServiceStopSystemd(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	sm := NewServiceManager(c)

	for _, hook := range def.PreStop {
		if err := hook(ctx, sm); err != nil {
			logger.WarnContext(ctx, "Pre-stop hook failed", "service", name, "error", err)
		}
	}

	if err := c.stopWithoutSystemd(ctx, name, def); err != nil {
		return fmt.Errorf("stop service %s: %w", name, err)
	}

	return nil
}

// ServiceRestart restarts a service.
func (c *Controller) ServiceRestart(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	// MTA restart is converted to reload for graceful handling
	if name == serviceMTA {
		if !c.noRewrite && len(def.ConfigRewrite) > 0 {
			c.rewriteConfigs(ctx, def)
		}

		return c.ServiceReload(ctx, name)
	}

	if err := c.ServiceStop(ctx, name); err != nil {
		logger.WarnContext(ctx, "Stop failed during restart, attempting start anyway",
			"service", name, "error", err)
	}

	return c.ServiceStart(ctx, name)
}

// ServiceReload sends a reload signal to a service. Bifurcated on
// IsSystemdMode(), mirroring startService/stopService:
//
//   - strict systemd: try systemctl reload per unit; on failure fall back
//     to stopService+startService (unchanged from before this fix).
//   - legacy: systemctl is never invoked, even as a fallback. See
//     reloadWithoutSystemd in cli_process.go for the native-reload /
//     stop+start fallback logic.
func (c *Controller) ServiceReload(ctx context.Context, name string) error {
	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	if def.ExternallyManaged {
		logger.DebugContext(ctx, "Service is externally managed, not reloading", "service", name)

		return nil
	}

	if !c.IsSystemdMode() {
		return c.reloadWithoutSystemd(ctx, name, def)
	}

	for _, unit := range def.SystemdUnits {
		logger.InfoContext(ctx, "Reloading service", "service", def.Name, "unit", unit)

		if err := c.Systemctl(ctx, "reload", unit); err != nil {
			// Fallback: try stop+start if reload not supported
			logger.WarnContext(ctx, "Reload failed, trying restart", "service", name, "error", err)

			if stopErr := c.stopService(ctx, name, def); stopErr != nil {
				return fmt.Errorf("failed to stop %s during reload fallback: %w", name, stopErr)
			}

			return c.startService(ctx, name, def)
		}
	}

	return nil
}

// ServiceStatus returns whether a service is running. Bifurcated on
// IsSystemdMode(); the two paths never mix, with one exception:
//
//   - strict systemd: trust systemctl is-active for each unit. No PID scan.
//   - legacy: process identity (procident.go): the pidfile PID validated
//     against the service binary, else an exe/argv scan of /proc. A service
//     that is not running is (false, nil). Never systemctl.
//     Exception: services with UseSystemdForStatus=true always use systemctl
//     is-active even in legacy mode (e.g. service-discover, which is always
//     managed by its systemd unit regardless of Carbonio orchestration mode).
//
// The legacy path covers the container case that motivated this design
// (podman without carbonio targets enabled): services like stats are spawned
// directly by statsCustomStart into /init.scope, so their systemd unit is
// inactive even while the workers are live. In legacy mode we never ask
// systemctl, so ServiceStatus reports the true state and controlStop can
// invoke statsCustomStop to actually terminate the workers.
func (c *Controller) ServiceStatus(ctx context.Context, name string) (bool, error) {
	def := c.LookupService(name)
	if def == nil {
		return false, fmt.Errorf(errUnknownService, name)
	}

	if c.IsSystemdMode() || def.UseSystemdForStatus {
		return c.systemdUnitsActive(ctx, def.SystemdUnits)
	}

	return c.runningPID(ctx, def) > 0, nil
}

// systemdUnitsActive reports whether every unit is active. A non-zero
// systemctl exit (unit inactive/failed/unknown) or an unbooted systemd is a
// "not running" answer; only a failure to run systemctl is an error.
func (c *Controller) systemdUnitsActive(ctx context.Context, units []string) (bool, error) {
	for _, unit := range units {
		if err := c.Systemctl(ctx, "is-active", unit); err != nil {
			var exitErr *sys.ExitError
			if errors.As(err, &exitErr) || errors.Is(err, ErrSystemdNotBooted) {
				return false, nil
			}

			return false, fmt.Errorf("systemctl is-active %s: %w", unit, err)
		}
	}

	return true, nil
}

// RunningPID returns the primary PID of a running service, or 0 when the
// service is not running or its PID cannot be determined. It is the same
// probe as the legacy-mode branch of ServiceStatus (see runningPID), and
// shares the snapshot carried by ctx (WithProcessSnapshot), so callers that
// format status detail (pid/since) from /proc see the very process, in the
// same process-table view, that ServiceStatus reported as running.
func (c *Controller) RunningPID(ctx context.Context, def *ServiceDef) int {
	if def == nil {
		return 0
	}

	return c.runningPID(ctx, def)
}

// ServiceListStatusStream emits ServiceInfo entries on the returned channel
// as each service's probe completes, allowing callers to render rows
// incrementally without waiting for the full scan to finish. The channel is
// closed when all services have been probed. The legacy-mode ProcessName
// probes share a single /proc snapshot taken at the first one.
func (c *Controller) ServiceListStatusStream(ctx context.Context) <-chan ServiceInfo {
	names := c.AllServiceNames()
	ch := make(chan ServiceInfo, len(names))

	// One /proc walk serves every ProcessName probe of this listing.
	ctx = c.WithProcessSnapshot(ctx)

	go func() {
		defer close(ch)

		for _, name := range names {
			def := c.LookupService(name)
			running, _ := c.ServiceStatus(ctx, name)

			select {
			case ch <- ServiceInfo{
				Name:        name,
				DisplayName: def.DisplayName,
				Running:     running,
			}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}

// ServiceListStatus returns all services with their running status.
func (c *Controller) ServiceListStatus(ctx context.Context) []ServiceInfo {
	result := make([]ServiceInfo, 0, len(c.registry))

	for info := range c.ServiceListStatusStream(ctx) {
		result = append(result, info)
	}

	return result
}

func (c *Controller) rewriteConfigs(ctx context.Context, def *ServiceDef) {
	logger.InfoContext(ctx, "Rewriting configuration files", "service", def.Name, "configs", def.ConfigRewrite)

	// Use configrewrite script if available (legacy path)
	configrewrite := c.p.configrewrite
	if _, err := c.env.FS.Stat(configrewrite); err == nil {
		// #nosec G204 - args come from internal registry
		if output, err := sys.CombinedOutput(ctx, c.env.Exec, configrewrite, def.ConfigRewrite...); err != nil {
			logger.WarnContext(ctx, "Config rewrite failed",
				"service", def.Name, "error", err, "output", string(output))
		}

		return
	}

	// Fall back to the running configd daemon via the REWRITE TCP protocol
	if err := c.rewriteViaConfigd(ctx, def.ConfigRewrite); err != nil {
		logger.WarnContext(ctx, "Config rewrite via configd failed",
			"service", def.Name, "configs", def.ConfigRewrite, "error", err)
	}
}

// rewriteViaConfigd sends a REWRITE command to the running configd daemon.
func (c *Controller) rewriteViaConfigd(ctx context.Context, configs []string) error {
	port := 7171

	lc, err := c.loadConfig()
	if err == nil {
		if p, err := strconv.Atoi(lc["zmconfigd_listen_port"]); err == nil && p > 0 {
			port = p
		}
	}

	addr := net.JoinHostPort("localhost", strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return fmt.Errorf("configd not reachable at %s: %w", addr, err)
	}

	defer func() { _ = conn.Close() }()

	message := "REWRITE " + strings.Join(configs, " ") + "\n"
	if _, err := conn.Write([]byte(message)); err != nil {
		return fmt.Errorf("failed to send REWRITE: %w", err)
	}

	buf := make([]byte, 2048)

	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	resp := strings.TrimSpace(string(buf[:n]))
	if strings.HasPrefix(resp, "ERROR") {
		return fmt.Errorf("configd returned error: %s", resp)
	}

	logger.InfoContext(ctx, "Config rewrite via configd succeeded", "configs", configs, "response", resp)

	return nil
}

// isDepEnabled checks if a conditional dependency should be started.
func (c *Controller) isDepEnabled(ctx context.Context, dep string) bool {
	def := c.LookupService(dep)
	if def == nil {
		return false
	}

	if def.EnableCheck != nil {
		return def.EnableCheck(ctx)
	}

	return true
}

// Systemctl executes a systemctl action on a unit. When systemd is not the
// init system on this host, returns ErrSystemdNotBooted immediately so callers
// can choose a fallback without paying for the systemctl invocation and without
// surfacing systemctl's multi-line "System has not been booted" stderr to users.
func (c *Controller) Systemctl(ctx context.Context, action, unit string) error {
	if !c.systemd.IsBooted() {
		return ErrSystemdNotBooted
	}

	// #nosec G204 — fixed binary path; action/unit come from the internal registry
	output, err := sys.CombinedOutput(ctx, c.env.Exec, systemctlBin, action, unit)
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", action, unit, strings.TrimSpace(string(output)), err)
	}

	return nil
}
