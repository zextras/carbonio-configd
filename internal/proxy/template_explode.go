// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// parseExplodeDirective checks whether the first line of lines is an explode directive.
// Returns the explode type, args, remaining lines, and ok=true when the directive is present.
func (tp *TemplateProcessor) parseExplodeDirective(
	lines []string,
) (explodeType, explodeArgs string, remaining []string, ok bool) {
	if len(lines) == 0 {
		return "", "", nil, false
	}

	matches := tp.explodePattern.FindStringSubmatch(lines[0])
	if matches == nil {
		return "", "", nil, false
	}

	return matches[1], matches[2], lines[1:], true
}

// processExplode handles !{explode domain(...)} and !{explode server(...)} directives.
//
// Explode directives allow templates to dynamically generate configuration blocks for multiple
// domains or servers by iterating over LDAP-queried data. The directive must be on the first line
// of the template file.
//
// Supported directive types:
//   - domain: Iterates over domains with virtual hostnames, setting vhn, vip, ssl.crt, ssl.key
//   - server: Iterates over servers with a specific service, setting server_id, server_hostname
//
// Format: !{explode <type>(<args>)}
//
// Examples:
//   - !{explode domain(vhn)}        - Generate blocks for all domains
//   - !{explode domain(vhn, sso)}   - Only domains with SSO (client cert mode != off)
//   - !{explode server(mailbox)}    - Generate blocks for servers with mailbox service
func (tp *TemplateProcessor) processExplode(ctx context.Context, explodeType,
	args string, templateLines []string, writer *bufio.Writer) error {
	switch explodeType {
	case "domain":
		return tp.processExplodeDomain(ctx, args, templateLines, writer)
	case "server":
		return tp.processExplodeServer(ctx, args, templateLines, writer)
	default:
		return fmt.Errorf("unknown explode type: %s", explodeType)
	}
}

// processExplodeDomain handles !{explode domain(vhn)} and !{explode domain(vhn, sso)} directives.
//
// This function iterates over all domains queried from LDAP (domains with non-empty virtual hostnames)
// and generates a configuration block for each domain by processing the template lines with
// domain-specific variables set.
//
// Arguments:
//   - vhn: Required. Indicates the template uses virtual hostname variable
//   - sso: Optional. Filters to only include domains with SSO enabled (clientCertMode != "off")
//
// Variables set for each domain:
//   - ${vhn}: Domain's virtual hostname (e.g., "mail.example.com")
//   - ${vip}: Domain's virtual IP address (e.g., "192.168.1.10")
//   - ${ssl.crt}: Domain's SSL certificate path (or global default)
//   - ${ssl.key}: Domain's SSL private key path (or global default)
//
// Output format:
//   - Each domain's block is separated by a blank line
//   - Variables are interpolated for each domain
//   - Original SSL defaults are restored after each domain
//
// Example template:
//
//	!{explode domain(vhn)}
//	server {
//	    server_name ${vhn};
//	    listen ${vip}:443 ssl;
//	    ssl_certificate ${ssl.crt};
//	}
//
// Generates (for 2 domains):
//
//	server {
//	    server_name mail.example.com;
//	    listen 192.168.1.10:443 ssl;
//	    ssl_certificate /ssl/example.crt;
//	}
//
//	server {
//	    server_name mail.test.com;
//	    listen 192.168.1.11:443 ssl;
//	    ssl_certificate /ssl/test.crt;
//	}
func (tp *TemplateProcessor) processExplodeDomain(
	ctx context.Context, argsStr string, templateLines []string, writer *bufio.Writer,
) error {
	// Parse arguments (e.g., "vhn" or "vhn, sso")
	argsStr = strings.TrimSpace(argsStr)
	if argsStr == "" {
		return fmt.Errorf("explode domain directive requires at least one argument")
	}

	args := strings.Split(argsStr, ",")
	for i := range args {
		args[i] = strings.TrimSpace(args[i])
		if args[i] == "" {
			return fmt.Errorf("explode domain directive has empty argument")
		}
	}

	// Get domains from generator (for now, use a mock implementation)
	// In production, this would query LDAP for all domains with virtual hostnames
	domains := tp.getDomains(ctx)

	if len(domains) == 0 {
		logger.DebugContext(ctx, "No domains found for explode directive")

		return nil
	}

	// For each domain, set vhn/vip variables and process template
	for i, domain := range domains {
		// Check if domain meets requirements specified in args
		if !tp.domainMeetsRequirements(&domain, args) {
			continue
		}

		// Set domain-specific variables
		tp.setDomainVariables(&domain)

		// Process template lines with domain variables
		if err := tp.processTemplateLines(ctx, templateLines, writer, false); err != nil {
			return err
		}

		// Add blank line between domains (except after last one)
		if i < len(domains)-1 {
			if _, err := writer.WriteString("\n"); err != nil {
				return fmt.Errorf("error writing separator: %w", err)
			}
		}

		// Clear domain-specific variables
		tp.clearDomainVariables()
	}

	return nil
}

// processExplodeServer handles !{explode server(serviceName)} directive.
//
// This function iterates over all servers with a specific service enabled (queried from LDAP)
// and generates a configuration block for each server by processing the template lines with
// server-specific variables set.
//
// Arguments:
//   - serviceName: Required. The service name to filter servers by (e.g., "mailbox", "proxy")
//
// Variables set for each server:
//   - ${server_id}: Server's unique ID
//   - ${server_hostname}: Server's fully qualified hostname
//
// Special behavior:
//   - Comment lines (starting with #) are skipped during server iteration
//   - No blank lines between server blocks (unlike domain explode)
//
// Example template:
//
//	!{explode server(mailbox)}
//	# This comment is skipped
//	server ${server_id} ${server_hostname}:7071;
//
// Generates (for 2 mailbox servers):
//
//	server server1-id mailbox1.example.com:7071;
//	server server2-id mailbox2.example.com:7071;
//
// LDAP Query: Finds all servers where zimbraServiceEnabled contains the specified service name.
func (tp *TemplateProcessor) processExplodeServer(ctx context.Context, serviceName string,
	templateLines []string, writer *bufio.Writer) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return fmt.Errorf("service name required for server explode directive")
	}

	// Get servers from generator (for now, use a mock implementation)
	// In production, this would query LDAP for all servers with the specified service
	servers := tp.getServersWithService(ctx, serviceName)

	if len(servers) == 0 {
		logger.DebugContext(ctx, "No servers found with service",
			"service", serviceName)

		return nil
	}

	// For each server, set server_id/server_hostname variables and process template
	for _, server := range servers {
		// Set server-specific variables
		tp.setServerVariables(server)

		// Process template lines with server variables (skip comment lines)
		if err := tp.processTemplateLines(ctx, templateLines, writer, true); err != nil {
			return err
		}

		// Clear server-specific variables
		tp.clearServerVariables()
	}

	return nil
}

// DomainInfo holds information about a domain for explode processing.
// This data is typically queried from LDAP using filters like:
//
//	(&(objectClass=zimbraDomain)(zimbraVirtualHostname=*))
type DomainInfo struct {
	Name             string
	VirtualHostname  string
	VirtualIPAddress string
	ClientCertMode   string
	ClientCertCA     string
	SSLCertificate   string
	SSLPrivateKey    string
}

// ServerInfo holds information about a server for explode processing.
// This data is typically queried from LDAP using filters like:
//
//	(&(objectClass=zimbraServer)(zimbraServiceEnabled=<serviceName>))
type ServerInfo struct {
	ID       string
	Hostname string
	Services []string
}

// getDomains returns list of domains for explode processing.
//
// In production, this queries LDAP for all domains with non-empty virtual hostnames:
//
//	LDAP Filter: (&(objectClass=zimbraDomain)(zimbraVirtualHostname=*))
//	Returns: zimbraDomainName, zimbraVirtualHostname, zimbraVirtualIPAddress,
//	         zimbraSSLCertificate, zimbraSSLPrivateKey, zimbraClientCertMode
//
// For testing, this function checks for an injected domainProvider function.
// cachedLDAPQuery executes an LDAP query with optional caching.
// If cache is available, the result is cached under cacheKey.
// The queryFunc performs the actual LDAP query; entityName is used in log messages.
func cachedLDAPQuery[T any](ctx context.Context, cache interface {
	GetCachedConfig(context.Context, string, func() (any, error)) (any, error)
}, cacheKey, entityName string, queryFunc func() (T, error),
) (T, error) {
	if cache != nil {
		cachedData, err := cache.GetCachedConfig(ctx, cacheKey, func() (any, error) {
			return queryFunc()
		})
		if err != nil {
			var zero T
			return zero, fmt.Errorf("failed to query %s from LDAP (cached): %w", entityName, err)
		}

		result, ok := cachedData.(T)
		if !ok {
			var zero T
			return zero, fmt.Errorf("cache returned unexpected type for %s", entityName)
		}

		return result, nil
	}

	result, err := queryFunc()
	if err != nil {
		var zero T
		return zero, fmt.Errorf("failed to query %s from LDAP: %w", entityName, err)
	}

	return result, nil
}

func (tp *TemplateProcessor) getDomains(ctx context.Context) []DomainInfo {
	domains, err := tp.loadDomains(ctx)
	if err != nil {
		if !errors.Is(err, errNoDomainSource) {
			logger.ErrorContext(ctx, "Domain query failed", "error", err)
		}

		return []DomainInfo{}
	}

	return domains
}

// errNoDomainSource means neither a test provider nor an LDAP client is available.
var errNoDomainSource = errors.New("no domain source available")

// perVirtualHostnameAttr gates all per-domain proxy configuration
// (legacy ProxyConfGen.java:253-257, server value with global fallback,
// unset treated as TRUE, see perVirtualHostnameEnabled).
const perVirtualHostnameAttr = "zimbraReverseProxyGenConfigPerVirtualHostname"

// perVirtualHostnameEnabled reports whether per-virtual-hostname configuration
// is enabled. Unset means enabled: legacy getBooleanAttr(attr, false) only
// falls back to false when the attribute is absent from the entry's inherited
// defaults, but the server inherits from globalConfig, which holds the schema
// default TRUE written at LDAP init (zimbra-attrs.xml). Only an explicit FALSE
// disables it.
func (g *Generator) perVirtualHostnameEnabled() bool {
	v, ok := g.getConfigValue(perVirtualHostnameAttr, sourceServer, sourceGlobal)

	return !ok || strings.EqualFold(v, "TRUE")
}

// loadDomains returns the sorted, sanitized domains for explode processing and
// certificate deployment. Unlike getDomains it reports query failures, so
// destructive callers (obsolete-file cleanup) can tell "no domains" from
// "could not read domains". With per-virtual-hostname config disabled it
// returns no domains, exactly like legacy loadDomainReverseProxyAttrs
// (ProxyConfGen.java:253-255).
func (tp *TemplateProcessor) loadDomains(ctx context.Context) ([]DomainInfo, error) {
	var domainInfos []DomainInfo

	// Resolve the domain source: injected provider (for testing), no LDAP
	// client available, or production LDAP query.
	switch {
	case tp.domainProvider != nil:
		domainInfos = tp.domainProvider()
	case tp.generator.LdapClient == nil || tp.generator.LdapClient.NativeClient == nil:
		logger.DebugContext(ctx, "No LDAP client available, returning empty list")

		return nil, errNoDomainSource
	case !tp.generator.perVirtualHostnameEnabled():
		logger.DebugContext(ctx, "Per-virtual-hostname configuration disabled, no domains")

		return []DomainInfo{}, nil
	default:
		domains, err := cachedLDAPQuery(ctx, tp.generator.Cache, "ldap:domains", "domains",
			func() ([]ldap.Domain, error) {
				return tp.generator.LdapClient.NativeClient.QueryDomains(ctx)
			})
		if err != nil {
			return nil, err
		}

		domainInfos = make([]DomainInfo, 0, len(domains))
		for _, domain := range domains {
			domainInfos = append(domainInfos, DomainInfo{
				Name:             domain.DomainName,
				VirtualHostname:  domain.VirtualHostname,
				VirtualIPAddress: domain.VirtualIPAddress,
				ClientCertMode:   domain.ClientCertMode,
				ClientCertCA:     domain.ClientCertCA,
				SSLCertificate:   domain.SSLCertificate,
				SSLPrivateKey:    domain.SSLPrivateKey,
			})
		}

		logger.DebugContext(ctx, "Loaded domains from LDAP",
			"count", len(domainInfos))
	}

	domainInfos = tp.dropUnsafeDomains(ctx, domainInfos)

	// Sort domains by name for deterministic output
	slices.SortStableFunc(domainInfos, func(a, b DomainInfo) int {
		return strings.Compare(a.Name, b.Name)
	})

	return domainInfos, nil
}

// dropUnsafeDomains removes domains whose LDAP-provided virtual hostname, IP
// or SSL paths could not be rendered into nginx configuration without
// changing its structure. A domain is dropped rather than repaired or given
// the default certificate, so a bad value never silently serves the wrong
// certificate. Legitimate values pass through unchanged and in order.
func (tp *TemplateProcessor) dropUnsafeDomains(ctx context.Context, domains []DomainInfo) []DomainInfo {
	return slices.DeleteFunc(domains, func(d DomainInfo) bool {
		err := validateDomainInfo(&d)
		if err == nil {
			return false
		}

		logger.ErrorContext(ctx, "Skipping domain with unsafe LDAP value",
			"domain", truncateForLog(d.Name), "error", err)

		return true
	})
}

// getServersWithService returns list of servers that have the specified service enabled.
//
// In production, this queries LDAP for all servers with the specified service:
//
//	LDAP Filter: (&(objectClass=zimbraServer)(zimbraServiceEnabled=<serviceName>))
//	Returns: zimbraId, zimbraServiceHostname
//
// For testing, this function checks for an injected serverProvider function.
func (tp *TemplateProcessor) getServersWithService(ctx context.Context, serviceName string) []ServerInfo {
	var serverInfos []ServerInfo

	// Resolve the server source: injected provider (for testing), no LDAP
	// client available, or production LDAP query.
	switch {
	case tp.serverProvider != nil:
		serverInfos = tp.serverProvider(serviceName)
	case tp.generator.LdapClient == nil || tp.generator.LdapClient.NativeClient == nil:
		logger.DebugContext(ctx, "No LDAP client available, returning empty list",
			"service", serviceName)

		return []ServerInfo{}
	default:
		cacheKey := fmt.Sprintf("ldap:servers:%s", serviceName)

		servers, err := cachedLDAPQuery(ctx, tp.generator.Cache, cacheKey, "servers",
			func() ([]ldap.Server, error) {
				return tp.generator.LdapClient.NativeClient.QueryServers(ctx, serviceName)
			})
		if err != nil {
			logger.ErrorContext(ctx, "Server query failed", "error", err, "service", serviceName)
			return []ServerInfo{}
		}

		serverInfos = make([]ServerInfo, 0, len(servers))
		for _, server := range servers {
			serverInfos = append(serverInfos, ServerInfo{
				ID:       server.ServerID,
				Hostname: server.ServiceHostname,
			})
		}

		logger.DebugContext(ctx, "Loaded servers with service from LDAP",
			"count", len(serverInfos),
			"service", serviceName)
	}

	serverInfos = slices.DeleteFunc(serverInfos, func(si ServerInfo) bool {
		if si.Hostname == "" || validHostToken(si.Hostname) {
			return false
		}

		logger.ErrorContext(ctx, "Skipping server with unsafe hostname",
			"service", serviceName, "hostname", truncateForLog(si.Hostname))

		return true
	})

	// Sort servers by hostname, then by ID for deterministic output
	slices.SortFunc(serverInfos, func(a, b ServerInfo) int {
		if cmp := strings.Compare(a.Hostname, b.Hostname); cmp != 0 {
			return cmp
		}

		return strings.Compare(a.ID, b.ID)
	})

	return serverInfos
}

// domainMeetsRequirements checks if domain meets the requirements specified in explode args
func (tp *TemplateProcessor) domainMeetsRequirements(domain *DomainInfo, args []string) bool {
	for _, arg := range args {
		switch arg {
		case "vhn":
			// Virtual hostname must not be empty
			if domain.VirtualHostname == "" {
				return false
			}
		case "sso":
			// Client cert mode must not be empty or "off"
			if domain.ClientCertMode == "" || domain.ClientCertMode == nginxOff {
				return false
			}
		}
	}

	return true
}

// setDomainVariables sets domain-specific variables for template processing.
//
// Sets the following variables:
//   - vhn: Domain's virtual hostname
//   - vip: Domain's virtual IP address
//   - ssl.crt: Domain's SSL certificate (if specified, otherwise uses global default)
//   - ssl.key: Domain's SSL private key (if specified, otherwise uses global default)
//
// Original SSL values are stored in temporary variables (_orig_ssl.crt, _orig_ssl.key)
// to enable restoration after processing. This ensures each domain gets correct SSL paths
// without domains "leaking" their SSL configuration to subsequent domains.
func (tp *TemplateProcessor) setDomainVariables(domain *DomainInfo) {
	// Set variables that templates expect
	tp.generator.Variables["vhn"] = &Variable{
		Keyword:   "vhn",
		ValueType: ValueTypeString,
		Value:     domain.VirtualHostname,
	}
	tp.generator.Variables["vip"] = &Variable{
		Keyword:   "vip",
		ValueType: ValueTypeString,
		Value:     domain.VirtualIPAddress,
	}

	tp.generator.Variables[varKeyOrigSSLCrt] = tp.generator.Variables[varKeySSLCrt]
	tp.generator.Variables[varKeyOrigSSLKey] = tp.generator.Variables[varKeySSLKey]

	tp.generator.Variables[varKeySSLCrt] = &Variable{
		Keyword:   varKeySSLCrt,
		ValueType: ValueTypeString,
		Value: tp.generator.domainSSLPathOrDefault(
			domain.Name, domain.SSLCertificate, sslCrtExt, tp.generator.Variables["ssl.crt.default"]),
	}
	tp.generator.Variables[varKeySSLKey] = &Variable{
		Keyword:   varKeySSLKey,
		ValueType: ValueTypeString,
		Value: tp.generator.domainSSLPathOrDefault(
			domain.Name, domain.SSLPrivateKey, sslKeyExt, tp.generator.Variables["ssl.key.default"]),
	}

	tp.setDomainClientCertVariables(domain)
}

// Per-domain client certificate variables (legacy ProxyConfGen.java:808-830).
const (
	varKeySSLClientCertMode = "ssl.clientcertmode"
	varKeySSLClientCertCA   = "ssl.clientcertca"
	varKeySSOCertAuth       = "web.sso.certauth.enabled"
	origVarPrefix           = "_orig_"
)

// domainScopedClientCertKeys are the variables overridden per domain by
// setDomainClientCertVariables and restored by clearDomainVariables.
var domainScopedClientCertKeys = []string{varKeySSLClientCertMode, varKeySSLClientCertCA, varKeySSOCertAuth}

// setDomainClientCertVariables mirrors ProxyConfGen.java:808-830:
//   - ssl.clientcertmode is the domain's mode, or ssl.clientcertmode.default.
//     When the domain has a mode, web.sso.certauth.enabled is on for "on" and
//     "optional" and off otherwise.
//   - ssl.clientcertca is <conf>/domaincerts/<domain>.client.ca.crt when the
//     domain has a client CA, or ssl.clientcertca.default.
func (tp *TemplateProcessor) setDomainClientCertVariables(domain *DomainInfo) {
	vars := tp.generator.Variables

	for _, key := range domainScopedClientCertKeys {
		vars[origVarPrefix+key] = vars[key]
	}

	if domain.ClientCertMode != "" {
		vars[varKeySSLClientCertMode] = stringVar(varKeySSLClientCertMode, domain.ClientCertMode)
		vars[varKeySSOCertAuth] = &Variable{
			Keyword:   varKeySSOCertAuth,
			ValueType: ValueTypeEnabler,
			Value:     clientCertVerifies(domain.ClientCertMode),
		}
	} else {
		vars[varKeySSLClientCertMode] = stringVar(varKeySSLClientCertMode,
			defaultVarString(vars["ssl.clientcertmode.default"]))
	}

	if domain.ClientCertCA != "" {
		vars[varKeySSLClientCertCA] = stringVar(varKeySSLClientCertCA,
			tp.generator.domainCertFilePath(domain.Name, sslClientCAExt))
	} else {
		vars[varKeySSLClientCertCA] = stringVar(varKeySSLClientCertCA,
			defaultVarString(vars["ssl.clientcertca.default"]))
	}
}

func stringVar(keyword, value string) *Variable {
	return &Variable{Keyword: keyword, ValueType: ValueTypeString, Value: value}
}

// defaultVarString returns a variable's string value, or "" when unset.
func defaultVarString(v *Variable) string {
	if v == nil {
		return ""
	}

	s, _ := v.Value.(string)

	return s
}

// clearDomainVariables removes domain-specific variables after processing.
//
// Removes: vhn, vip
// Restores: ssl.crt and ssl.key to their original values (global defaults or nil)
//
// This ensures clean separation between domains during explode processing and prevents
// one domain's SSL configuration from affecting subsequent domains.
func (tp *TemplateProcessor) clearDomainVariables() {
	delete(tp.generator.Variables, "vhn")
	delete(tp.generator.Variables, "vip")

	// Restore original SSL values (may be global defaults or nil)
	if origCrt, ok := tp.generator.Variables[varKeyOrigSSLCrt]; ok {
		if origCrt != nil {
			tp.generator.Variables[varKeySSLCrt] = origCrt
		} else {
			delete(tp.generator.Variables, varKeySSLCrt)
		}

		delete(tp.generator.Variables, varKeyOrigSSLCrt)
	}

	if origKey, ok := tp.generator.Variables[varKeyOrigSSLKey]; ok {
		if origKey != nil {
			tp.generator.Variables[varKeySSLKey] = origKey
		} else {
			delete(tp.generator.Variables, varKeySSLKey)
		}

		delete(tp.generator.Variables, varKeyOrigSSLKey)
	}

	for _, key := range domainScopedClientCertKeys {
		orig, ok := tp.generator.Variables[origVarPrefix+key]
		if !ok {
			continue
		}

		if orig != nil {
			tp.generator.Variables[key] = orig
		} else {
			delete(tp.generator.Variables, key)
		}

		delete(tp.generator.Variables, origVarPrefix+key)
	}
}

// setServerVariables sets server-specific variables for template processing
func (tp *TemplateProcessor) setServerVariables(server ServerInfo) {
	tp.generator.Variables["server_id"] = &Variable{
		Keyword:   "server_id",
		ValueType: ValueTypeString,
		Value:     server.ID,
	}
	tp.generator.Variables["server_hostname"] = &Variable{
		Keyword:   "server_hostname",
		ValueType: ValueTypeString,
		Value:     server.Hostname,
	}
}

// clearServerVariables removes server-specific variables after processing
func (tp *TemplateProcessor) clearServerVariables() {
	delete(tp.generator.Variables, "server_id")
	delete(tp.generator.Variables, "server_hostname")
}
