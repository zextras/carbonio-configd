// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"context"
	"fmt"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
)

const (
	ldapServiceName    = "ldap"
	configdServiceName = "configd"
	proxyServiceName   = "proxy"
)

// controlStart implements the legacy `zmcontrol start` orchestration:
//  1. If LDAP is local and stopped, start it FIRST (single-server hosts always;
//     multi-server LDAP-replica hosts; multi-server non-LDAP nodes skip this).
//  2. Start configd next so REWRITE-protocol calls in subsequent service
//     starts have a daemon to talk to. Without this every dependent service
//     would fail to regenerate its config.
//  3. Then iterate the rest of the registered services in legacy %startorder
//     (store, memcached, proxy, antispam, ..., mta, stats), filtering by what
//     LDAP says is enabled on this host. The configuration of all of them is
//     regenerated once up front; each service then starts `norewrite` (legacy
//     zmcontrol passes norewrite to every zm*ctl it runs). The resolver file
//     (zmresolverctl) is regenerated right before proxy.
func (o *orchestrator) controlStart(ctx context.Context, c *cliutil.Console) int {
	c.Header()

	if err := o.startBootstrapServices(ctx, c); err != nil {
		return 1
	}

	enabledList, err := o.ctl.DiscoverEnabledServices(ctx)
	if err != nil {
		c.Errorf("Cannot determine services: %v\n", err)

		return 1
	}

	threshold := o.ctl.GetDiskThreshold()
	bd := cliutil.BaseDir()

	// CheckDiskSpace returns ok=true on error (statfs failure), matching legacy
	// hasAvailableSpace which stays silent for unreadable/missing dirs, so the
	// warning below can only fire with a real measured value.
	avail, ok, _ := o.ctl.CheckDiskSpace(bd, threshold)
	if !ok {
		c.Warn("Disk space below threshold for %s (%dMB available, %dMB required)", bd, avail, threshold)
	}

	enabledSet := make(map[string]bool, len(enabledList))
	for _, s := range enabledList {
		enabledSet[services.MapLDAPServiceToRegistry(s)] = true
	}

	return o.startEnabledServices(ctx, c, enabledSet, threshold)
}

// statusForStart reports whether name is running. A probe error (as opposed to
// plain "not running", which is (false, nil)) must not be read as stopped: the
// start path aborts instead of blindly starting a service that may be up.
func (o *orchestrator) statusForStart(ctx context.Context, c *cliutil.Console, name string) (bool, error) {
	running, err := o.svcStatus(ctx, name)
	if err != nil {
		c.Warn("Cannot determine status of %s: %v", name, err)

		return false, fmt.Errorf("status of %s: %w", name, err)
	}

	return running, nil
}

// runningForStop reports whether name should be stopped. On a probe error the
// service is assumed possibly running so the stop is still attempted (legacy
// control.pl always invoked `<svc>ctl stop`; it never skipped on a failed probe).
func (o *orchestrator) runningForStop(ctx context.Context, c *cliutil.Console, name string) bool {
	running, err := o.svcStatus(ctx, name)
	if err != nil {
		c.Warn("Cannot determine status of %s: %v; attempting stop anyway", name, err)

		return true
	}

	return running
}

func (o *orchestrator) startBootstrapServices(ctx context.Context, c *cliutil.Console) error {
	if o.ctl.IsLDAPLocal() {
		running, statErr := o.statusForStart(ctx, c, ldapServiceName)
		if statErr != nil {
			return statErr
		}

		if !running {
			def := o.ctl.LookupService(ldapServiceName)
			done := c.Progress("Starting", def.DisplayName)

			err := o.ctl.ServiceStart(ctx, ldapServiceName)
			done(err)

			if err != nil {
				return err
			}
		}
	}

	running, statErr := o.statusForStart(ctx, c, configdServiceName)
	if statErr != nil {
		return statErr
	}

	if !running {
		def := o.ctl.LookupService(configdServiceName)
		done := c.Progress("Starting", def.DisplayName)

		err := o.ctl.ServiceStart(ctx, configdServiceName)
		done(err)

		if err != nil {
			return err
		}
	}

	return nil
}

func (o *orchestrator) startEnabledServices(
	ctx context.Context, c *cliutil.Console, enabledSet map[string]bool, threshold int,
) int {
	toStart := o.servicesToStart(ctx, enabledSet)

	if err := o.ctl.RewriteConfigsFor(ctx, toStart); err != nil {
		c.Warn("Configuration rewrite failed: %v", err)
	}

	startCtx := services.WithNoRewriteContext(ctx)
	rc := 0

	for _, name := range toStart {
		def := o.ctl.LookupService(name)
		if def == nil {
			continue
		}

		if !o.startOne(ctx, startCtx, c, name, def.DisplayName, threshold) {
			rc = 1
		}
	}

	return rc
}

// servicesToStart returns the control services (minus ldap and configd) that
// are enabled by LDAP or by a custom-enable marker, in control order.
func (o *orchestrator) servicesToStart(ctx context.Context, enabledSet map[string]bool) []string {
	toStart := make([]string, 0, len(enabledSet))

	for _, name := range o.ctl.ControlServiceNames() {
		if name == ldapServiceName || name == configdServiceName {
			continue
		}

		if !enabledSet[name] && !o.ctl.IsCustomEnabled(ctx, name) {
			continue
		}

		toStart = append(toStart, name)
	}

	return toStart
}

// startOne runs the pre-start checks and starts a single service, reporting
// whether the start succeeded.
func (o *orchestrator) startOne(
	ctx, startCtx context.Context, c *cliutil.Console, name, displayName string, threshold int,
) bool {
	o.ctl.CheckServiceDiskSpace(name, threshold)

	if name == proxyServiceName {
		if err := o.ctl.RegenerateResolvers(ctx); err != nil {
			c.Printf("resolvers.conf file generation failed.\n")
		}
	}

	done := c.Progress("Starting", displayName)
	startErr := o.ctl.ServiceStart(startCtx, name)
	done(startErr)

	return startErr == nil
}

func (o *orchestrator) controlStop(ctx context.Context, c *cliutil.Console) int {
	c.Header()

	rc := 0
	ordered := o.ctl.ControlServiceNames()

	// Reverse order for stop
	for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
		ordered[i], ordered[j] = ordered[j], ordered[i]
	}

	for _, name := range ordered {
		if name == configdServiceName || name == ldapServiceName {
			continue
		}

		def := o.ctl.LookupService(name)
		if def == nil {
			continue
		}

		if !o.runningForStop(ctx, c, name) {
			continue
		}

		done := c.Progress("Stopping", def.DisplayName)
		stopErr := o.ctl.ServiceStop(ctx, name)
		done(stopErr)

		if stopErr != nil {
			rc = 1
		}
	}

	// Stop LDAP last if local
	rc |= o.stopLDAPIfLocal(ctx, c, nil)

	// Stop the configd daemon itself — it must be last so it can handle
	// rewriteConfigs calls from other services during their own shutdown.
	if o.runningForStop(ctx, c, configdServiceName) {
		def := o.ctl.LookupService(configdServiceName)
		done := c.Progress("Stopping", def.DisplayName)
		stopErr := o.ctl.ServiceStop(ctx, configdServiceName)
		done(stopErr)

		if stopErr != nil {
			rc = 1
		}
	}

	return rc
}

// stopLDAPIfLocal stops LDAP if it's local and was enabled (or LDAP discovery failed).
func (o *orchestrator) stopLDAPIfLocal(ctx context.Context, c *cliutil.Console, enabledSet map[string]bool) int {
	if enabledSet != nil && !enabledSet[ldapServiceName] {
		return 0
	}

	if !o.ctl.IsLDAPLocal() {
		return 0
	}

	if !o.runningForStop(ctx, c, ldapServiceName) {
		return 0
	}

	def := o.ctl.LookupService(ldapServiceName)
	done := c.Progress("Stopping", def.DisplayName)
	stopErr := o.ctl.ServiceStop(ctx, ldapServiceName)
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
// When LDAP is unreachable, Controller.DiscoverEnabledServices already falls
// back to the on-disk cache; we propagate that opportunistically. As a last
// resort (LDAP and cache both unavailable) we degrade to the full registry
// view rather than printing nothing — operators must still see something.
//
// Exit code: rc=1 if any displayed service of the status list (LC
// zmcontrol_service_status_list; default: all but stats/logger) is
// Stopped/Failed or discovery failed, else rc=0 (legacy doStatus).
func (o *orchestrator) controlStatus(ctx context.Context, c *cliutil.Console) int {
	c.Header()

	// One process-table snapshot answers every status probe and every
	// pid/uptime detail of this run, so the rows are mutually consistent and
	// /proc is walked once.
	ctx = o.ctl.WithProcessSnapshot(ctx)

	discCh := make(chan discResult, 1)

	go func() { discCh <- o.discoverStatusSet(ctx) }()

	type svcRow struct {
		services.ServiceInfo
		detail string
	}

	var rows []svcRow

	stream := o.ctl.ServiceListStatusStream(ctx, false)
	disc := <-discCh

	for info := range stream {
		if disc.set != nil && !disc.set[info.Name] {
			continue
		}

		detail := o.reporter.ServiceDetail(ctx, info.Name, info.Running)
		c.Status(info.DisplayName, info.Running, detail)
		rows = append(rows, svcRow{ServiceInfo: info, detail: detail})
	}

	if disc.failed {
		logger.WarnContext(ctx, "Service discovery unavailable, displayed full registry as fallback")
	}

	o.reporter.Advanced(ctx, c)

	if disc.failed {
		// Legacy: "Cannot determine services" is rc=1.
		return 1
	}

	// Legacy %serviceStatusList: the services whose stopped state fails the
	// status. zmcontrol_service_status_list names them; unset, every service
	// but stats and logger counts.
	counted := o.ctl.StatusListServices()

	for _, r := range rows {
		if !r.Running && statusCounts(counted, r.Name) {
			return 1
		}
	}

	return 0
}

// discResult is the outcome of enabled-service discovery for controlStatus.
type discResult struct {
	set    map[string]bool
	failed bool
}

// discoverStatusSet resolves the set of registry services to display.
func (o *orchestrator) discoverStatusSet(ctx context.Context) discResult {
	list, err := o.ctl.DiscoverEnabledServices(ctx)
	if err != nil {
		return discResult{set: nil, failed: true}
	}

	s := make(map[string]bool, len(list)+1)
	for _, n := range list {
		s[services.MapLDAPServiceToRegistry(n)] = true
	}

	// Legacy parity: zmcontrol/control.pl always inserted "zmconfigd"
	// regardless of LDAP state, so configd is always shown.
	s[configdServiceName] = true

	return discResult{set: s, failed: false}
}

// statusCounts reports whether a stopped service name fails the status: the
// configured status list when set, else every service but stats and logger.
func statusCounts(counted map[string]bool, name string) bool {
	if counted != nil {
		return counted[name]
	}

	return name != "stats" && name != "logger"
}
