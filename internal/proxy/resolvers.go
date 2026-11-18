// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - custom variable resolvers
package proxy

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// configSource identifies where to look up a configuration key.
type configSource int

const (
	sourceGlobal configSource = iota
	sourceServer
	sourceLocal
)

// getConfigValue looks up key across config sources in the given order,
// returning the first non-empty value found.
func (g *Generator) getConfigValue(key string, sources ...configSource) (string, bool) {
	for _, src := range sources {
		var (
			v  string
			ok bool
		)

		switch src {
		case sourceGlobal:
			if g.GlobalConfig != nil {
				v, ok = g.GlobalConfig.Data.Get(key)
			}
		case sourceServer:
			if g.ServerConfig != nil {
				v, ok = g.ServerConfig.Data.Get(key)
			}
		case sourceLocal:
			if g.LocalConfig != nil {
				v, ok = g.LocalConfig.Data.Get(key)
			}
		}

		if ok && v != "" {
			return v, true
		}
	}

	return "", false
}

// IP mode and login page constants
const (
	ipModeBoth         = "both"
	staticLoginPath    = "/static/login/"
	nginxReturn200     = "return 200"
	nginxReturn303Path = "return 303 /static/login/"
	nginxOff           = "off"
	sslDHParamKey      = "ssl_dhparam"
)

// resolverQueryFailed is the log message emitted when a config resolver returns an error.
const resolverQueryFailed = "Resolver query failed, returning empty"

// makeIPModeResolver returns a resolver that reports whether the runtime IP mode
// matches mode (e.g. "ipv4", "ipv6", "both").
func (g *Generator) makeIPModeResolver(mode string) func(context.Context) (any, error) {
	return func(_ context.Context) (any, error) {
		return g.getIPMode() == mode, nil
	}
}

// getIPMode retrieves the IP mode from configuration
func (g *Generator) getIPMode() string {
	// Check GlobalConfig first (zimbraIPMode is a global attribute)
	if g.GlobalConfig != nil {
		if mode, ok := g.GlobalConfig.Data.Get("zimbraIPMode"); ok {
			return strings.ToLower(mode)
		}
	}

	// Fall back to LocalConfig for compatibility
	if g.LocalConfig != nil {
		if mode, ok := g.LocalConfig.Data.Get("zimbraIPMode"); ok {
			return strings.ToLower(mode)
		}
	}

	return ipModeBoth // Default to dual stack
}

// clientCertCAFilename is the nginx client CA certificate filename within ConfDir.
const clientCertCAFilename = "nginx.client.ca.crt"

// clientCertCAPath returns the full path to the client CA certificate file,
// shared by resolveClientCertCADefault and resolveSSLClientCertCAEnabled.
func (g *Generator) clientCertCAPath() string {
	return g.ConfDir + "/" + clientCertCAFilename
}

// resolveDHParamEnabled returns the keyword if DH parameters file exists
func (g *Generator) resolveDHParamEnabled(ctx context.Context) (any, error) {
	dhPath := g.ConfDir + "/dhparam.pem"
	if _, err := g.env.FS.Stat(dhPath); err == nil {
		return sslDHParamKey, nil // Return the keyword to enable
	}

	return "", nil
}

// resolveMailWhitelistIPs renders zimbraReverseProxyIPThrottleWhitelist as nginx
// mail_whitelist_ip directives, one per IP, matching legacy ServersVar formatting
// apart from indentation (continuation lines are indented by the renderer).
func (g *Generator) resolveMailWhitelistIPs(ctx context.Context) (any, error) {
	raw, ok := g.getConfigValue("zimbraReverseProxyIPThrottleWhitelist", sourceServer, sourceGlobal)
	if !ok || raw == "" {
		return "", nil
	}

	ips := strings.Fields(strings.ReplaceAll(raw, "\n", " "))
	if len(ips) == 0 {
		return "", nil
	}

	var sb strings.Builder

	for _, ip := range ips {
		// The token is spliced into a directive: "1.2.3.4;}" must not pass.
		if !validIPOrCIDR(ip) {
			logger.ErrorContext(ctx, "Skipping invalid mail whitelist address", "address", truncateForLog(ip))

			continue
		}

		sb.WriteString("mail_whitelist_ip    ")
		sb.WriteString(ip)
		sb.WriteString(";\n")
	}

	return sb.String(), nil
}

// ldapConfigured reports whether a native LDAP client is available. Without
// one (offline rendering, tests) there is no server source at all.
func (g *Generator) ldapConfigured() bool {
	return g.LdapClient != nil && g.LdapClient.NativeClient != nil
}

// unavailableOffline is the error path of the availability enablers. With LDAP
// configured a failed backend query is an error, since "unavailable" would
// comment out the upstream and nginx would reload into an outage; without LDAP
// there is nothing to query, so the backend is simply unavailable.
func (g *Generator) unavailableOffline(ctx context.Context, err error) (any, error) {
	if g.ldapConfigured() {
		return nil, fmt.Errorf("query reverse proxy backends: %w", err)
	}

	logger.WarnContext(ctx, resolverQueryFailed, "error", err)

	return false, nil
}

// resolveWebAvailable returns true when at least one webclient upstream server is configured.
// Mirrors legacy ZMWebAvailableVar (ProxyConfGen.java:1702).
func (g *Generator) resolveWebAvailable(ctx context.Context) (any, error) {
	servers, err := g.reverseProxyBackendsRaw(ctx, false)
	if err != nil {
		return g.unavailableOffline(ctx, err)
	}

	return len(servers) > 0, nil
}

// resolveLookupAvailable returns true when there is at least one lookup target.
// Mirrors legacy ZMLookupAvailableVar (ProxyConfGen.java:1701), which counted the
// handlers after the lookup-target fallback, not the raw attribute.
func (g *Generator) resolveLookupAvailable(ctx context.Context) (any, error) {
	hosts, err := g.lookupTargetHosts(ctx)
	if err != nil {
		return nil, err
	}

	return len(hosts) > 0, nil
}

// resolveWebSSLDhparamEnabled returns true when the web SSL DH parameter file exists on disk.
// Mirrors legacy WebSSLDhparamEnablerVar (ProxyConfGen.java:1913).
func (g *Generator) resolveWebSSLDhparamEnabled(_ context.Context) (any, error) {
	dhPath := g.ConfDir + "/dhparam.pem"

	if g.LocalConfig != nil {
		if v, ok := g.LocalConfig.Data.Get("web.ssl.dhparam.file"); ok && v != "" {
			dhPath = v
		}
	}

	if _, err := g.env.FS.Stat(dhPath); err == nil {
		return true, nil
	}

	return false, nil
}

// resolveSSLClientCertCAEnabled returns true when the client CA certificate file exists.
// Mirrors legacy ssl.clientcertca.enabled (ProxyConfGen.java:2684-2713).
func (g *Generator) resolveSSLClientCertCAEnabled(_ context.Context) (any, error) {
	caPath := g.clientCertCAPath()

	info, err := g.env.FS.Stat(caPath)
	if err != nil {
		return false, nil //nolint:nilerr // missing file means disabled, not an error
	}

	return info.Size() > 0, nil
}
