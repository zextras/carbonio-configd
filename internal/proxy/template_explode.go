// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bufio"
	"context"
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
	var domainInfos []DomainInfo

	// Resolve the domain source: injected provider (for testing), no LDAP
	// client available, or production LDAP query.
	switch {
	case tp.domainProvider != nil:
		domainInfos = tp.domainProvider()
	case tp.generator.LdapClient == nil:
		logger.DebugContext(ctx, "No LDAP client available, returning empty list")
		return []DomainInfo{}
	default:
		domains, err := cachedLDAPQuery(ctx, tp.generator.Cache, "ldap:domains", "domains",
			func() ([]ldap.Domain, error) {
				return tp.generator.LdapClient.QueryDomains(ctx)
			})
		if err != nil {
			logger.ErrorContext(ctx, "Domain query failed", "error", err)
			return []DomainInfo{}
		}

		domainInfos = make([]DomainInfo, 0, len(domains))
		for _, domain := range domains {
			domainInfos = append(domainInfos, DomainInfo{
				Name:             domain.DomainName,
				VirtualHostname:  domain.VirtualHostname,
				VirtualIPAddress: domain.VirtualIPAddress,
				ClientCertMode:   domain.ClientCertMode,
				SSLCertificate:   domain.SSLCertificate,
				SSLPrivateKey:    domain.SSLPrivateKey,
			})
		}

		logger.DebugContext(ctx, "Loaded domains from LDAP",
			"count", len(domainInfos))
	}

	// Sort domains by name for deterministic output
	slices.SortFunc(domainInfos, func(a, b DomainInfo) int {
		return strings.Compare(a.Name, b.Name)
	})

	return domainInfos
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
	case tp.generator.LdapClient == nil:
		logger.DebugContext(ctx, "No LDAP client available, returning empty list",
			"service", serviceName)

		return []ServerInfo{}
	default:
		cacheKey := fmt.Sprintf("ldap:servers:%s", serviceName)

		servers, err := cachedLDAPQuery(ctx, tp.generator.Cache, cacheKey, "servers",
			func() ([]ldap.Server, error) {
				return tp.generator.LdapClient.QueryServers(ctx, serviceName)
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

	// Store original SSL values before overriding (for restoration)
	tp.generator.Variables[varKeyOrigSSLCrt] = tp.generator.Variables[varKeySSLCrt]
	tp.generator.Variables[varKeyOrigSSLKey] = tp.generator.Variables[varKeySSLKey]

	// Set SSL certificate variables if available
	if domain.SSLCertificate != "" {
		tp.generator.Variables[varKeySSLCrt] = &Variable{
			Keyword:   varKeySSLCrt,
			ValueType: ValueTypeString,
			Value:     domain.SSLCertificate,
		}
	}

	if domain.SSLPrivateKey != "" {
		tp.generator.Variables[varKeySSLKey] = &Variable{
			Keyword:   varKeySSLKey,
			ValueType: ValueTypeString,
			Value:     domain.SSLPrivateKey,
		}
	}
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
