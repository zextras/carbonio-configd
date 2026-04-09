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

	"github.com/zextras/carbonio-configd/internal/localconfig"
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

// ErrSystemdNotBooted is returned by Systemctl when /run/systemd/system is
// missing — i.e. the host did not boot with systemd as PID 1.
var ErrSystemdNotBooted = fmt.Errorf("systemd is not the init system on this host")

// ServiceInfo holds service name and status for display.
type ServiceInfo struct {
	Name        string
	DisplayName string
	Running     bool
}

// IsSystemdMode returns true if any Carbonio systemd target is enabled.
// When true, services are managed exclusively via systemctl (with polkit).
// When false, services are managed via direct binary execution (legacy mode).
func IsSystemdMode() bool {
	systemdModeOnce.Do(func() {
		mgr := systemd.NewManager()
		systemdMode = mgr.IsSystemdEnabled(context.Background())
	})

	return systemdMode
}

// ServiceStart starts a service by name, handling dependencies, config rewrite, and hooks.
func ServiceStart(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponent(ctx, serviceCliComponent)

	def := LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	// Skip if already running — avoids redundant starts and dependency hangs
	if running, _ := ServiceStatus(ctx, name); running {
		logger.DebugContext(ctx, "Service already running, skipping start", "service", name)

		return nil
	}

	if err := startEnabledDependencies(ctx, name, def); err != nil {
		return err
	}

	// Rewrite configs before start (unless --no-rewrite)
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

// startEnabledDependencies starts all enabled dependencies of the service in order.
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

// runPreStartHooks executes each pre-start hook and returns the first error encountered.
func runPreStartHooks(ctx context.Context, name string, sm *ServiceManager, def *ServiceDef) error {
	for _, hook := range def.PreStart {
		if err := hook(ctx, sm); err != nil {
			return fmt.Errorf("pre-start hook failed for %s: %w", name, err)
		}
	}

	return nil
}

// runPostStartHooks executes each post-start hook, logging but not returning errors.
func runPostStartHooks(ctx context.Context, name string, sm *ServiceManager, def *ServiceDef) {
	for _, hook := range def.PostStart {
		if err := hook(ctx, sm); err != nil {
			logger.WarnContext(ctx, "Post-start hook failed", "service", name, "error", err)
		}
	}
}

// ServiceStop stops a service by name, handling hooks and dependencies.
func ServiceStop(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponent(ctx, serviceCliComponent)

	def := LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	// Run pre-stop hooks
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

	// Stop dependencies in reverse order
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
	ctx = logger.ContextWithComponent(ctx, serviceCliComponent)

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

// ServiceStatus returns whether a service is running.
func ServiceStatus(ctx context.Context, name string) (bool, error) {
	def := LookupService(name)
	if def == nil {
		return false, fmt.Errorf(errUnknownService, name)
	}

	// Systemd mode: use systemctl exclusively — PID files are managed by
	// systemd and we should not second-guess its state tracking.
	if systemd.IsBooted() {
		for _, unit := range def.SystemdUnits {
			if checkErr := Systemctl(ctx, "is-active", unit); checkErr != nil {
				return false, nil //nolint:nilerr // not-active is not an error
			}
		}

		return true, nil
	}

	// Legacy mode (non-systemd): prefer PID file, then /proc scan fallback.
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

// ServiceListStatus returns all services with their running status.
func ServiceListStatus(ctx context.Context) []ServiceInfo {
	names := AllServiceNames()
	result := make([]ServiceInfo, 0, len(names))

	for _, name := range names {
		def := LookupService(name)
		running, _ := ServiceStatus(ctx, name)

		result = append(result, ServiceInfo{
			Name:        name,
			DisplayName: def.DisplayName,
			Running:     running,
		})
	}

	return result
}

// rewriteConfigs triggers config rewrite for the service's config names.
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

	lc, err := localconfig.LoadResolvedConfig()
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
