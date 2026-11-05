// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	serviceCliComponent = "service-cli"
	errUnknownService   = "unknown service: %s"
	errMemberFmt        = "%s: %w"
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
//
// The verdict is cached for the Controller's lifetime, except when the probe
// failed (systemctl missing, hung or timed out): that is not an answer, so the
// call reports legacy mode and the next call probes again instead of pinning
// the daemon to a mode decided by a transient failure.
func (c *Controller) IsSystemdMode() bool {
	c.modeMu.Lock()
	defer c.modeMu.Unlock()

	if c.modeKnown {
		return c.systemdMode
	}

	enabled, err := c.systemd.IsSystemdEnabled(context.Background())
	if err != nil {
		logger.WarnContext(context.Background(), "systemd mode probe failed; not caching the verdict",
			"error", err)

		return false
	}

	c.systemdMode, c.modeKnown = enabled, true

	return enabled
}

// noRewriteKey marks a context whose start skips config regeneration for this
// service only (legacy `norewrite` argument), as opposed to the Controller-wide
// WithNoRewrite.
type noRewriteKey struct{}

// WithNoRewriteContext returns ctx marked so that a ServiceStart/ServiceRestart run
// with it skips the service's config rewrite and pre-start regeneration
// (zmaltermimeconfig). Composite services pass it on to their members.
func WithNoRewriteContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRewriteKey{}, true)
}

// rewriteSkipped reports whether this start must not regenerate configuration.
func (c *Controller) rewriteSkipped(ctx context.Context) bool {
	if c.noRewrite {
		return true
	}

	skipped, _ := ctx.Value(noRewriteKey{}).(bool)

	return skipped
}

// ServiceDisabled reports whether name is an IdleWhenDisabled service that is
// not enabled on this host: every lifecycle verb is then a successful no-op and
// status has nothing to report (legacy zmantispamctl).
func (c *Controller) ServiceDisabled(ctx context.Context, name string) bool {
	def := c.LookupService(name)

	return def != nil && c.idleWhenDisabled(ctx, def)
}

func (c *Controller) idleWhenDisabled(ctx context.Context, def *ServiceDef) bool {
	return def.IdleWhenDisabled && def.EnableCheck != nil && !def.EnableCheck(ctx)
}

// applicableMembers returns def's members that take part on this host, in
// start order.
func (c *Controller) applicableMembers(ctx context.Context, def *ServiceDef) []string {
	members := make([]string, 0, len(def.Members))

	for _, m := range def.Members {
		if def.MemberApplies == nil || def.MemberApplies(ctx, m) {
			members = append(members, m)
		}
	}

	return members
}

// stopOrder returns the members of def in the order they are stopped.
func (def *ServiceDef) stopOrder(members []string) []string {
	if def.StopMembersForward {
		return members
	}

	reversed := slices.Clone(members)
	slices.Reverse(reversed)

	return reversed
}

// ServiceStart starts a service by name, handling dependencies, config rewrite, and hooks.
func (c *Controller) ServiceStart(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	def := c.LookupService(name)
	if def == nil {
		return fmt.Errorf(errUnknownService, name)
	}

	if c.idleWhenDisabled(ctx, def) {
		logger.DebugContext(ctx, "Service is not enabled on this host; nothing to start", "service", name)

		return nil
	}

	if len(def.Members) > 0 {
		return c.startComposite(ctx, def)
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

	c.startWanted(ctx, name, def)

	if err := c.startEnabledDependencies(ctx, name, def); err != nil {
		return err
	}

	if !c.rewriteSkipped(ctx) && len(def.ConfigRewrite) > 0 {
		c.rewriteConfigs(ctx, def)
	}

	sm := NewServiceManager(c)

	if err := c.runPreStart(ctx, name, sm, def); err != nil {
		return err
	}

	if err := c.startService(ctx, name, def); err != nil {
		return fmt.Errorf("start service %s: %w", name, err)
	}

	runPostStartHooks(ctx, name, sm, def)

	return nil
}

// startWanted starts the Wants of def. A failure is logged, never fatal:
// mailbox must still try to come up when the database is slow, as legacy
// zmstorectl did.
func (c *Controller) startWanted(ctx context.Context, name string, def *ServiceDef) {
	for _, want := range def.Wants {
		logger.InfoContext(ctx, "Starting wanted service", "wants", want, "for", name)

		if err := c.ServiceStart(ctx, want); err != nil {
			logger.WarnContext(ctx, "Wanted service failed to start; continuing",
				"wants", want, "for", name, "error", err)
		}
	}
}

// startComposite starts a composite service: its own ConfigRewrite once, then
// every applicable member in order. A failing member does not stop the others
// (legacy zmstorectl/zmantivirusctl run every script and keep the last bad
// status); the errors are joined.
func (c *Controller) startComposite(ctx context.Context, def *ServiceDef) error {
	if !c.rewriteSkipped(ctx) && len(def.ConfigRewrite) > 0 {
		c.rewriteConfigs(ctx, def)
	}

	memberCtx := ctx
	if def.ForceMembersNoRewrite {
		memberCtx = WithNoRewriteContext(ctx)
	}

	var errs []error

	for _, member := range c.applicableMembers(ctx, def) {
		if err := c.ServiceStart(memberCtx, member); err != nil {
			errs = append(errs, fmt.Errorf(errMemberFmt, member, err))
		}
	}

	return errors.Join(errs...)
}

// stopComposite stops the applicable members of def in its stop order,
// continuing past failures.
func (c *Controller) stopComposite(ctx context.Context, def *ServiceDef, leaf bool) error {
	var errs []error

	for _, member := range def.stopOrder(c.applicableMembers(ctx, def)) {
		var err error
		if leaf {
			err = c.ServiceStopSystemd(ctx, member)
		} else {
			err = c.ServiceStop(ctx, member)
		}

		if err != nil {
			errs = append(errs, fmt.Errorf(errMemberFmt, member, err))
		}
	}

	return errors.Join(errs...)
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

	if c.idleWhenDisabled(ctx, def) {
		return nil
	}

	if len(def.Members) > 0 {
		var errs []error

		for _, member := range c.applicableMembers(ctx, def) {
			if err := c.ServiceStartSystemd(ctx, member); err != nil {
				errs = append(errs, fmt.Errorf(errMemberFmt, member, err))
			}
		}

		return errors.Join(errs...)
	}

	sm := NewServiceManager(c)

	if err := c.runPreStart(ctx, name, sm, def); err != nil {
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

// runPreStart runs what precedes a start: the declarative Setup steps, then
// the PreStart hooks.
func (c *Controller) runPreStart(ctx context.Context, name string, sm *ServiceManager, def *ServiceDef) error {
	if err := c.runSteps(ctx, def.Setup); err != nil {
		return fmt.Errorf("pre-start hook failed for %s: %w", name, err)
	}

	return runPreStartHooks(ctx, name, sm, def)
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

	if c.idleWhenDisabled(ctx, def) {
		return nil
	}

	if len(def.Members) > 0 {
		return c.stopComposite(ctx, def, false)
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

	if c.idleWhenDisabled(ctx, def) {
		return nil
	}

	if len(def.Members) > 0 {
		return c.stopComposite(ctx, def, true)
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

	// A service that reloads instead of restarting (the MTA) is never stopped
	// for a restart.
	if def.Commands != nil && def.Commands.RestartAsReload {
		if !c.rewriteSkipped(ctx) && len(def.ConfigRewrite) > 0 {
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

	if len(def.Members) > 0 {
		// Legacy zmstorectl/zmantivirusctl reload is a restart of every member.
		return c.ServiceRestart(ctx, name)
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
// directly by the collectors launcher into /init.scope, so their systemd unit is
// inactive even while the workers are live. In legacy mode we never ask
// systemctl, so ServiceStatus reports the true state and controlStop can
// invoke stopCollectors to actually terminate the workers.
func (c *Controller) ServiceStatus(ctx context.Context, name string) (bool, error) {
	def := c.LookupService(name)
	if def == nil {
		return false, fmt.Errorf(errUnknownService, name)
	}

	if len(def.Members) > 0 {
		return c.compositeStatus(ctx, def)
	}

	if c.IsSystemdMode() || def.UseSystemdForStatus {
		return c.systemdUnitsActive(ctx, def.SystemdUnits)
	}

	return c.runningPID(ctx, def) > 0 && c.companionsRunning(ctx, def), nil
}

// compositeStatus reports whether every applicable member of def is running.
func (c *Controller) compositeStatus(ctx context.Context, def *ServiceDef) (bool, error) {
	for _, member := range c.applicableMembers(ctx, def) {
		running, err := c.ServiceStatus(ctx, member)
		if err != nil || !running {
			return false, err
		}
	}

	return true, nil
}

// NotRunningMembers returns the display names of the applicable members of a
// composite that are not running, in start order: what a composite's status
// names when it is down (legacy storectl/antispamctl/antivirusctl print the
// script of each failing member). It is empty for a service without members
// and for a composite whose members all run.
func (c *Controller) NotRunningMembers(ctx context.Context, def *ServiceDef) []string {
	var down []string

	for _, member := range c.applicableMembers(ctx, def) {
		if running, err := c.ServiceStatus(ctx, member); err != nil || running {
			continue
		}

		if m := c.LookupService(member); m != nil {
			down = append(down, m.DisplayName)
		}
	}

	return down
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

// ServiceListStatusStream emits ServiceInfo entries (every service when
// includeComponents is set, else only those `control` orchestrates) on the returned channel
// as each service's probe completes, allowing callers to render rows
// incrementally without waiting for the full scan to finish. The channel is
// closed when all services have been probed. The legacy-mode ProcessName
// probes share a single /proc snapshot taken at the first one.
func (c *Controller) ServiceListStatusStream(ctx context.Context, includeComponents bool) <-chan ServiceInfo {
	names := c.ControlServiceNames()
	if includeComponents {
		names = c.AllServiceNames()
	}

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

// ServiceListStatus returns the services with their running status; see
// ServiceListStatusStream for includeComponents.
func (c *Controller) ServiceListStatus(ctx context.Context, includeComponents bool) []ServiceInfo {
	result := make([]ServiceInfo, 0, len(c.registry))

	for info := range c.ServiceListStatusStream(ctx, includeComponents) {
		result = append(result, info)
	}

	return result
}

func (c *Controller) rewriteConfigs(ctx context.Context, def *ServiceDef) {
	logger.InfoContext(ctx, "Rewriting configuration files", "service", def.Name, "configs", def.ConfigRewrite)

	if err := c.rewriteSections(ctx, def.ConfigRewrite); err != nil {
		logger.WarnContext(ctx, "Config rewrite failed",
			"service", def.Name, "configs", def.ConfigRewrite, "error", err)
	}
}

// rewriteSections regenerates the given configuration sections: through the
// legacy configrewrite script when it is installed, else through the running
// configd daemon (REWRITE protocol).
func (c *Controller) rewriteSections(ctx context.Context, sections []string) error {
	configrewrite := c.p.configrewrite
	if _, err := c.env.FS.Stat(configrewrite); err == nil {
		// #nosec G204 - args come from internal registry
		if output, err := sys.CombinedOutput(ctx, c.env.Exec, configrewrite, sections...); err != nil {
			return fmt.Errorf("%s: %s: %w", configrewrite, strings.TrimSpace(string(output)), err)
		}

		return nil
	}

	return c.rewriteViaConfigd(ctx, sections)
}

// RewriteConfigsFor regenerates, once, the union of the ConfigRewrite sections
// of the named services and of their composite members. `control start` calls
// it before starting the services `norewrite`.
func (c *Controller) RewriteConfigsFor(ctx context.Context, names []string) error {
	ctx = logger.ContextWithComponentOnce(ctx, serviceCliComponent)

	var sections []string

	seen := map[string]bool{}

	var collect func(name string)

	collect = func(name string) {
		def := c.LookupService(name)
		if def == nil || seen[def.Name] {
			return
		}

		seen[def.Name] = true

		for _, section := range def.ConfigRewrite {
			if !slices.Contains(sections, section) {
				sections = append(sections, section)
			}
		}

		for _, member := range def.Members {
			collect(member)
		}
	}

	for _, name := range names {
		collect(name)
	}

	if len(sections) == 0 {
		return nil
	}

	logger.InfoContext(ctx, "Rewriting configuration files", "services", names, "configs", sections)

	return c.rewriteSections(ctx, sections)
}

// RegenerateResolvers regenerates nginx's resolvers.conf by running
// zmresolverctl, as legacy zmcontrol does before it starts proxy. A host
// without the wrapper has nothing to regenerate.
func (c *Controller) RegenerateResolvers(ctx context.Context) error {
	if _, err := c.env.FS.Stat(c.p.resolverctl); err != nil {
		return nil //nolint:nilerr // wrapper not installed: nothing to do
	}

	if out, err := sys.CombinedOutput(ctx, c.env.Exec, c.p.resolverctl); err != nil {
		return fmt.Errorf("%s: %s: %w", c.p.resolverctl, strings.TrimSpace(string(out)), err)
	}

	return nil
}

// StatusListServices returns the set of canonical service names whose stopped
// state fails `control status`, from LC zmcontrol_service_status_list (legacy
// names, LDAP names and aliases accepted). It returns nil when the key is unset
// or empty: the caller then applies the default (every service but stats and
// logger).
func (c *Controller) StatusListServices() map[string]bool {
	lc, err := c.loadConfig()
	if err != nil {
		return nil
	}

	fields := strings.Fields(lc["zmcontrol_service_status_list"])
	if len(fields) == 0 {
		return nil
	}

	set := make(map[string]bool, len(fields))

	for _, f := range fields {
		set[MapLDAPServiceToRegistry(f)] = true
		if def := c.LookupService(f); def != nil {
			set[def.Name] = true
		}
	}

	return set
}

// rewriteViaConfigd sends a REWRITE command to the running configd daemon and
// waits for it to finish (synchronous, both loopback families, bounded by
// zimbra_configrewrite_timeout). Unlike "configd rewrite" it never falls back
// to an in-process rewrite: callers here only want the running daemon to do it.
func (c *Controller) rewriteViaConfigd(ctx context.Context, configs []string) error {
	resp, err := rewrite.Request(ctx, "REWRITE", configs, c.configdEndpoint())
	if err != nil {
		return fmt.Errorf("configd REWRITE: %w", err)
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
