// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
)

var cacheFile = logPath + "/.zmcontrol.cache"

// legacyServiceNames are service names from LDAP that should be ignored.
var legacyServiceNames = map[string]bool{
	"zimlet":      true,
	"zimbraAdmin": true,
	"zimbra":      true,
}

// DiscoverEnabledServices queries LDAP for services enabled on this host.
// Falls back to cache if LDAP is unreachable.
func DiscoverEnabledServices(ctx context.Context) ([]string, error) {
	ctx = logger.ContextWithComponent(ctx, "discovery")

	lc, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load localconfig: %w", err)
	}

	hostname := lc["zimbra_server_hostname"]
	if hostname == "" {
		return nil, fmt.Errorf("zimbra_server_hostname not set in localconfig")
	}

	startTLS := lc["ldap_starttls_supported"] != "0" && lc["ldap_starttls_supported"] != ""

	var tlsConfig *tls.Config
	if startTLS {
		tlsConfig = carbonioCATLSConfig()
	}

	client, err := carboldap.NewClient(&carboldap.ClientConfig{
		URL:       lc["ldap_master_url"],
		BindDN:    lc["zimbra_ldap_userdn"],
		Password:  lc["zimbra_ldap_password"],
		StartTLS:  startTLS,
		TLSConfig: tlsConfig,
	})
	if err != nil {
		logger.WarnContext(ctx, "LDAP connect failed, trying cache", "error", err)

		return readCache(ctx)
	}

	defer func() { _ = client.Close() }()

	raw, err := client.GetEnabledServices(hostname)
	if err != nil {
		logger.WarnContext(ctx, "LDAP query failed, trying cache", "error", err)

		return readCache(ctx)
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
		if s == "zmconfigd" || s == "configd" {
			hasConfigd = true

			break
		}
	}

	if !hasConfigd {
		services = append(services, "zmconfigd")
	}

	writeCache(ctx, services)

	logger.InfoContext(ctx, "Discovered enabled services", "count", len(services), "services", services)

	return services, nil
}

// IsLDAPLocal returns true if the LDAP URL contains the local hostname.
func IsLDAPLocal() bool {
	lc, err := loadConfig()
	if err != nil {
		return false
	}

	hostname := lc["zimbra_server_hostname"]
	ldapURL := lc["ldap_url"]

	return strings.Contains(ldapURL, hostname)
}

// MapLDAPServiceToRegistry maps LDAP service names to registry names.
// LDAP uses names like "directory-server", "service"; registry uses "ldap", "mailbox".
func MapLDAPServiceToRegistry(ldapName string) string {
	mapping := map[string]string{
		"directory-server": "ldap",
		"service":          "mailbox",
		"zmconfigd":        "configd",
	}

	if mapped, ok := mapping[ldapName]; ok {
		return mapped
	}

	return ldapName
}

func readCache(ctx context.Context) ([]string, error) {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, fmt.Errorf("cannot determine enabled services: LDAP unreachable and cache missing")
	}

	logger.WarnContext(ctx, "Using cached service list — may be inaccurate", "cache", cacheFile)

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

func writeCache(ctx context.Context, services []string) {
	content := strings.Join(services, "\n") + "\n"
	// #nosec G306 - cache file needs to be readable by zextras group
	if err := os.WriteFile(cacheFile, []byte(content), 0o600); err != nil {
		logger.WarnContext(ctx, "Failed to write service cache", "error", err)
	}
}

// carbonioCATLSConfig returns a TLS config that trusts Carbonio's self-signed CA.
// Falls back to system roots if the CA cert cannot be loaded.
func carbonioCATLSConfig() *tls.Config {
	caPath := confPath + "/ca/ca.pem"

	caPEM, err := os.ReadFile(caPath) //nolint:gosec // path is from internal constant
	if err != nil {
		return nil // fall back to system roots
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)

	return &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
}
