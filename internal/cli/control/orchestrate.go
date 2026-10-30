// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"context"
	"fmt"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/status"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/services"
)

const (
	ldapServiceName    = "ldap"
	configdServiceName = "configd"
)

// controlStart implements the legacy `zmcontrol start` orchestration:
//  1. If LDAP is local and stopped, start it FIRST (single-server hosts always;
//     multi-server LDAP-replica hosts; multi-server non-LDAP nodes skip this).
//  2. Start configd next so REWRITE-protocol calls in subsequent service
//     starts have a daemon to talk to. Without this every dependent service
//     would fail to regenerate its config.
//  3. Then iterate the rest of the registered services in legacy %startorder
//     (mailbox, memcached, proxy, amavis, ..., mta, stats), filtering by what
//     LDAP says is enabled on this host.
func controlStart(ctx context.Context, c *cliutil.Console) int {
	c.Header()

	if err := startBootstrapServices(ctx, c); err != nil {
		return 1
	}

	enabledList, err := services.DiscoverEnabledServices(ctx)
	if err != nil {
		c.Errorf("Cannot determine services: %v\n", err)

		return 1
	}

	threshold := services.GetDiskThreshold()
	bd := cliutil.BaseDir()

	// CheckDiskSpace returns ok=true on error (statfs failure), matching legacy
	// hasAvailableSpace which stays silent for unreadable/missing dirs, so the
	// warning below can only fire with a real measured value.
	avail, ok, _ := services.CheckDiskSpace(bd, threshold)
	if !ok {
		c.Warn("Disk space below threshold for %s (%dMB available, %dMB required)", bd, avail, threshold)
	}

	enabledSet := make(map[string]bool, len(enabledList))
	for _, s := range enabledList {
		enabledSet[services.MapLDAPServiceToRegistry(s)] = true
	}

	return startEnabledServices(ctx, c, enabledSet, threshold)
}

// controlSvcStatusFn is overridden in tests.
var controlSvcStatusFn = services.ServiceStatus

// statusForStart reports whether name is running. A probe error (as opposed to
// plain "not running", which is (false, nil)) must not be read as stopped: the
// start path aborts instead of blindly starting a service that may be up.
func statusForStart(ctx context.Context, c *cliutil.Console, name string) (bool, error) {
	running, err := controlSvcStatusFn(ctx, name)
	if err != nil {
		c.Warn("Cannot determine status of %s: %v", name, err)

		return false, fmt.Errorf("status of %s: %w", name, err)
	}

	return running, nil
}

// runningForStop reports whether name should be stopped. On a probe error the
// service is assumed possibly running so the stop is still attempted (legacy
// control.pl always invoked `<svc>ctl stop`; it never skipped on a failed probe).
func runningForStop(ctx context.Context, c *cliutil.Console, name string) bool {
	running, err := controlSvcStatusFn(ctx, name)
	if err != nil {
		c.Warn("Cannot determine status of %s: %v; attempting stop anyway", name, err)

		return true
	}

	return running
}

func startBootstrapServices(ctx context.Context, c *cliutil.Console) error {
	if services.IsLDAPLocal() {
		running, statErr := statusForStart(ctx, c, ldapServiceName)
		if statErr != nil {
			return statErr
		}

		if !running {
			def := services.LookupService(ldapServiceName)
			done := c.Progress("Starting", def.DisplayName)

			err := services.ServiceStart(ctx, ldapServiceName)
			done(err)

			if err != nil {
				return err
			}
		}
	}

	running, statErr := statusForStart(ctx, c, configdServiceName)
	if statErr != nil {
		return statErr
	}

	if !running {
		def := services.LookupService(configdServiceName)
		done := c.Progress("Starting", def.DisplayName)

		err := services.ServiceStart(ctx, configdServiceName)
		done(err)

		if err != nil {
			return err
		}
	}

	return nil
}

func startEnabledServices(ctx context.Context, c *cliutil.Console, enabledSet map[string]bool, threshold int) int {
	rc := 0

	for _, name := range services.AllServiceNames() {
		if name == ldapServiceName || name == configdServiceName {
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

		done := c.Progress("Starting", def.DisplayName)
		startErr := services.ServiceStart(ctx, name)
		done(startErr)

		if startErr != nil {
			rc = 1
		}
	}

	return rc
}

func controlStop(ctx context.Context, c *cliutil.Console) int {
	c.Header()

	rc := 0
	ordered := services.AllServiceNames()

	// Reverse order for stop
	for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
		ordered[i], ordered[j] = ordered[j], ordered[i]
	}

	for _, name := range ordered {
		if name == configdServiceName || name == ldapServiceName {
			continue
		}

		def := services.LookupService(name)
		if def == nil {
			continue
		}

		if !runningForStop(ctx, c, name) {
			continue
		}

		done := c.Progress("Stopping", def.DisplayName)
		stopErr := services.ServiceStop(ctx, name)
		done(stopErr)

		if stopErr != nil {
			rc = 1
		}
	}

	// Stop LDAP last if local
	rc |= stopLDAPIfLocal(ctx, c, nil)

	// Stop the configd daemon itself — it must be last so it can handle
	// rewriteConfigs calls from other services during their own shutdown.
	if runningForStop(ctx, c, configdServiceName) {
		def := services.LookupService(configdServiceName)
		done := c.Progress("Stopping", def.DisplayName)
		stopErr := services.ServiceStop(ctx, configdServiceName)
		done(stopErr)

		if stopErr != nil {
			rc = 1
		}
	}

	return rc
}

// stopLDAPIfLocal stops LDAP if it's local and was enabled (or LDAP discovery failed).
func stopLDAPIfLocal(ctx context.Context, c *cliutil.Console, enabledSet map[string]bool) int {
	if enabledSet != nil && !enabledSet[ldapServiceName] {
		return 0
	}

	if !services.IsLDAPLocal() {
		return 0
	}

	if !runningForStop(ctx, c, ldapServiceName) {
		return 0
	}

	def := services.LookupService(ldapServiceName)
	done := c.Progress("Stopping", def.DisplayName)
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
// Exit code: rc=1 if any displayed service except stats/logger is
// Stopped/Failed or discovery failed, else rc=0 (legacy doStatus).
func controlStatus(ctx context.Context, c *cliutil.Console) int {
	c.Header()

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
		s[configdServiceName] = true

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

		detail := status.ServiceDetail(ctx, info.Name, info.Running)
		c.Status(info.DisplayName, info.Running, detail)
		rows = append(rows, svcRow{ServiceInfo: info, detail: detail})
	}

	if disc.failed {
		logger.WarnContext(ctx, "Service discovery unavailable, displayed full registry as fallback")
	}

	status.Advanced(ctx, c)

	if disc.failed {
		// Legacy: "Cannot determine services" is rc=1.
		return 1
	}

	for _, r := range rows {
		// Legacy %serviceStatusList excludes stats and logger by default.
		if !r.Running && r.Name != "stats" && r.Name != "logger" {
			return 1
		}
	}

	return 0
}
