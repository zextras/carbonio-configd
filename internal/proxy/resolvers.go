// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - custom variable resolvers
package proxy

import (
	"context"
	"os"
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
	nginxReturn307Path = "return 307 /static/login/"
	nginxOff           = "off"
	sslDHParamKey      = "ssl_dhparam"
	sslDHParamPath     = "/opt/zextras/conf/dhparam.pem"
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

// resolveClientCertCADefault returns the default client CA certificate path if it exists
func (g *Generator) resolveClientCertCADefault(ctx context.Context) (any, error) {
	caPath := g.ConfDir + "/nginx.client.ca.crt"
	if _, err := os.Stat(caPath); err == nil {
		return caPath, nil
	}

	return ":empty:", nil // Special value indicating file doesn't exist
}

// resolveDHParamEnabled returns the keyword if DH parameters file exists
func (g *Generator) resolveDHParamEnabled(ctx context.Context) (any, error) {
	dhPath := g.ConfDir + "/dhparam.pem"
	if _, err := os.Stat(dhPath); err == nil {
		return sslDHParamKey, nil // Return the keyword to enable
	}

	return "", nil
}

// resolveMailWhitelistIPs renders zimbraReverseProxyIPThrottleWhitelist as nginx
// mail_whitelist_ip directives, one per IP, matching legacy ServersVar formatting.
func (g *Generator) resolveMailWhitelistIPs(_ context.Context) (any, error) {
	raw, ok := g.getConfigValue("zimbraReverseProxyIPThrottleWhitelist", sourceServer, sourceGlobal)
	if !ok || raw == "" {
		return "", nil
	}

	ips := strings.Fields(strings.ReplaceAll(raw, "\n", " "))
	if len(ips) == 0 {
		return "", nil
	}

	var sb strings.Builder

	for i, ip := range ips {
		if i == 0 {
			sb.WriteString("mail_whitelist_ip    ")
		} else {
			sb.WriteString("    mail_whitelist_ip    ")
		}

		sb.WriteString(ip)
		sb.WriteString(";\n")
	}

	return sb.String(), nil
}

// resolveWebAvailable returns true when at least one webclient upstream server is configured.
// Mirrors legacy ZMWebAvailableVar (ProxyConfGen.java:1702).
func (g *Generator) resolveWebAvailable(ctx context.Context) (any, error) {
	servers, err := g.getAllReverseProxyBackends(ctx)
	if err != nil {
		logger.WarnContext(ctx, resolverQueryFailed, "error", err)
		return false, nil //nolint:nilerr // test environments without zmprov return empty
	}

	return len(servers) > 0, nil
}

// resolveLookupAvailable returns true when at least one lookup handler is configured.
// Mirrors legacy ZMLookupAvailableVar (ProxyConfGen.java:1701).
func (g *Generator) resolveLookupAvailable(_ context.Context) (any, error) {
	val, ok := g.getConfigValue("zimbraReverseProxyAvailableLookupTargets", sourceGlobal)
	if !ok {
		return false, nil
	}

	return len(strings.Fields(strings.ReplaceAll(val, "\n", " "))) > 0, nil
}

// resolveWebSSLDhparamEnabled returns true when the web SSL DH parameter file exists on disk.
// Mirrors legacy WebSSLDhparamEnablerVar (ProxyConfGen.java:1913).
func (g *Generator) resolveWebSSLDhparamEnabled(_ context.Context) (any, error) {
	dhPath := sslDHParamPath

	if g.LocalConfig != nil {
		if v, ok := g.LocalConfig.Data.Get("web.ssl.dhparam.file"); ok && v != "" {
			dhPath = v
		}
	}

	if _, err := os.Stat(dhPath); err == nil {
		return true, nil
	}

	return false, nil
}

// resolveSSLClientCertCAEnabled returns true when the client CA certificate file exists.
// Mirrors legacy ssl.clientcertca.enabled (ProxyConfGen.java:2684-2713).
func (g *Generator) resolveSSLClientCertCAEnabled(_ context.Context) (any, error) {
	caPath := g.ConfDir + "/nginx.client.ca.crt"

	info, err := os.Stat(caPath)
	if err != nil {
		return false, nil //nolint:nilerr // missing file means disabled, not an error
	}

	return info.Size() > 0, nil
}
