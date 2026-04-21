// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/systemd"
)

const (
	serviceCliComponent = "service-cli"
	errUnknownService   = "unknown service: %s"
)

// NoRewrite disables config rewriting before service start.
var NoRewrite bool

var (
	systemdMode     bool
	systemdModeOnce sync.Once
)

// isSystemdModeFn is the mode detector. Exposed as a variable so tests can
// force strict-systemd or pure-legacy behavior without manipulating the host.
var isSystemdModeFn = defaultIsSystemdMode

func defaultIsSystemdMode() bool {
	systemdModeOnce.Do(func() {
		mgr := systemd.NewManager()
		systemdMode = mgr.IsSystemdEnabled(context.Background())
	})

	return systemdMode
}

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
func IsSystemdMode() bool {
	return isSystemdModeFn()
}

// ServiceStart starts a service by name, handling dependencies, config rewrite, and hooks.
func ServiceStart(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	// Skip if already running — avoids redundant starts and dependency hangs
	running, statusErr := ServiceStatus(ctx, name)
	if statusErr != nil {
		logger.WarnContext(ctx, "Failed to check service status, proceeding with start",
			"service", name, "error", statusErr)
	}

	if running {
		logger.DebugContext(ctx, "Service already running, skipping start", "service", name)

		return nil
	}

	if err := startEnabledDependencies(ctx, name, def); err != nil {
		return err
	}

	if !NoRewrite && len(def.ConfigRewrite) > 0 {
		rewriteConfigs(ctx, def)
	}

	sm := newCLIServiceManager()

	if err := runPreStartHooks(ctx, name, sm, def); err != nil {
		return err
	}

	if err := startService(ctx, name, def); err != nil {
		return fmt.Errorf("start service %s: %w", name, err)
	}

	runPostStartHooks(ctx, name, sm, def)

	return nil
}

func startEnabledDependencies(ctx context.Context, name string, def *ServiceDef) error {
	for _, dep := range def.Dependencies {
		if !isDepEnabled(ctx, dep) {
			logger.DebugContext(ctx, "Skipping disabled dependency", "dependency", dep, "for", name)

			continue
		}

		logger.InfoContext(ctx, "Starting dependency", "dependency", dep, "for", name)

		if err := ServiceStart(ctx, dep); err != nil {
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
func ServiceStop(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	sm := newCLIServiceManager()

	for _, hook := range def.PreStop {
		if err := hook(ctx, sm); err != nil {
			logger.WarnContext(ctx, "Pre-stop hook failed", "service", name, "error", err)
		}
	}

	// Stop the service — try systemctl first, fall back to direct process
	if err := stopService(ctx, name, def); err != nil {
		return fmt.Errorf("stop service %s: %w", name, err)
	}

	for i := len(def.Dependencies) - 1; i >= 0; i-- {
		dep := def.Dependencies[i]

		if !isDepEnabled(ctx, dep) {
			continue
		}

		logger.InfoContext(ctx, "Stopping dependency", "dependency", dep, "for", name)

		if err := ServiceStop(ctx, dep); err != nil {
			logger.WarnContext(ctx, "Failed to stop dependency", "dependency", dep, "error", err)
		}
	}

	return nil
}

// ServiceRestart restarts a service.
func ServiceRestart(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	// MTA restart is converted to reload for graceful handling
	if name == serviceMTA {
		if !NoRewrite && len(def.ConfigRewrite) > 0 {
			rewriteConfigs(ctx, def)
		}

		return ServiceReload(ctx, name)
	}

	if err := ServiceStop(ctx, name); err != nil {
		logger.WarnContext(ctx, "Stop failed during restart, attempting start anyway",
			"service", name, "error", err)
	}

	return ServiceStart(ctx, name)
}

// ServiceReload sends a reload signal to a service.
func ServiceReload(ctx context.Context, name string) error {
	def := LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	for _, unit := range def.SystemdUnits {
		logger.InfoContext(ctx, "Reloading service", "service", def.Name, "unit", unit)

		if err := Systemctl(ctx, "reload", unit); err != nil {
			// Fallback: try stop+start if reload not supported
			logger.WarnContext(ctx, "Reload failed, trying restart", "service", name, "error", err)

			if stopErr := stopService(ctx, name, def); stopErr != nil {
				return fmt.Errorf("failed to stop %s during reload fallback: %w", name, stopErr)
			}

			return startService(ctx, name, def)
		}
	}

	return nil
}

// ServiceStatus returns whether a service is running. Bifurcated on
// IsSystemdMode(); the two paths never mix:
//
//   - strict systemd: trust systemctl is-active for each unit. No PID scan.
//   - legacy: PID file first, then ProcessName /proc scan. Never systemctl.
//
// The legacy path covers the container case that motivated this design
// (podman without carbonio targets enabled): services like stats are spawned
// directly by statsCustomStart into /init.scope, so their systemd unit is
// inactive even while the workers are live. In legacy mode we never ask
// systemctl, so ServiceStatus reports the true state and controlStop can
// invoke statsCustomStop to actually terminate the workers.
func ServiceStatus(ctx context.Context, name string) (bool, error) {
	def := LookupService(name)
	if def == nil {
		return false, fmt.Errorf(errUnknownService, name)
	}

	if IsSystemdMode() {
		for _, unit := range def.SystemdUnits {
			if err := Systemctl(ctx, "is-active", unit); err != nil {
				return false, nil //nolint:nilerr // not-active is not an error
			}
		}

		return true, nil
	}

	// PidFile may be unreadable (e.g. postfix master.pid is root:root 0600)
	// so we fall through to ProcessName scan on read failure.
	if def.PidFile != "" {
		if running, ok := isRunningByPidFile(def.PidFile); ok {
			return running, nil
		}
	}

	if def.ProcessName != "" {
		return isProcessRunning(ctx, def.ProcessName), nil
	}

	return false, nil
}

// RunningPID returns the primary PID of a running service, or 0 when the
// service is not running or its PID cannot be determined. Resolution order
// mirrors the legacy-mode branch of ServiceStatus (PidFile → ProcessName),
// so callers that format status detail (pid/since) from /proc see the same
// process that ServiceStatus reports as running.
func RunningPID(def *ServiceDef) int {
	if def == nil {
		return 0
	}

	if pid := pidFromPidFile(def.PidFile); pid > 0 {
		return pid
	}

	return pidFromProcessName(def.ProcessName)
}

// pidFromPidFile reads a pidfile and returns the PID when that PID points to
// a live (non-zombie) process. Returns 0 for any failure (unreadable,
// corrupt, or process gone).
func pidFromPidFile(pidFile string) int {
	if pidFile == "" {
		return 0
	}

	data, err := os.ReadFile(pidFile) //nolint:gosec // path is from internal registry
	if err != nil {
		return 0
	}

	pidStr, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")

	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil || pid <= 0 {
		return 0
	}

	if !processAlive(pid) {
		return 0
	}

	return pid
}

// pidFromProcessName scans /proc for the first cmdline match that isn't the
// current process or its parent. Empty processName short-circuits to 0.
func pidFromProcessName(processName string) int {
	if processName == "" {
		return 0
	}

	pids, err := scanProcessesByCmdline(processName)
	if err != nil {
		return 0
	}

	self := os.Getpid()
	parent := os.Getppid()

	for _, p := range pids {
		if p != self && p != parent {
			return p
		}
	}

	return 0
}

// ServiceListStatusStream emits ServiceInfo entries on the returned channel
// as each service's probe completes, allowing callers to render rows
// incrementally without waiting for the full scan to finish. The channel is
// closed when all services have been probed.
func ServiceListStatusStream(ctx context.Context) <-chan ServiceInfo {
	names := AllServiceNames()
	ch := make(chan ServiceInfo, len(names))

	go func() {
		defer close(ch)

		for _, name := range names {
			def := LookupService(name)
			running, _ := ServiceStatus(ctx, name)

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
func ServiceListStatus(ctx context.Context) []ServiceInfo {
	result := make([]ServiceInfo, 0, len(Registry))

	for info := range ServiceListStatusStream(ctx) {
		result = append(result, info)
	}

	return result
}

func rewriteConfigs(ctx context.Context, def *ServiceDef) {
	logger.InfoContext(ctx, "Rewriting configuration files", "service", def.Name, "configs", def.ConfigRewrite)

	// Use configrewrite script if available (legacy path)
	configrewrite := basePath + "/libexec/configrewrite"
	if _, err := os.Stat(configrewrite); err == nil {
		// #nosec G204 - args come from internal registry
		args := def.ConfigRewrite
		cmd := exec.CommandContext(ctx, configrewrite, args...)

		if output, err := cmd.CombinedOutput(); err != nil {
			logger.WarnContext(ctx, "Config rewrite failed",
				"service", def.Name, "error", err, "output", string(output))
		}

		return
	}

	// Fall back to the running configd daemon via the REWRITE TCP protocol
	if err := rewriteViaConfigd(ctx, def.ConfigRewrite); err != nil {
		logger.WarnContext(ctx, "Config rewrite via configd failed",
			"service", def.Name, "configs", def.ConfigRewrite, "error", err)
	}
}

// rewriteViaConfigd sends a REWRITE command to the running configd daemon.
func rewriteViaConfigd(ctx context.Context, configs []string) error {
	port := 7171

	lc, err := loadConfig()
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
func isDepEnabled(ctx context.Context, dep string) bool {
	def := LookupService(dep)
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
func Systemctl(ctx context.Context, action, unit string) error {
	if !systemd.IsBooted() {
		return ErrSystemdNotBooted
	}

	cmd := exec.CommandContext(ctx, "systemctl", action, unit)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", action, unit, strings.TrimSpace(string(output)), err)
	}

	return nil
}

func newCLIServiceManager() *ServiceManager {
	sm := NewServiceManager()
	sm.SetUseSystemd(true)

	return sm
}
