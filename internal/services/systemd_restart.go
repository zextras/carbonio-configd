// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// tryReloadOrRestartAction is the systemctl verb of a systemd-mode config
// restart: reload the units that support it, restart the others, and leave a
// unit that is not running alone.
const tryReloadOrRestartAction = "try-reload-or-restart"

// ServiceTryReloadOrRestart applies a config change to a running service on a
// systemd host with one "systemctl try-reload-or-restart <units>": the units
// that can reload do so, the others are restarted, and a service that is not
// running is left stopped (starting it is a separate decision). Services whose
// lifecycle configd only observes are left alone. It never touches a process
// directly; the caller must be in systemd mode.
func (c *Controller) ServiceTryReloadOrRestart(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	if def.ExternallyManaged {
		logger.DebugContext(ctx, "Service is externally managed, not reloading", "service", name)

		return nil
	}

	if len(def.SystemdUnits) == 0 {
		return fmt.Errorf("service %s has no systemd unit", name)
	}

	if !c.systemd.IsBooted() {
		return ErrSystemdNotBooted
	}

	logger.InfoContext(ctx, "Reloading or restarting service via systemctl",
		"service", def.Name, "units", def.SystemdUnits)

	args := append([]string{tryReloadOrRestartAction}, def.SystemdUnits...)

	// #nosec G204 — fixed binary path; the units come from the internal registry
	output, err := sys.CombinedOutput(ctx, c.env.Exec, systemctlBin, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", tryReloadOrRestartAction, strings.Join(def.SystemdUnits, " "),
			strings.TrimSpace(string(output)), err)
	}

	return nil
}

// hostBackend is what only the host Controller offers: the systemd mode, the
// systemd restart verb and the shared process snapshot. A hook backend (pods)
// replaces the host service control, so none of it applies there.
type hostBackend interface {
	IsSystemdMode() bool
	ServiceTryReloadOrRestart(ctx context.Context, name string) error
	WithProcessSnapshot(ctx context.Context) context.Context
}

var _ hostBackend = (*Controller)(nil)

// IsSystemdMode reports whether the services are managed through systemd
// units (a Carbonio role target is enabled); see Controller.IsSystemdMode. A
// manager over a hook backend is never in systemd mode: the hooks decide what
// a restart does.
func (sm *ServiceManager) IsSystemdMode() bool {
	host, ok := sm.ctl.(hostBackend)

	return ok && host.IsSystemdMode()
}

// ApplySystemd carries out one plan entry on a systemd host, the way the
// restart policy needs it: ActionRestart is a try-reload-or-restart of the
// service's units, ActionStart and ActionStop start or stop them. Other
// actions (notably ActionPendingRestart, which is never executed) are
// rejected. With DisableRestarts set nothing is executed and the entry is
// only logged, as ProcessRestarts does.
func (sm *ServiceManager) ApplySystemd(ctx context.Context, service string, action plan.Action) error {
	ctx = logger.ContextWithComponentOnce(ctx, "services")
	service = strings.ToLower(service)

	if sm.DisableRestarts {
		logger.DebugContext(ctx, "[DRY-RUN] Would apply systemd action",
			"service", service, "action", string(action))

		return nil
	}

	switch action {
	case plan.ActionRestart:
		host, ok := sm.ctl.(hostBackend)
		if !ok {
			return fmt.Errorf("service %s: the backend has no systemd restart", service)
		}

		return host.ServiceTryReloadOrRestart(ctx, service)
	case plan.ActionStart:
		// No rewrite: the daemon's own cycle has written the files (see ControlProcess).
		return sm.ctl.ServiceStart(WithNoRewriteContext(ctx), service)
	case plan.ActionStop:
		return sm.ctl.ServiceStop(ctx, service)
	case plan.ActionPendingRestart:
		return fmt.Errorf("action %q of service %s is never executed", action, service)
	default:
		return fmt.Errorf("invalid action %q for service %s", action, service)
	}
}

// WithProcessSnapshot returns ctx carrying a process-table snapshot shared by
// the status probes made with it; see Controller.WithProcessSnapshot.
func (sm *ServiceManager) WithProcessSnapshot(ctx context.Context) context.Context {
	if host, ok := sm.ctl.(hostBackend); ok {
		return host.WithProcessSnapshot(ctx)
	}

	return ctx
}
