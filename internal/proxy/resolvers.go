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
		var data map[string]string

		switch src {
		case sourceGlobal:
			if g.GlobalConfig != nil {
				data = g.GlobalConfig.Data
			}
		case sourceServer:
			if g.ServerConfig != nil {
				data = g.ServerConfig.Data
			}
		case sourceLocal:
			if g.LocalConfig != nil {
				data = g.LocalConfig.Data
			}
		}

		if v, ok := data[key]; ok && v != "" {
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
		if mode, ok := g.GlobalConfig.Data["zimbraIPMode"]; ok {
			return strings.ToLower(mode)
		}
	}

	// Fall back to LocalConfig for compatibility
	if g.LocalConfig != nil {
		if mode, ok := g.LocalConfig.Data["zimbraIPMode"]; ok {
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
		return "ssl_dhparam", nil // Return the keyword to enable
	}

	return "", nil
}

// makeBackendResolver creates a resolver that returns formatted upstream servers
// from getAllReverseProxyBackends (ssl=false) or getAllReverseProxyBackendsSSL (ssl=true).
func (g *Generator) makeBackendResolver(ssl bool) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		var (
			servers []UpstreamServer
			err     error
		)

		if ssl {
			servers, err = g.getAllReverseProxyBackendsSSL(ctx)
		} else {
			servers, err = g.getAllReverseProxyBackends(ctx)
		}

		if err != nil {
			logger.WarnContext(ctx, resolverQueryFailed, "error", err)
			return "", nil //nolint:nilerr // Intentional: fallback for test environments without zmprov
		}

		return formatUpstreamServers(servers), nil
	}
}

// makeAttributeResolver creates a resolver that returns formatted upstream servers
// from a specific LDAP attribute (e.g., zimbraReverseProxyUpstreamEwsServers).
func (g *Generator) makeAttributeResolver(attr string, ssl bool) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		var (
			servers []UpstreamServer
			err     error
		)

		if ssl {
			servers, err = g.getUpstreamServersByAttributeSSL(ctx, attr)
		} else {
			servers, err = g.getUpstreamServersByAttribute(ctx, attr)
		}

		if err != nil {
			logger.WarnContext(ctx, resolverQueryFailed, "error", err)
			return "", nil //nolint:nilerr // Intentional: fallback for test environments without zmprov
		}

		return formatUpstreamServers(servers), nil
	}
}
