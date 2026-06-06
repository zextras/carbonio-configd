// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - upstream server discovery
package proxy

import (
	"bufio"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	errGetAllServers                   = "failed to get all servers: %w"
	serverNamePrefix                   = "# name "
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
)

// MemcacheServer represents a memcached server
type MemcacheServer struct {
	Hostname string // zimbraServiceHostname
	Port     int    // zimbraMemcachedBindPort
}

// serverData holds server attributes during parsing
type serverData struct {
	hostname     string
	lookupTarget bool
	mailMode     string
	mailPort     int
	mailSSLPort  int
}

// mcServerData holds memcached server attributes during parsing.
type mcServerData struct {
	hostname      string
	hasMemcached  bool
	memcachedPort int
}

// applyServerAttr updates cur with a parsed key/value attribute pair.
func applyServerAttr(key, value string, cur *serverData) {
	switch key {
	case zimbraServiceHostnameAttr:
		cur.hostname = value
	case zimbraReverseProxyLookupTargetAttr:
		cur.lookupTarget = strings.EqualFold(value, "TRUE")
	case zimbraMailModeAttr:
		cur.mailMode = strings.ToLower(value)
	case zimbraMailPortAttr:
		if port, err := strconv.Atoi(value); err == nil {
			cur.mailPort = port
		}
	case zimbraMailSSLPortAttr:
		if port, err := strconv.Atoi(value); err == nil {
			cur.mailSSLPort = port
		}
	}
}

// appendValidUpstream appends an UpstreamServer if cur qualifies as a proxy backend.
func appendValidUpstream(servers *[]UpstreamServer, cur serverData, portSelector func(serverData) UpstreamServer) {
	if cur.hostname == "" || !cur.lookupTarget {
		return
	}

	s := portSelector(cur)
	if s.Port > 0 {
		*servers = append(*servers, s)
	}
}

// applyMcAttr updates cur with a parsed key/value attribute pair.
func applyMcAttr(key, value string, cur *mcServerData) {
	switch key {
	case zimbraServiceHostnameAttr:
		cur.hostname = value
	case zimbraServiceEnabledAttr:
		if value == "memcached" {
			cur.hasMemcached = true
		}
	case zimbraMemcachedBindPortAttr:
		if port, err := strconv.Atoi(value); err == nil {
			cur.memcachedPort = port
		}
	}
}

// appendValidMcServer appends a MemcacheServer if cur has memcached enabled.
func appendValidMcServer(servers *[]MemcacheServer, cur mcServerData) {
	if cur.hostname != "" && cur.hasMemcached && cur.memcachedPort > 0 {
		*servers = append(*servers, MemcacheServer{Hostname: cur.hostname, Port: cur.memcachedPort})
	}
}

// getAllReverseProxyBackends queries all servers that should be reverse proxy backends.
// Returns upstream servers that have zimbraReverseProxyLookupTarget=TRUE
// Results are cached to avoid repeated expensive LDAP calls.
func (g *Generator) getAllReverseProxyBackends(ctx context.Context) ([]UpstreamServer, error) {
	return g.getAllReverseProxyBackendsBy(ctx, false)
}

// parseReverseProxyBackends parses zmprov gas output to find servers with zimbraReverseProxyLookupTarget=TRUE
//
// This function parses the zmprov -l gas output format:
// # name server1.example.com
// zimbraServiceHostname: server1.example.com
// zimbraReverseProxyLookupTarget: TRUE
// zimbraMailMode: http
// zimbraMailPort: 8080
// zimbraMailSSLPort: 8443
//
// Expected attributes:
// - zimbraReverseProxyLookupTarget: TRUE/FALSE
// - zimbraMailMode: http, https, mixed, both
// - zimbraMailPort: integer (default 80)
// - zimbraMailSSLPort: integer (default 443)
// - zimbraServiceHostname: string
func (g *Generator) parseReverseProxyBackends(output string) []UpstreamServer {
	return g.parseReverseProxyBackendsGeneric(output, g.buildUpstreamServer)
}

// parseReverseProxyBackendsSSL parses zmprov gas output for SSL upstream servers
func (g *Generator) parseReverseProxyBackendsSSL(output string) []UpstreamServer {
	return g.parseReverseProxyBackendsGeneric(output, g.buildUpstreamServerSSL)
}

// parseReverseProxyBackendsGeneric is a generic helper for parsing reverse proxy backends.
// It accepts a port selector function to determine which port to use for each server.
func (g *Generator) parseReverseProxyBackendsGeneric(
	output string,
	portSelector func(serverData) UpstreamServer) []UpstreamServer {
	var servers []UpstreamServer

	scanner := bufio.NewScanner(strings.NewReader(output))
	current := serverData{mailPort: 80, mailSSLPort: 443}

	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			appendValidUpstream(&servers, current, portSelector)
			current = serverData{mailPort: 80, mailSSLPort: 443}

			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			applyServerAttr(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), &current)
		}
	}

	if err := scanner.Err(); err != nil {
		logger.ErrorContext(context.Background(), "Error scanning reverse proxy backends",
			"error", err)
	}

	appendValidUpstream(&servers, current, portSelector)

	return servers
}

// buildUpstreamServer builds an UpstreamServer based on mail mode
// Logic from Jython:
// - If mailMode is http, mixed, or both -> use zimbraMailPort
// - Otherwise -> use zimbraMailSSLPort
func (g *Generator) buildUpstreamServer(data serverData) UpstreamServer {
	server := UpstreamServer{
		Host: data.hostname,
	}

	// Determine port based on mail mode
	switch data.mailMode {
	case mailModeHTTP, mailModeMixed, ipModeBoth:
		server.Port = data.mailPort
	default:
		// For "https", "redirect", or any other mode, use SSL port
		server.Port = data.mailSSLPort
	}

	return server
}

// buildUpstreamServerSSL builds an UpstreamServer for SSL upstreams
// Always uses zimbraMailSSLPort (Java behavior from ProxyConfVar.java:304)
// This matches the Java implementation which uses zimbraReverseProxyHttpSSLPortAttribute
func (g *Generator) buildUpstreamServerSSL(data serverData) UpstreamServer {
	return UpstreamServer{
		Host: data.hostname,
		Port: data.mailSSLPort,
	}
}

// getAllReverseProxyBackendsSSL queries all servers that should be SSL reverse proxy backends.
// Results are cached to avoid repeated expensive LDAP calls.
func (g *Generator) getAllReverseProxyBackendsSSL(ctx context.Context) ([]UpstreamServer, error) {
	return g.getAllReverseProxyBackendsBy(ctx, true)
}

// getAllReverseProxyBackendsBy is the shared implementation for backend discovery.
// When ssl=true, it uses SSL ports and cache fields; otherwise plain HTTP.
func (g *Generator) getAllReverseProxyBackendsBy(ctx context.Context, ssl bool) ([]UpstreamServer, error) {
	label, fallbackPort, parser, cached := g.backendsByParams(ssl)

	if g.upstreamCache != nil && g.upstreamCache.populated && cached != nil {
		logger.DebugContext(ctx, "Using cached "+label+"reverse proxy backends",
			"server_count", len(*cached))

		return *cached, nil
	}

	logger.DebugContext(ctx, "Querying all "+label+"reverse proxy backend servers (cache miss)")

	outputStr, err := g.getOrCacheServersOutput()
	if err != nil {
		return nil, fmt.Errorf(errGetAllServers, err)
	}

	servers := parser(outputStr)

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
	label string, fallbackPort int, parser func(string) []UpstreamServer, cached *[]UpstreamServer,
) {
	if ssl {
		label = "SSL "
		fallbackPort = 8443
		parser = g.parseReverseProxyBackendsSSL

		if g.upstreamCache != nil {
			cached = &g.upstreamCache.reverseProxyBackendsSSL
		}
	} else {
		label = ""
		fallbackPort = 8080
		parser = g.parseReverseProxyBackends

		if g.upstreamCache != nil {
			cached = &g.upstreamCache.reverseProxyBackends
		}
	}

	return label, fallbackPort, parser, cached
}

// getOrCacheServersOutput returns the cached gas output, fetching it from LDAP when missing.
func (g *Generator) getOrCacheServersOutput() (string, error) {
	if g.upstreamCache != nil && g.upstreamCache.gasOutput != "" {
		return g.upstreamCache.gasOutput, nil
	}

	outputStr, err := g.getAllServersOutput()
	if err != nil {
		return "", err
	}

	if g.upstreamCache != nil {
		g.upstreamCache.gasOutput = outputStr
	}

	return outputStr, nil
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

	// Get all servers with attributes using native LDAP client
	outputStr, err := g.getAllServersOutput()
	if err != nil {
		return nil, fmt.Errorf(errGetAllServers, err)
	}

	servers := g.parseMemcachedServers(outputStr)

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

// parseMemcachedServers parses zmprov gas output to find servers with memcached service enabled.
func (g *Generator) parseMemcachedServers(output string) []MemcacheServer {
	var servers []MemcacheServer

	scanner := bufio.NewScanner(strings.NewReader(output))
	current := mcServerData{memcachedPort: 11211}

	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			appendValidMcServer(&servers, current)
			current = mcServerData{memcachedPort: 11211}

			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			applyMcAttr(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), &current)
		}
	}

	if err := scanner.Err(); err != nil {
		logger.ErrorContext(context.Background(), "Error scanning memcached servers",
			"error", err)
	}

	appendValidMcServer(&servers, current)

	return servers
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

// getAllServersOutput retrieves all servers with attributes using the native LDAP client
// and formats the output to match zmprov -l gas -v format for backward compatibility.
// This allows reusing existing parsing logic while eliminating subprocess overhead.
func (g *Generator) getAllServersOutput() (string, error) {
	// Check if native LDAP client is available
	if g.LdapClient == nil || g.LdapClient.NativeClient == nil {
		return "", fmt.Errorf("native LDAP client not initialized")
	}

	// Get all servers with attributes
	serversMap, err := g.LdapClient.NativeClient.GetAllServersWithAttributes()
	if err != nil {
		return "", fmt.Errorf("failed to query LDAP servers: %w", err)
	}

	// Convert map structure to zmprov gas -v output format
	// Format:
	// # name server1.example.com
	// zimbraServiceHostname: server1.example.com
	// zimbraReverseProxyLookupTarget: TRUE
	// ...
	var builder strings.Builder

	serverNames := make([]string, 0, len(serversMap))
	for serverName := range serversMap {
		serverNames = append(serverNames, serverName)
	}

	slices.Sort(serverNames)

	for _, serverName := range serverNames {
		attrs := serversMap[serverName]

		builder.WriteString(serverNamePrefix)
		builder.WriteString(serverName)
		builder.WriteString("\n")

		attrKeys := make([]string, 0, len(attrs))
		for key := range attrs {
			if key == "cn" {
				continue
			}

			attrKeys = append(attrKeys, key)
		}

		slices.Sort(attrKeys)

		for _, key := range attrKeys {
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(attrs[key])
			builder.WriteString("\n")
		}

		builder.WriteString("\n")
	}

	return builder.String(), nil
}
