// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// ============================================================================
// Listen Addresses Resolver (Virtual IP Expansion)
// ============================================================================

// resolveListenAddresses returns listen directives for virtual IP addresses
// Queries domains and expands virtualIPAddress attributes
// Used in strict server_name enforcement to catch unknown hostnames
// Java equivalent: ListenAddressesVar.format()
func (g *Generator) resolveListenAddresses(ctx context.Context) (any, error) {
	strictServerNamePrefix := g.resolveStrictServerNamePrefix(ctx)

	logger.DebugContext(ctx, "Resolved strict server name prefix for listen addresses",
		"prefix", strictServerNamePrefix)

	addressSet, err := g.collectVirtualIPAddresses(ctx)
	if err != nil {
		return nil, err
	}

	// If no addresses found, return expanded placeholder (matches Java: addresses.isEmpty())
	if len(addressSet) == 0 {
		return strictServerNamePrefix, nil
	}

	// Get the expanded value of web.https.port. The variables are resolved in
	// map order, so web.https.port may still hold its default here: resolve it
	// first, or the listen port would depend on the iteration order.
	if v, ok := g.Variables["web.https.port"]; ok {
		if val, lerr := g.LookupValue(ctx, v); lerr == nil {
			v.Value = val
		}
	}

	httpsPort, err := g.ExpandVariable(ctx, "web.https.port")
	if err != nil {
		httpsPort = "443"
	}

	return g.formatListenDirectives(addressSet, strictServerNamePrefix, httpsPort), nil
}

// resolveStrictServerNamePrefix returns the strict server name prefix as a string,
// defaulting to "" on error or wrong type.
func (g *Generator) resolveStrictServerNamePrefix(ctx context.Context) string {
	raw, err := g.resolveStrictServerName(ctx)
	if err != nil {
		logger.WarnContext(ctx, "Could not resolve web.strict.servername, defaulting to empty",
			"error", err)

		return ""
	}

	prefix, ok := raw.(string)
	if !ok {
		logger.WarnContext(ctx, "Web.strict.servername returned non-string, defaulting to empty",
			"type", fmt.Sprintf("%T", raw))

		return ""
	}

	return prefix
}

// collectVirtualIPAddresses returns the set of virtual IP addresses from all LDAP domains.
// Without an LDAP client the set is empty (fallback handled by the caller); a
// failed query is an error.
func (g *Generator) collectVirtualIPAddresses(ctx context.Context) (map[string]bool, error) {
	if !g.ldapConfigured() {
		return map[string]bool{}, nil
	}

	domains, err := g.queryDomains(ctx)
	if err != nil {
		return nil, err
	}

	addressSet := make(map[string]bool)

	for _, domain := range domains {
		if domain.VirtualIPAddress == "" {
			continue
		}

		if !validIPLiteral(domain.VirtualIPAddress) {
			logger.WarnContext(ctx, "Skipping invalid virtual IP address",
				"domain", truncateForLog(domain.DomainName),
				"address", truncateForLog(domain.VirtualIPAddress))

			continue
		}

		addressSet[domain.VirtualIPAddress] = true
	}

	return addressSet, nil
}

// queryDomains fetches domains from LDAP, using the cache when available.
func (g *Generator) queryDomains(ctx context.Context) ([]ldap.Domain, error) {
	// Legacy loads no domains (hence no virtual IP listen addresses) unless
	// per-virtual-hostname config is enabled (ProxyConfGen.java:253-255).
	if !g.perVirtualHostnameEnabled() {
		return []ldap.Domain{}, nil
	}

	if g.Cache == nil {
		return g.queryDomainsLDAP(ctx)
	}

	cachedData, err := g.Cache.GetCachedConfig(ctx, domainCacheKey, func() (any, error) {
		return g.LdapClient.NativeClient.QueryDomains(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query domains from LDAP: %w", err)
	}

	domains, ok := cachedData.([]ldap.Domain)
	if !ok {
		return nil, fmt.Errorf("unexpected cached domains type %T", cachedData)
	}

	return domains, nil
}

func (g *Generator) queryDomainsLDAP(ctx context.Context) ([]ldap.Domain, error) {
	domains, err := g.LdapClient.NativeClient.QueryDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query domains from LDAP: %w", err)
	}

	return domains, nil
}

// listenHost brackets an IPv6 literal as nginx's listen directive requires.
func listenHost(addr string) string {
	if strings.Contains(addr, ":") && !strings.HasPrefix(addr, "[") {
		return "[" + addr + "]"
	}

	return addr
}

// formatListenDirectives formats the sorted address set into nginx listen directives.
func (g *Generator) formatListenDirectives(addressSet map[string]bool, prefix, httpsPort string) string {
	addresses := make([]string, 0, len(addressSet))
	for addr := range addressSet {
		addresses = append(addresses, addr)
	}

	slices.Sort(addresses)

	var sb strings.Builder

	for i, addr := range addresses {
		if i > 0 {
			sb.WriteString("\n")
		}

		fmt.Fprintf(&sb, "%s    listen                  %s:%s default_server;", prefix, listenHost(addr), httpsPort)
	}

	return sb.String()
}

// ============================================================================
// Lookup and Availability Resolvers
// ============================================================================

// resolveLookupTargetAvailable returns "true" if lookup target is available
// Checks if zimbraReverseProxyLookupTarget is set and valid
func (g *Generator) resolveLookupTargetAvailable(ctx context.Context) (any, error) {
	if _, ok := g.getConfigValue("zimbraReverseProxyLookupTarget", sourceGlobal); ok {
		return true, nil
	}

	return false, nil
}

// resolveWebUpstreamTargetAvailable returns "true" if web upstream targets exist
// Checks if there are any mailbox servers with HTTP service enabled
func (g *Generator) resolveWebUpstreamTargetAvailable(ctx context.Context) (any, error) {
	servers, err := g.reverseProxyBackendsRaw(ctx, false)
	if err != nil {
		return g.unavailableOffline(ctx, err)
	}

	return len(servers) > 0, nil
}

// ============================================================================
// SASL Host from IP Resolver
// ============================================================================

// resolveSaslHostFromIP returns hostname for SASL authentication from IP
// Reads zimbraReverseProxySaslHostFromIP attribute
func (g *Generator) resolveSaslHostFromIP(ctx context.Context) (any, error) {
	saslHost := nginxOff

	if val, ok := g.getConfigValue("zimbraReverseProxySaslHostFromIP", sourceGlobal); ok {
		if validNginxArg(val) {
			saslHost = val
		} else {
			logger.ErrorContext(ctx, "Ignoring unsafe SASL host-from-IP value, using default",
				"value", truncateForLog(val))
		}
	}

	return saslHost, nil
}

// ============================================================================
// Timeout Resolvers with Offset Calculation (TimeoutVar pattern)
// ============================================================================

// makeTimeoutResolver creates a resolver that reads a base timeout from LocalConfig
// and adds an offset. This matches the Java TimeoutVar pattern where nginx is configured
// to time out slightly after the backend.
func (g *Generator) makeTimeoutResolver(configKey string, defaultBase, offset int) func(context.Context) (any, error) {
	return func(_ context.Context) (any, error) {
		base := defaultBase

		if val, ok := g.getConfigValue(configKey, sourceLocal); ok {
			if parsed, err := strconv.Atoi(val); err == nil {
				base = parsed
			}
		}

		return base + offset, nil
	}
}

// resolveWebUpstreamLoginTarget resolves login upstream target URL with http:// or https://
// makeUpstreamTargetResolver creates a resolver that returns an upstream target URL
// with http:// or https:// based on zimbraReverseProxySSLToUpstreamEnabled.
// sslName is the upstream name for SSL, nonSSLName for plain HTTP.
func (g *Generator) makeUpstreamTargetResolver(sslName, nonSSLName string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		sslToUpstream := true // default is true per Java code

		if val, ok := g.getConfigValue("zimbraReverseProxySSLToUpstreamEnabled", sourceServer); ok {
			sslToUpstream = config.IsTruthy(val)
		}

		if sslToUpstream {
			return "https://" + sslName, nil
		}

		return "http://" + nonSSLName, nil
	}
}
