// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - Java-faithful upstream server resolution.
//
// This file ports the Java ProxyConfGen upstream variable behaviour exactly:
// the per-service server sources (getAllMailClientServers / getAllWebClientServers
// / getAllAdminClientServers), the attribute-list sources (EWS / login), the
// isValidUpstream gate, the server directive format, and the ServersVar
// indentation. See carbonio-proxy ServersVar.java, ProxyConfVar.java
// (isValidUpstream / generateServerDirective) and the *UpstreamServersVar.java
// subclasses.
package proxy

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// Service constants mirror Provisioning.SERVICE_* in carbonio-mailbox.
const (
	serviceMailbox     = "mailbox"     // SERVICE_MAILBOX
	serviceMailclient  = "service"     // SERVICE_MAILCLIENT
	serviceWebclient   = "zimbra"      // SERVICE_WEBCLIENT
	serviceAdminclient = "zimbraAdmin" // SERVICE_ADMINCLIENT
)

// ZX upstream ports are hardcoded in Java (ProxyConfGen.ZIMBRA_UPSTREAM_ZX_PORT).
const (
	zxUpstreamPort    = 8742
	zxUpstreamSSLPort = 8743
)

// Server attributes and defaults used when generating directives.
const (
	attrMailProxyReconnectTimeout = "zimbraMailProxyReconnectTimeout"
	attrMailProxyMaxFails         = "zimbraMailProxyMaxFails"
	defaultReconnectTimeout       = 60
	defaultMaxFails               = 1
)

// Port-attribute config keys and their Java defaults
// (ZAttrConfig.getReverseProxy*PortAttribute).
const (
	attrUpstreamEwsServers   = "zimbraReverseProxyUpstreamEwsServers"
	attrUpstreamLoginServers = "zimbraReverseProxyUpstreamLoginServers"
	attrHTTPPortAttribute    = "zimbraReverseProxyHttpPortAttribute"
	attrHTTPSSLPortAttribute = "zimbraReverseProxyHttpSSLPortAttribute"
	attrAdminPortAttribute   = "zimbraReverseProxyAdminPortAttribute"
	defaultHTTPPortAttr      = "zimbraMailPort"
	defaultHTTPSSLPortAttr   = "zimbraMailSSLPort"
	defaultAdminPortAttr     = "zimbraAdminPort"
)

// validUpstreamMailModes is the set of zimbraMailMode values accepted by
// isValidUpstream (ProxyConfVar.isValidUpstream).
var validUpstreamMailModes = map[string]bool{
	mailModeHTTP:     true,
	mailModeHTTPS:    true,
	mailModeMixed:    true,
	ipModeBoth:       true,
	mailModeRedirect: true,
}

// upstreamSpec describes how a single nginx upstream "servers" variable is
// resolved, mirroring one Java *UpstreamServersVar subclass.
type upstreamSpec struct {
	// services lists the zimbraServiceEnabled values whose servers form the
	// candidate set (their union, deduplicated). Mutually exclusive with attrList.
	services []string
	// attrList is a multi-valued attribute (read from server/global config) that
	// explicitly lists the upstream server hostnames (EWS / login). Mutually
	// exclusive with services.
	attrList string
	// portAttrKey is the config key naming the server attribute to read the port
	// from (e.g. zimbraReverseProxyHttpPortAttribute). Mutually exclusive with fixedPort.
	portAttrKey string
	// portAttrDefault is the default server attribute name when portAttrKey is unset in config.
	portAttrDefault string
	// fixedPort, when non-zero, overrides portAttrKey and is used directly (ZX).
	fixedPort int
}

// makeUpstreamResolver returns a resolver that produces the formatted nginx
// "server ...;" directive block for the given spec, faithful to Java ProxyConfGen.
func (g *Generator) makeUpstreamResolver(spec *upstreamSpec) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		candidates, err := g.upstreamCandidates(ctx, spec)
		if err != nil {
			logger.WarnContext(ctx, resolverQueryFailed, "error", err)

			return "", nil //nolint:nilerr // Intentional: fallback to empty in degraded/test environments
		}

		directives := g.upstreamDirectives(ctx, spec, candidates)

		return formatServersVar(directives), nil
	}
}

// upstreamDirectives filters candidates by isValidUpstream and renders each into
// a server directive, resolving the port per server.
func (g *Generator) upstreamDirectives(
	ctx context.Context, spec *upstreamSpec, candidates []upstreamCandidate,
) []string {
	portAttr := g.resolveUpstreamPortAttr(spec)
	directives := make([]string, 0, len(candidates))

	for _, c := range candidates {
		if !isValidUpstream(c.attrs) {
			logger.DebugContext(ctx, "Skipping invalid upstream server",
				"server", c.hostname, "mailmode", c.attrs[zimbraMailModeAttr])

			continue
		}

		port := g.resolveServerPort(c.attrs, spec.fixedPort, portAttr)

		d := generateServerDirective(c.hostname, c.attrs, port)
		if d != "" {
			directives = append(directives, d)
		}
	}

	return directives
}

// resolveServerPort returns the port for a server: the fixed port when set,
// otherwise the server's port attribute, falling back to the global config
// value for that attribute (Zimbra Server.getIntAttr inherits global config).
func (g *Generator) resolveServerPort(attrs map[string]string, fixedPort int, portAttr string) int {
	if fixedPort != 0 {
		return fixedPort
	}

	if v := atoiOr(attrs[portAttr], 0); v > 0 {
		return v
	}

	if gv, ok := g.getConfigValue(portAttr, sourceGlobal); ok {
		return atoiOr(gv, 0)
	}

	return 0
}

// upstreamHasServers reports whether spec yields at least one valid upstream
// server. It is used by the *.disable enabler variables so that the enable/disable
// decision is always consistent with the rendered server list (same spec, same
// source, same isValidUpstream gate) — mirroring Java EwsEnablerVar/LoginEnablerVar
// which check the same upstream attribute the servers var reads.
func (g *Generator) upstreamHasServers(ctx context.Context, spec *upstreamSpec) bool {
	candidates, err := g.upstreamCandidates(ctx, spec)
	if err != nil {
		return false
	}

	return len(g.upstreamDirectives(ctx, spec, candidates)) > 0
}

// upstreamCandidate is a server under consideration for an upstream block.
type upstreamCandidate struct {
	hostname string
	attrs    map[string]string
}

// upstreamCandidates returns the candidate servers for spec, either by
// service-union or by attribute-listed hostnames, deduplicated and sorted.
func (g *Generator) upstreamCandidates(ctx context.Context, spec *upstreamSpec) ([]upstreamCandidate, error) {
	byHost, err := g.getServerAttrsByHostname(ctx)
	if err != nil {
		return nil, err
	}

	selected := make(map[string]map[string]string)

	if spec.attrList != "" {
		want := make(map[string]bool)
		for _, name := range g.attrListServerNames(spec.attrList) {
			want[name] = true
		}

		for _, attrs := range byHost {
			if host := serverHostname(attrs); want[host] {
				selected[host] = attrs
			}
		}
	} else {
		for _, attrs := range byHost {
			if serverEnablesAnyService(attrs, spec.services) {
				selected[serverHostname(attrs)] = attrs
			}
		}
	}

	hosts := make([]string, 0, len(selected))
	for h := range selected {
		hosts = append(hosts, h)
	}

	sort.Strings(hosts)

	candidates := make([]upstreamCandidate, 0, len(hosts))
	for _, h := range hosts {
		candidates = append(candidates, upstreamCandidate{hostname: h, attrs: selected[h]})
	}

	return candidates, nil
}

// getServerAttrsByHostname returns all servers indexed by zimbraServiceHostname,
// with their full attribute maps. Result is cached on the upstream cache.
func (g *Generator) getServerAttrsByHostname(_ context.Context) (map[string]map[string]string, error) {
	if g.upstreamCache != nil && g.upstreamCache.serverAttrsByHost != nil {
		return g.upstreamCache.serverAttrsByHost, nil
	}

	if g.LdapClient == nil || g.LdapClient.NativeClient == nil {
		return map[string]map[string]string{}, nil
	}

	serversMap, err := g.LdapClient.NativeClient.GetAllServersWithAttributes()
	if err != nil {
		return nil, fmt.Errorf("failed to query LDAP servers: %w", err)
	}

	byHost := make(map[string]map[string]string, len(serversMap))

	for cn, attrs := range serversMap {
		host := attrs[zimbraServiceHostnameAttr]
		if host == "" {
			host = cn
		}

		byHost[host] = attrs
	}

	if g.upstreamCache != nil {
		g.upstreamCache.serverAttrsByHost = byHost
	}

	return byHost, nil
}

// attrListServerNames reads a multi-valued attribute (e.g.
// zimbraReverseProxyUpstreamEwsServers) listing upstream server hostnames,
// checking server config first then global config, matching Java serverSource.
func (g *Generator) attrListServerNames(attr string) []string {
	value, ok := g.getConfigValue(attr, sourceServer, sourceGlobal)
	if !ok || value == "" {
		return nil
	}

	var names []string

	for line := range strings.SplitSeq(value, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}

	return names
}

// resolveUpstreamPortAttr returns the server attribute name to read the port
// from for spec, honouring the configurable port-attribute keys with Java defaults.
func (g *Generator) resolveUpstreamPortAttr(spec *upstreamSpec) string {
	if spec.fixedPort != 0 || spec.portAttrKey == "" {
		return ""
	}

	if v, ok := g.getConfigValue(spec.portAttrKey, sourceGlobal); ok && v != "" {
		return v
	}

	return spec.portAttrDefault
}

// serverHostname returns the server's zimbraServiceHostname.
func serverHostname(attrs map[string]string) string {
	return attrs[zimbraServiceHostnameAttr]
}

// serverEnablesAnyService reports whether the server's zimbraServiceEnabled
// (multi-valued, newline-joined) contains any of the given services exactly.
func serverEnablesAnyService(attrs map[string]string, services []string) bool {
	enabled := attrs[zimbraServiceEnabledAttr]
	for v := range strings.SplitSeq(enabled, "\n") {
		val := strings.TrimSpace(v)
		if slices.Contains(services, val) {
			return true
		}
	}

	return false
}

// isValidUpstream mirrors ProxyConfVar.isValidUpstream: the server must have
// zimbraReverseProxyLookupTarget=TRUE and a zimbraMailMode in the accepted set.
func isValidUpstream(attrs map[string]string) bool {
	if !strings.EqualFold(strings.TrimSpace(attrs[zimbraReverseProxyLookupTargetAttr]), "TRUE") {
		return false
	}

	mode := strings.ToLower(strings.TrimSpace(attrs[zimbraMailModeAttr]))

	return validUpstreamMailModes[mode]
}

// generateServerDirective mirrors ProxyConfVar.generateServerDirective. It
// returns "hostname:port fail_timeout=Ns[ max_fails=M]" or "" if no valid port.
func generateServerDirective(hostname string, attrs map[string]string, port int) string {
	if port <= 0 {
		return ""
	}

	timeout := atoiOr(attrs[attrMailProxyReconnectTimeout], defaultReconnectTimeout)
	maxFails := atoiOr(attrs[attrMailProxyMaxFails], defaultMaxFails)

	if maxFails != defaultMaxFails {
		return fmt.Sprintf("%s:%d fail_timeout=%ds max_fails=%d", hostname, port, timeout, maxFails)
	}

	return fmt.Sprintf("%s:%d fail_timeout=%ds", hostname, port, timeout)
}

// formatServersVar mirrors Java ServersVar.format: first directive has no
// leading indent, subsequent directives are indented with 8 spaces; each line
// is "server    <directive>;" and the block ends with a trailing newline.
func formatServersVar(directives []string) string {
	if len(directives) == 0 {
		return ""
	}

	var sb strings.Builder

	for i, d := range directives {
		if i == 0 {
			fmt.Fprintf(&sb, "server    %s;\n", d)
		} else {
			fmt.Fprintf(&sb, "        server    %s;\n", d)
		}
	}

	return sb.String()
}

// atoiOr parses s as an int, returning def on any error.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}

	return def
}
