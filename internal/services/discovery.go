// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// legacyServiceNames are service names from LDAP that should be ignored.
// These are zimbraServiceEnabled values, not OS users.
var legacyServiceNames = map[string]bool{
	"zimlet":          true,
	"zimbraAdmin":     true,
	ldapServiceZimbra: true,
}

// DiscoverEnabledServices queries LDAP for services enabled on this host.
// Falls back to cache if LDAP is unreachable.
func (c *Controller) DiscoverEnabledServices(ctx context.Context) ([]string, error) {
	ctx = logger.ContextWithComponent(ctx, "discovery")

	lc, err := c.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load localconfig: %w", err)
	}

	hostname := lc["zimbra_server_hostname"]
	if hostname == "" {
		return nil, fmt.Errorf("zimbra_server_hostname not set in localconfig")
	}

	// ldap_master_url may be space-separated multi-URL (Carbonio HA): the
	// parameters carry the failover list (CO-3565) and the transport security
	// of the legacy Java rule (ResolveTLS).
	params, err := carboldap.ParamsFrom(ctx, lc, carboldap.PurposeDiscovery, c.env.FS)
	if err != nil {
		logger.WarnContext(ctx, "ldap_master_url is empty in localconfig, trying cache")

		return c.readCache(ctx)
	}

	client, err := carboldap.NewClient(params)
	if err != nil {
		logger.WarnContext(ctx, "LDAP connect failed, trying cache", "error", err)

		return c.readCache(ctx)
	}

	defer func() { _ = client.Close() }()

	raw, err := client.GetEnabledServices(hostname)
	if err != nil {
		logger.WarnContext(ctx, "LDAP query failed, trying cache", "error", err)

		return c.readCache(ctx)
	}

	// Filter legacy names
	services := make([]string, 0, len(raw))
	for _, s := range raw {
		if !legacyServiceNames[s] {
			services = append(services, s)
		}
	}

	// Always include zmconfigd/configd
	hasConfigd := false

	for _, s := range services {
		if s == svcZmconfigd || s == svcConfigd {
			hasConfigd = true

			break
		}
	}

	if !hasConfigd {
		services = append(services, svcZmconfigd)
	}

	c.writeCache(ctx, services)

	logger.InfoContext(ctx, "Discovered enabled services", "count", len(services), "services", services)

	return services, nil
}

// IsLDAPLocal returns true if the LDAP URL contains the local hostname.
func (c *Controller) IsLDAPLocal() bool {
	lc, err := c.loadConfig()
	if err != nil {
		return false
	}

	hostname := lc["zimbra_server_hostname"]
	ldapURL := lc["ldap_url"]

	return strings.Contains(ldapURL, hostname)
}

// MapLDAPServiceToRegistry maps an LDAP zimbraServiceEnabled value to its
// canonical Registry key: the LDAP-only names of ldapServiceNames ("mailbox"
// is the store composite) first, then the shared ServiceAliases table
// (registry.go) — the same aliasing LookupService uses. Falls back to
// ldapName unchanged when no alias applies (e.g. it's already a canonical
// registry name, or an LDAP-only name with no service-side control
// equivalent).
func MapLDAPServiceToRegistry(ldapName string) string {
	if canonical, ok := ldapServiceNames[ldapName]; ok {
		return canonical
	}

	if canonical, ok := ServiceAliases[ldapName]; ok {
		return canonical
	}

	return ldapName
}

func (c *Controller) readCache(ctx context.Context) ([]string, error) {
	data, err := c.env.FS.ReadFile(c.p.cacheFile)
	if err != nil {
		return nil, fmt.Errorf("cannot determine enabled services: LDAP unreachable and cache missing")
	}

	logger.WarnContext(ctx, "Using cached service list — may be inaccurate", "cache", c.p.cacheFile)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	services := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			services = append(services, line)
		}
	}

	return services, nil
}

func (c *Controller) writeCache(ctx context.Context, services []string) {
	content := strings.Join(services, "\n") + "\n"
	// #nosec G306 - cache file needs to be readable by zextras group
	if err := writeFile(c.env.FS, c.p.cacheFile, []byte(content), 0o600); err != nil {
		logger.WarnContext(ctx, "Failed to write service cache", "error", err)
	}
}

// writeFile writes data to name like os.WriteFile (create or truncate, no
// atomic rename), through the filesystem boundary.
func writeFile(fsys sys.FS, name string, data []byte, perm fs.FileMode) error {
	f, err := fsys.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err //nolint:wrapcheck // same error os.WriteFile returns
	}

	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	return err //nolint:wrapcheck // same error os.WriteFile returns
}
