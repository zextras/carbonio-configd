// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - upstream server discovery
package proxy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	errGetAllServers                   = "failed to get all servers: %w"
	zimbraServiceHostnameAttr          = "zimbraServiceHostname"
	zimbraReverseProxyLookupTargetAttr = "zimbraReverseProxyLookupTarget"
	zimbraMailModeAttr                 = "zimbraMailMode"
	zimbraMailPortAttr                 = "zimbraMailPort"
	zimbraMailSSLPortAttr              = "zimbraMailSSLPort"
	zimbraServiceEnabledAttr           = "zimbraServiceEnabled"
	zimbraMemcachedBindPortAttr        = "zimbraMemcachedBindPort"
	localhostName                      = "localhost"
	mailModeHTTP                       = "http"
	mailModeMixed                      = "mixed"
	mailModeHTTPS                      = "https"
	mailModeRedirect                   = "redirect"
	memcachedService                   = "memcached"
	defaultMailPort                    = 80
	defaultMailSSLPort                 = 443
	defaultMemcachedPort               = 11211
)

// errNativeLDAPClientNotInitialized is returned when a native-LDAP-backed
// lookup is attempted without a configured LdapClient/NativeClient.
var errNativeLDAPClientNotInitialized = errors.New("native LDAP client not initialized")

// MemcacheServer represents a memcached server
type MemcacheServer struct {
	Hostname string // zimbraServiceHostname
	Port     int    // zimbraMemcachedBindPort
}

// buildReverseProxyBackends filters byHost (as returned by
// getServerAttrsByHostname) down to servers that qualify as reverse proxy
// backends (non-empty zimbraServiceHostname and
// zimbraReverseProxyLookupTarget=TRUE), building an UpstreamServer per
// qualifying server via buildFn. Results are sorted by hostname.
func buildReverseProxyBackends(
	byHost map[string]map[string]string,
	buildFn func(hostname string, attrs map[string]string) UpstreamServer,
) []UpstreamServer {
	var servers []UpstreamServer

	for _, attrs := range byHost {
		hostname := attrs[zimbraServiceHostnameAttr]
		if hostname == "" || !strings.EqualFold(attrs[zimbraReverseProxyLookupTargetAttr], "TRUE") {
			continue
		}

		if s := buildFn(hostname, attrs); s.Port > 0 {
			servers = append(servers, s)
		}
	}

	slices.SortFunc(servers, func(a, b UpstreamServer) int { return strings.Compare(a.Host, b.Host) })

	return servers
}

// buildUpstreamServer builds an UpstreamServer for hostname based on mail mode.
// Logic from Jython:
// - If mailMode is http, mixed, or both -> use zimbraMailPort
// - Otherwise -> use zimbraMailSSLPort
func (g *Generator) buildUpstreamServer(hostname string, attrs map[string]string) UpstreamServer {
	server := UpstreamServer{Host: hostname}

	switch strings.ToLower(attrs[zimbraMailModeAttr]) {
	case mailModeHTTP, mailModeMixed, ipModeBoth:
		server.Port = atoiOr(attrs[zimbraMailPortAttr], defaultMailPort)
	default:
		// For "https", "redirect", or any other mode, use SSL port
		server.Port = atoiOr(attrs[zimbraMailSSLPortAttr], defaultMailSSLPort)
	}

	return server
}

// buildUpstreamServerSSL builds an UpstreamServer for SSL upstreams.
// Always uses zimbraMailSSLPort (Java behavior from ProxyConfVar.java:304)
// This matches the Java implementation which uses zimbraReverseProxyHttpSSLPortAttribute
func (g *Generator) buildUpstreamServerSSL(hostname string, attrs map[string]string) UpstreamServer {
	return UpstreamServer{
		Host: hostname,
		Port: atoiOr(attrs[zimbraMailSSLPortAttr], defaultMailSSLPort),
	}
}

// getAllReverseProxyBackends queries all servers that should be reverse proxy backends.
// Returns upstream servers that have zimbraReverseProxyLookupTarget=TRUE
// Results are cached to avoid repeated expensive LDAP calls.
func (g *Generator) getAllReverseProxyBackends(ctx context.Context) ([]UpstreamServer, error) {
	return g.getAllReverseProxyBackendsBy(ctx, false)
}

// getAllReverseProxyBackendsSSL queries all servers that should be SSL reverse proxy backends.
// Results are cached to avoid repeated expensive LDAP calls.
func (g *Generator) getAllReverseProxyBackendsSSL(ctx context.Context) ([]UpstreamServer, error) {
	return g.getAllReverseProxyBackendsBy(ctx, true)
}

// getAllReverseProxyBackendsBy is the shared implementation for backend discovery.
// When ssl=true, it uses SSL ports and cache fields; otherwise plain HTTP.
func (g *Generator) getAllReverseProxyBackendsBy(ctx context.Context, ssl bool) ([]UpstreamServer, error) {
	label, fallbackPort, buildFn, cached := g.backendsByParams(ssl)

	if g.upstreamCache != nil && g.upstreamCache.populated && cached != nil {
		logger.DebugContext(ctx, "Using cached "+label+"reverse proxy backends",
			"server_count", len(*cached))

		return *cached, nil
	}

	logger.DebugContext(ctx, "Querying all "+label+"reverse proxy backend servers (cache miss)")

	if g.upstreamCache == nil || g.upstreamCache.serverAttrsByHost == nil {
		if g.LdapClient == nil || g.LdapClient.NativeClient == nil {
			return nil, fmt.Errorf(errGetAllServers, errNativeLDAPClientNotInitialized)
		}
	}

	byHost, err := g.getServerAttrsByHostname(ctx)
	if err != nil {
		return nil, fmt.Errorf(errGetAllServers, err)
	}

	servers := buildReverseProxyBackends(byHost, buildFn)

	logger.DebugContext(ctx, "Found "+label+"reverse proxy backend servers",
		"server_count", len(servers))

	if g.upstreamCache != nil {
		if cached != nil {
			*cached = servers
		}

		g.upstreamCache.populated = true
	}

	if len(servers) == 0 {
		logger.WarnContext(ctx, "No "+label+"reverse proxy backends found, using fallback",
			"fallback_port", fallbackPort)

		return []UpstreamServer{{Host: localhostName, Port: fallbackPort}}, nil
	}

	return servers, nil
}

// backendsByParams returns the parameters that vary between SSL and non-SSL backend lookups.
func (g *Generator) backendsByParams(ssl bool) (
	label string, fallbackPort int, buildFn func(string, map[string]string) UpstreamServer, cached *[]UpstreamServer,
) {
	if ssl {
		label = "SSL "
		fallbackPort = 8443
		buildFn = g.buildUpstreamServerSSL

		if g.upstreamCache != nil {
			cached = &g.upstreamCache.reverseProxyBackendsSSL
		}
	} else {
		label = ""
		fallbackPort = 8080
		buildFn = g.buildUpstreamServer

		if g.upstreamCache != nil {
			cached = &g.upstreamCache.reverseProxyBackends
		}
	}

	return label, fallbackPort, buildFn, cached
}

// buildMemcachedServers filters byHost (as returned by
// getServerAttrsByHostname) down to servers that have the memcached service
// enabled (zimbraServiceEnabled contains "memcached") and a non-empty
// zimbraServiceHostname. Results are sorted by hostname.
func buildMemcachedServers(byHost map[string]map[string]string) []MemcacheServer {
	var servers []MemcacheServer

	for _, attrs := range byHost {
		hostname := attrs[zimbraServiceHostnameAttr]
		if hostname == "" || !serverEnablesAnyService(attrs, []string{memcachedService}) {
			continue
		}

		if port := atoiOr(attrs[zimbraMemcachedBindPortAttr], defaultMemcachedPort); port > 0 {
			servers = append(servers, MemcacheServer{Hostname: hostname, Port: port})
		}
	}

	slices.SortFunc(servers, func(a, b MemcacheServer) int { return strings.Compare(a.Hostname, b.Hostname) })

	return servers
}

// getAllMemcachedServers queries all memcached servers
// This is equivalent to the Jython gamcs() function
// Results are cached to avoid repeated expensive LDAP calls.
func (g *Generator) getAllMemcachedServers(ctx context.Context) ([]MemcacheServer, error) {
	// Check cache first
	if g.upstreamCache != nil && g.upstreamCache.populated {
		logger.DebugContext(ctx, "Using cached memcached servers",
			"server_count", len(g.upstreamCache.memcachedServers))

		return g.upstreamCache.memcachedServers, nil
	}

	logger.DebugContext(ctx, "Querying all memcached servers (cache miss)")

	if g.upstreamCache == nil || g.upstreamCache.serverAttrsByHost == nil {
		if g.LdapClient == nil || g.LdapClient.NativeClient == nil {
			return nil, fmt.Errorf(errGetAllServers, errNativeLDAPClientNotInitialized)
		}
	}

	byHost, err := g.getServerAttrsByHostname(ctx)
	if err != nil {
		return nil, fmt.Errorf(errGetAllServers, err)
	}

	servers := buildMemcachedServers(byHost)

	logger.DebugContext(ctx, "Found memcached servers",
		"server_count", len(servers))

	// Cache the result
	if g.upstreamCache != nil {
		g.upstreamCache.memcachedServers = servers
		// Mark cache as populated after first query
		g.upstreamCache.populated = true
	}

	return servers, nil
}

// formatMemcacheServers formats memcache servers for nginx config
// Returns properly formatted server directive lines with indentation
func formatMemcacheServers(servers []MemcacheServer) string {
	if len(servers) == 0 {
		return ""
	}

	lines := make([]string, 0, len(servers))
	for _, s := range servers {
		lines = append(lines, fmt.Sprintf("  servers   %s:%d;", s.Hostname, s.Port))
	}

	return strings.Join(lines, "\n")
}
