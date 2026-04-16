// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// bufPool reuses bytes.Buffer allocations across template processing calls.
var bufPool = sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 8192)) },
}

// SSL variable key constants used in template processing.
const (
	varKeySSLCrt     = "ssl.crt"
	varKeySSLKey     = "ssl.key"
	varKeyOrigSSLCrt = "_orig_ssl.crt"
	varKeyOrigSSLKey = "_orig_ssl.key"
)

// Template represents a parsed nginx configuration template
type Template struct {
	Name    string
	Path    string
	Content string
	Lines   []string
}

// TemplateProcessor processes nginx configuration templates
type TemplateProcessor struct {
	generator             *Generator
	templateDir           string
	outputDir             string
	varPattern            *regexp.Regexp
	explodePattern        *regexp.Regexp
	enablerPattern        *regexp.Regexp
	emptyDirectivePattern *regexp.Regexp
	// debugVars is the set of variable names that trigger extra debug logging in interpolateLine.
	debugVars map[string]struct{}
	// Mock functions for testing
	domainProvider func() []DomainInfo
	serverProvider func(serviceName string) []ServerInfo
}

// NewTemplateProcessor creates a new template processor
func NewTemplateProcessor(gen *Generator, templateDir, outputDir string) *TemplateProcessor {
	return &TemplateProcessor{
		generator:             gen,
		templateDir:           templateDir,
		outputDir:             outputDir,
		varPattern:            regexp.MustCompile(`\$\{([a-zA-Z0-9._:]+)\}`),
		explodePattern:        regexp.MustCompile(`^!\{explode\s+(\w+)\(([^)]*)\)\}`),
		enablerPattern:        regexp.MustCompile(`^(\s*)\$\{([^}]+)\}(.+)$`),
		emptyDirectivePattern: regexp.MustCompile(`^\s+[a-z0-9_]+\s+;`),
		debugVars: map[string]struct{}{
			"mail.imap.enabled":  {},
			"mail.pop3.enabled":  {},
			"mail.imaps.enabled": {},
			"mail.pop3s.enabled": {},
		},
	}
}

// LoadTemplate reads a template file from disk
func (tp *TemplateProcessor) LoadTemplate(ctx context.Context, name string) (*Template, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	// If name is an absolute path, use it directly; otherwise join with templateDir
	var path string
	if filepath.IsAbs(name) {
		path = name
	} else {
		path = filepath.Join(tp.templateDir, name)
	}

	//nolint:gosec // G304: File path comes from trusted configuration
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file %s from %s: %w", name, path, err)
	}

	// Split into lines for line-by-line processing
	lines := strings.Split(string(content), "\n")

	logger.DebugContext(ctx, "Loaded template",
		"name", name,
		"line_count", len(lines),
		"byte_count", len(content))

	return &Template{
		Name:    name,
		Path:    path,
		Content: string(content),
		Lines:   lines,
	}, nil
}

// ProcessTemplate processes a template with variable substitution
func (tp *TemplateProcessor) ProcessTemplate(ctx context.Context, tmpl *Template) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	output := bufPool.Get().(*bytes.Buffer)

	output.Reset()
	defer bufPool.Put(output)

	writer := bufio.NewWriter(output)

	// Check if first line contains explode directive
	//nolint:nestif // Explode directive requires nested processing of template iterations
	if len(tmpl.Lines) > 0 {
		if matches := tp.explodePattern.FindStringSubmatch(tmpl.Lines[0]); matches != nil {
			// Handle explode directive - process the rest of the template for each iteration
			explodeType := matches[1] // "domain" or "server"
			explodeArgs := matches[2] // arguments in parentheses

			// Process the rest of template (skip first line with directive)
			remainingLines := tmpl.Lines[1:]

			if err := tp.processExplode(ctx, explodeType, explodeArgs, remainingLines, writer); err != nil {
				return "", fmt.Errorf("error processing explode directive: %w", err)
			}

			if err := writer.Flush(); err != nil {
				return "", fmt.Errorf("error flushing output: %w", err)
			}

			return output.String(), nil
		}
	}

	// No explode directive - process template normally
	for lineNum, line := range tmpl.Lines {
		// Process variable substitutions
		processed, err := tp.interpolateLine(ctx, line)
		if err != nil {
			return "", fmt.Errorf("error processing line %d: %w", lineNum+1, err)
		}

		// Write processed line
		if _, err := writer.WriteString(processed + "\n"); err != nil {
			return "", fmt.Errorf("error writing output: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return "", fmt.Errorf("error flushing output: %w", err)
	}

	return output.String(), nil
}

// interpolateLine replaces all ${VAR} references in a line
func (tp *TemplateProcessor) interpolateLine(ctx context.Context, line string) (string, error) {
	// Check for enabler variables at the start of the line
	// Pattern: optional whitespace + ${var} + rest of line
	// Examples: "    ${mail.imap.enabled} include ..."
	//           "    ${core.ipboth.enabled}listen ..."
	// Note: No space required after } - enabler can be directly followed by directive
	enablerPattern := tp.enablerPattern

	if matches := enablerPattern.FindStringSubmatch(line); matches != nil {
		if _, ok := tp.debugVars[matches[2]]; ok {
			logger.DebugContext(ctx, "Checking potential enabler line",
				"line", line)
			logger.DebugContext(ctx, "Pattern matches",
				"matches", true)
		}

		result, handled, err := tp.processEnablerLine(ctx, matches)
		if err != nil {
			return "", err
		}

		if handled {
			return result, nil
		}
	}

	// Normal variable substitution for non-enabler variables
	result := tp.varPattern.ReplaceAllStringFunc(line, func(match string) string {
		// Extract variable name from ${VAR}
		varName := tp.varPattern.FindStringSubmatch(match)[1]

		// Look up variable value
		value, err := tp.generator.ExpandVariable(ctx, varName)
		if err != nil {
			// For missing variables, return empty string (fail silently for now)
			// In production, might want to log this
			return ""
		}

		return value
	})

	// Check if the line ends up with a directive that has no arguments
	// Pattern: whitespace + word (letters/digits/underscores) + whitespace + semicolon
	// Example: "    imap_id         ;" or "    proxy_issue_pop3_xoip   ;"
	// Must have at least one space between directive and semicolon
	if tp.emptyDirectivePattern.MatchString(result) {
		// Comment out the line by prepending "# "
		trimmed := strings.TrimSpace(result)
		logger.DebugContext(ctx, "Commenting out empty directive",
			"directive", trimmed)
		result = "    # " + trimmed
	}

	return result, nil
}

// processEnablerLine handles enabler variable logic for a line that matched the
// enabler pattern. It returns the processed line, whether the enabler was handled
// (i.e. the variable was an actual enabler type), and any error.
func (tp *TemplateProcessor) processEnablerLine(
	ctx context.Context, matches []string,
) (result string, handled bool, err error) {
	indent := matches[1]
	varName := matches[2]
	restOfLine := matches[3]

	if logger.IsDebug(ctx) {
		logger.DebugContext(ctx, "Matched enabler pattern",
			"variable", varName,
			"rest_of_line", restOfLine)
	}

	// Check if this is an enabler variable
	v, exists := tp.generator.Variables[varName]
	if !exists || v.ValueType != ValueTypeEnabler {
		return "", false, nil
	}

	// Get the boolean value
	val := v.Value
	isEnabled := false

	logger.DebugContext(ctx, "Found enabler variable",
		"variable", varName,
		"value", val,
		"value_type", fmt.Sprintf("%T", val))

	// Handle different value types
	switch v := val.(type) {
	case bool:
		isEnabled = v
		logger.DebugContext(ctx, "Enabler is bool",
			"variable", varName,
			"enabled", isEnabled)
	case string:
		// For string enablers, any non-empty value means enabled
		// This handles both "TRUE" and keyword-style enablers (e.g., "server")
		isEnabled = v != ""
		logger.DebugContext(ctx, "Enabler is string",
			"variable", varName,
			"value", v,
			"enabled", isEnabled)
	case int, int64:
		isEnabled = v != 0
		logger.DebugContext(ctx, "Enabler is int",
			"variable", varName,
			"value", v,
			"enabled", isEnabled)
	default:
		logger.ErrorContext(ctx, "Enabler has unexpected type",
			"variable", varName,
			"type", fmt.Sprintf("%T", val),
			"value", val)
	}

	processedLine := indent + restOfLine

	// Recursively process the rest of the line (it may have other ${} variables)
	processedRest, err := tp.interpolateLine(ctx, processedLine)
	if err != nil {
		return "", false, err
	}

	if isEnabled {
		// Variable is true - remove the enabler variable, keep the rest of the line
		logger.DebugContext(ctx, "Enabler is TRUE, processing rest of line",
			"variable", varName,
			"processed_line", processedLine)

		return processedRest, true, nil
	}

	// Variable is false - comment out the line (without the enabler variable)
	logger.DebugContext(ctx, "Enabler is FALSE, commenting out line",
		"variable", varName)

	// Remove the indent from processed line and add it back with comment
	trimmedLine := strings.TrimLeft(processedRest, " \t")

	return indent + "#" + trimmedLine, true, nil
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

// processTemplateLines iterates over template lines, optionally skipping comment lines,
// interpolates each line, and writes the result to writer.
func (tp *TemplateProcessor) processTemplateLines(
	ctx context.Context, lines []string, writer *bufio.Writer, skipComments bool,
) error {
	for _, line := range lines {
		if skipComments && strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		processed, err := tp.interpolateLine(ctx, line)
		if err != nil {
			return fmt.Errorf("error processing exploded template line: %w", err)
		}

		if _, err := writer.WriteString(processed + "\n"); err != nil {
			return fmt.Errorf("error writing exploded output: %w", err)
		}
	}

	return nil
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

// WriteOutput writes processed template to output file
func (tp *TemplateProcessor) WriteOutput(ctx context.Context, name string, content string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	// Determine output file path (remove .template extension)
	outputName := strings.TrimSuffix(name, ".template")
	outputPath := filepath.Join(tp.outputDir, outputName)

	// Check if generator is available for mode checking
	dryRun := false
	verbose := false

	if tp.generator != nil {
		dryRun = tp.generator.DryRun
		verbose = tp.generator.Verbose
	}

	// In dry-run mode, just log what would be written
	if dryRun {
		logger.DebugContext(ctx, "[DRY-RUN] Would write file",
			"path", outputPath,
			"byte_count", len(content))

		if verbose {
			logger.DebugContext(ctx, "[DRY-RUN] Content preview",
				"content", truncateString(content, 500))
		}

		return nil
	}

	// Ensure output directory exists
	if err := os.MkdirAll(tp.outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write atomically using temp file + rename.
	// Use os.CreateTemp in the output directory to avoid predictable temp paths
	// and ensure the temp file is on the same filesystem for atomic rename.
	tmpFile, err := os.CreateTemp(filepath.Dir(outputPath), ".configd-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tmpPath := tmpFile.Name()

	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		if rerr := os.Remove(tmpPath); rerr != nil {
			logger.WarnContext(ctx, "Failed to remove temp file",
				"path", tmpPath,
				"error", rerr)
		}

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	if verbose {
		logger.DebugContext(ctx, "Wrote file",
			"path", outputPath,
			"byte_count", len(content))
	}

	return nil
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + fmt.Sprintf("\n... (truncated, %d more bytes)", len(s)-maxLen)
}

// ProcessTemplateFile is a convenience method that loads, processes, and writes a template
func (tp *TemplateProcessor) ProcessTemplateFile(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	tmpl, err := tp.LoadTemplate(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to load template %s: %w", name, err)
	}

	content, err := tp.ProcessTemplate(ctx, tmpl)
	if err != nil {
		return fmt.Errorf("failed to process template %s: %w", name, err)
	}

	if err := tp.WriteOutput(ctx, name, content); err != nil {
		return fmt.Errorf("failed to write output for template %s: %w", name, err)
	}

	return nil
}

// ProcessAllTemplates processes all .template files in the template directory
func (tp *TemplateProcessor) ProcessAllTemplates(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	entries, err := os.ReadDir(tp.templateDir)
	if err != nil {
		return fmt.Errorf("failed to read template directory %s: %w", tp.templateDir, err)
	}

	var processingErrors []error

	successCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if strings.HasSuffix(entry.Name(), ".template") {
			if err := tp.ProcessTemplateFile(ctx, entry.Name()); err != nil {
				processingErrors = append(processingErrors, err)
			} else {
				successCount++
			}
		}
	}

	if len(processingErrors) > 0 {
		return fmt.Errorf("processed %d templates with %d errors: %v",
			successCount, len(processingErrors), processingErrors)
	}

	logger.DebugContext(ctx, "Successfully processed templates",
		"count", successCount)

	return nil
}

// ExpandVariable expands a single variable by name
// This is a helper method on Generator that looks up the variable and returns its expanded value
func (g *Generator) ExpandVariable(ctx context.Context, name string) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	v, exists := g.Variables[name]
	if !exists {
		return "", fmt.Errorf("variable %s not found", name)
	}

	// Log for debugging port issues
	if strings.Contains(name, "port") {
		logger.DebugContext(ctx, "ExpandVariable",
			"name", name,
			"value", v.Value,
			"type", fmt.Sprintf("%T", v.Value),
			"value_type", v.ValueType)
	}

	// Use CustomFormatter if available
	if v.CustomFormatter != nil {
		return v.CustomFormatter(v.Value)
	}

	// Format based on ValueType (Java's approach: different formatting for ENABLER vs BOOLEAN)
	if v.ValueType == ValueTypeEnabler {
		return formatEnabler(v.Value), nil
	}

	// Handle TIME type values - Java outputs integers as milliseconds with "ms" suffix
	if v.ValueType == ValueTypeTime {
		return formatTimeValue(v.Value), nil
	}

	// Handle TimeInSec type values - Convert milliseconds to plain seconds (Java's TimeInSecVarWrapper)
	if v.ValueType == ValueTypeTimeInSec {
		return formatTimeInSecValue(v.Value), nil
	}

	// Default formatting based on type
	return formatValue(v.Value), nil
}

// formatEnabler formats a boolean value for ENABLER type variables
// Returns "" if true (line enabled), "#" if false (line commented out)
// This matches Java's ProxyConfVar.formatEnabler() behavior
func formatEnabler(value any) string {
	if value == nil {
		return "#"
	}

	switch v := value.(type) {
	case bool:
		if v {
			return ""
		}

		return "#"
	case string:
		// For string enablers, any non-empty value means enabled
		if v != "" {
			return ""
		}

		return "#"
	case int, int64:
		if v != 0 {
			return ""
		}

		return "#"
	default:
		// Unknown type, default to disabled
		return "#"
	}
}

// formatValue converts a value to its string representation
func formatValue(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case int, int64:
		return fmt.Sprintf("%d", v)
	case bool:
		if v {
			return "on"
		}

		return "off"
	case []string:
		return strings.Join(v, " ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatTimeValue formats time values for output
// This matches Java ProxyConfGen behavior for regular TIME values:
// Java ProxyConfGen outputs integer time values as milliseconds with "ms" suffix
// Example: 3600000 -> "3600000ms", "10s" -> "10s" (already has unit)
func formatTimeValue(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		// Already formatted with unit (e.g., "10s", "2m")
		return v
	case int:
		// Integer values are milliseconds, add "ms" suffix
		return fmt.Sprintf("%dms", v)
	case int64:
		// Integer values are milliseconds, add "ms" suffix
		return fmt.Sprintf("%dms", v)
	default:
		// Fallback to string representation
		return fmt.Sprintf("%v", v)
	}
}

// formatTimeInSecValue formats time values for TimeInSec type variables
// This matches Java's TimeInSecVarWrapper behavior:
// - LDAP stores values in milliseconds
// - Converts to plain seconds (divides by 1000)
// - Outputs as plain number without unit suffix
// Example: 300000 (ms) -> "300" (seconds)
func formatTimeInSecValue(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		// If it's a string, try to parse as integer milliseconds
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			return fmt.Sprintf("%d", ms/1000)
		}

		return v
	case int:
		// Convert milliseconds to seconds
		return fmt.Sprintf("%d", v/1000)
	case int64:
		// Convert milliseconds to seconds
		return fmt.Sprintf("%d", v/1000)
	default:
		// Fallback to string representation
		return fmt.Sprintf("%v", v)
	}
}

// atomicCopyFile copies src to dst atomically via a temp file + rename.
func atomicCopyFile(src, dst string) error {
	//nolint:gosec // G304: File path comes from trusted configuration
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}

	defer func() { _ = source.Close() }()

	tmpFile, err := os.CreateTemp(filepath.Dir(dst), ".configd-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, source); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to copy to temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// Backup creates a backup of an existing configuration file
func (tp *TemplateProcessor) Backup(ctx context.Context, configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// File doesn't exist, no backup needed
		return nil
	}

	if err := atomicCopyFile(configPath, configPath+".backup"); err != nil {
		return fmt.Errorf("failed to backup %s: %w", configPath, err)
	}

	return nil
}

// Rollback restores a configuration from backup
func (tp *TemplateProcessor) Rollback(configPath string) error {
	backupPath := configPath + ".backup"

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("no backup file found: %s", backupPath)
	}

	if err := os.Rename(backupPath, configPath); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	return nil
}

// ValidateNginxConfig runs nginx -t to validate the configuration
// Returns nil if validation succeeds, error with nginx output if it fails
func (tp *TemplateProcessor) ValidateNginxConfig(ctx context.Context, configPath string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	// Look for nginx binary in common locations
	nginxPaths := []string{
		"/opt/zextras/common/sbin/nginx",
		"/usr/bin/nginx",
		"/usr/sbin/nginx",
		"nginx", // Try PATH
	}

	var nginxBinary string

	for _, path := range nginxPaths {
		if _, err := exec.LookPath(path); err == nil {
			nginxBinary = path
			break
		}
	}

	if nginxBinary == "" {
		logger.WarnContext(ctx, "Nginx binary not found, skipping validation")

		return nil // Don't fail if nginx isn't available
	}

	// Run nginx -t with the specific config file
	cmd := exec.CommandContext(ctx, nginxBinary, "-t", "-c", configPath)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	// Check if syntax is OK (nginx prints this to stderr even on success)
	if strings.Contains(outputStr, "syntax is ok") {
		// Syntax validation passed - ignore PID file errors or other runtime issues
		if tp.generator != nil && tp.generator.Verbose {
			logger.DebugContext(ctx, "Nginx -t validation passed",
				"config_path", configPath)
		}

		return nil
	}

	// If there's an error and syntax is NOT ok, it's a real validation failure
	if err != nil {
		return fmt.Errorf("nginx validation failed: %w\nOutput: %s", err, outputStr)
	}

	return nil
}
