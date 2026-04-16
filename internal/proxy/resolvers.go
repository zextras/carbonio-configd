// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - custom variable resolvers
package proxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/ldap"
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

// pop3ExpireCapability is the POP3 capability string for message expiry.
const pop3ExpireCapability = "EXPIRE 31 USER"

// defaultPOP3Capabilities is the default set of POP3 capabilities (matching Java ProxyConfGen).
var defaultPOP3Capabilities = []string{pop3ExpireCapability, "TOP", "UIDL", "USER", "XOIP"}

// compressionMIMETypes is the shared list of MIME types for gzip and brotli compression.
const compressionMIMETypes = `        application/atom+xml
        application/geo+json
        application/javascript
        application/x-javascript
        application/json
        application/ld+json
        application/manifest+json
        application/rdf+xml
        application/rss+xml
        application/xhtml+xml
        application/xml
        font/eot
        font/otf
        font/ttf
        font/woff2
        image/svg+xml
        text/css
        text/javascript
        text/plain
        text/xml;
`

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

// resolveLookupHandlers constructs the lookup handler URLs
// Format: https://hostname:port/service/extension/nginx-lookup
func (g *Generator) resolveLookupHandlers(ctx context.Context) (any, error) {
	// Get the extension port from global config (zimbraExtensionBindPort)
	extensionPort := "7072" // default

	if val, ok := g.getConfigValue("zimbraExtensionBindPort", sourceGlobal); ok {
		extensionPort = val
	}

	// Get the lookup target hostname from global config
	// zimbraReverseProxyAvailableLookupTargets contains the hostname
	lookupHost := ""

	if val, ok := g.getConfigValue("zimbraReverseProxyAvailableLookupTargets", sourceGlobal); ok {
		lookupHost = val
	}

	// If no specific lookup host, try to get our own hostname
	if lookupHost == "" {
		if val, ok := g.getConfigValue("zimbra_server_hostname", sourceLocal); ok {
			lookupHost = val
		}
	}

	// Fallback to localhost
	if lookupHost == "" {
		lookupHost = "127.0.0.1"
	}

	// Resolve hostname to IP, caching within a single proxygen cycle
	// to avoid repeated /etc/hosts reads from the pure-Go resolver.
	if g.cachedLookupIP != "" {
		lookupHost = g.cachedLookupIP
	} else {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, lookupHost)
		if err == nil && len(ips) > 0 {
			lookupHost = ips[0].String()
		} else {
			logger.WarnContext(ctx, "Failed to resolve lookup host to IP, using hostname",
				"hostname", lookupHost,
				"error", err)
		}

		g.cachedLookupIP = lookupHost
	}

	// Construct the lookup handler URL
	url := fmt.Sprintf("https://%s:%s/service/extension/nginx-lookup", lookupHost, extensionPort)

	return url, nil
}

// resolveMemcacheServers returns the list of memcache servers
func (g *Generator) resolveMemcacheServers(ctx context.Context) (any, error) {
	servers, err := g.getAllMemcachedServers(ctx)
	if err != nil {
		// In test environments without zmprov, return empty string instead of error
		logger.WarnContext(ctx, resolverQueryFailed, "error", err)
		return "", nil //nolint:nilerr // Intentional: fallback for test environments without zmprov
	}

	return formatMemcacheServers(servers), nil
}

// resolveLoginUpstreamDisable returns "#" to comment out login upstream block if no servers configured
// Returns "" (empty string) if servers are configured to enable the upstream block
func (g *Generator) resolveLoginUpstreamDisable(ctx context.Context) (any, error) {
	return g.resolveUpstreamDisable(ctx, "zimbraReverseProxyUpstreamLoginServers", "Login")
}

// resolveStrictServerName returns "" to enable strict server name block if enabled in config
// Returns "#" to comment out the block if disabled
// Server config takes precedence over global config
func (g *Generator) resolveStrictServerName(ctx context.Context) (any, error) {
	const attrName = "zimbraReverseProxyStrictServerNameEnabled"

	// Check ServerConfig first, then fall back to GlobalConfig
	if val, ok := g.getConfigValue(attrName, sourceServer, sourceGlobal); ok {
		logger.DebugContext(ctx, "Found strict server name attribute",
			"value", val)

		if isTruthy(val) {
			logger.DebugContext(ctx, "Strict server name enabled")

			return "", nil
		}

		logger.DebugContext(ctx, "Strict server name disabled")

		return "#", nil
	}

	// Default - comment out the block (disabled)
	logger.DebugContext(ctx, "Strict server name attribute not found, disabled by default")

	return "#", nil
}

// resolveProxyHTTPCompression returns HTTP compression directives (gzip + brotli configuration)
// Returns full compression config block if enabled, empty string if disabled
func (g *Generator) resolveProxyHTTPCompression(ctx context.Context) (any, error) {
	// Check zimbraHttpCompressionEnabled from server config
	enabled := true // default

	if val, ok := g.getConfigValue("zimbraHttpCompressionEnabled", sourceServer); ok {
		enabled = isTruthy(val)
	}

	if !enabled {
		return "", nil
	}

	// Return full compression directive block
	// This matches Java's ProxyCompressionServerVar.G_ZIP_COMPRESSION_DIRECTIVE + BROTLI_COMPRESSION_DIRECTIVE
	compressionDirectives := `
    gzip on;
    gzip_disable "msie6";
    gzip_vary on;
    gzip_proxied any;
    gzip_comp_level 6;
    gzip_buffers 16 8k;
    gzip_http_version 1.1;
    gzip_min_length 256;
    gzip_types
` + compressionMIMETypes + `
    brotli on;
    brotli_static on;
    brotli_types
` + compressionMIMETypes

	return compressionDirectives, nil
}

// resolveWebSSLProtocols returns space-separated list of enabled SSL/TLS protocols
// Reads from zimbraReverseProxySSLProtocols (multi-value attribute)
// Returns default "TLSv1.2 TLSv1.3" if not configured
func (g *Generator) resolveWebSSLProtocols(ctx context.Context) (any, error) {
	// Default protocols
	defaultProtocols := []string{"TLSv1.2", "TLSv1.3"}

	// Check ServerConfig for zimbraReverseProxySSLProtocols
	if val, ok := g.getConfigValue("zimbraReverseProxySSLProtocols", sourceServer); ok {
		// Multi-value attribute - split by comma or space
		protocols := strings.FieldsFunc(val, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\n'
		})
		// Filter out empty strings
		filtered := make([]string, 0, len(protocols))

		for _, p := range protocols {
			p = strings.TrimSpace(p)
			if p != "" {
				filtered = append(filtered, p)
			}
		}

		if len(filtered) > 0 {
			return filtered, nil
		}
	}

	return defaultProtocols, nil
}

// resolveAddHeadersDefault generates the add_header directive block for default virtual host.
// This reads zimbraReverseProxyResponseHeaders and carbonioReverseProxyResponseCSPHeader
// and generates nginx add_header directives, matching Java AddHeadersVar behavior.
func (g *Generator) resolveAddHeadersDefault(ctx context.Context) (any, error) {
	var headers []string

	// Get custom response headers from GlobalConfig (multi-value attribute)
	if val, ok := g.getConfigValue("zimbraReverseProxyResponseHeaders", sourceGlobal); ok {
		// zimbraReverseProxyResponseHeaders is multi-value, parse as individual header lines
		// Format is "HeaderName: Value" per line
		headerLines := parseMultiValueAttribute(val)
		headers = append(headers, headerLines...)
	}

	// Get CSP header if it exists
	if cspVal, ok := g.getConfigValue("carbonioReverseProxyResponseCSPHeader", sourceGlobal); ok {
		headers = append(headers, cspVal)
	}

	// If no headers configured, return empty string
	if len(headers) == 0 {
		return "", nil
	}

	// Generate add_header directives
	var result strings.Builder

	for i, header := range headers {
		// Parse header into name and value
		parts := strings.SplitN(header, ":", 2)
		if len(parts) != 2 {
			logger.WarnContext(ctx, "Skipping malformed header",
				"header", header)

			continue
		}

		headerName := strings.TrimSpace(parts[0])
		headerValue := strings.TrimSpace(parts[1])

		// Add indentation for subsequent headers
		if i > 0 {
			result.WriteString("\n    ")
		}

		// Generate add_header directive
		fmt.Fprintf(&result, "add_header %s %s;", headerName, headerValue)
	}

	return result.String(), nil
}

// normalizeMultiValue replaces newline and comma separators with spaces in a single pass,
// suitable for splitting multi-value LDAP attributes with strings.Fields.
func normalizeMultiValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == ',' {
			return ' '
		}

		return r
	}, s)
}

// parseMultiValueAttribute splits a multi-value LDAP attribute into individual values.
// LDAP multi-value attributes come as single strings with values separated by some delimiter.
// In our case, the attribute contains multiple "Header: Value" strings.
func parseMultiValueAttribute(attr string) []string {
	// For multi-value attributes, values are typically newline-separated in our format
	// However, the actual LDAP multi-value comes as multiple separate values
	// When read via zmprov, they appear as multiple lines
	// When read via our LDAP code, they may come as a single concatenated string or array

	// Try splitting by newline first
	if strings.Contains(attr, "\n") {
		lines := strings.Split(attr, "\n")

		result := make([]string, 0, len(lines))

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				result = append(result, line)
			}
		}

		if len(result) > 0 {
			return result
		}
	}

	// If no newlines, return as single value
	return []string{attr}
}

// makeLoginURLResolver creates a resolver that returns the login URL from the given
// config key, falling back to staticLoginPath ("/static/login/").
func (g *Generator) makeLoginURLResolver(configKey string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		if val, ok := g.getConfigValue(configKey, sourceGlobal); ok {
			return val, nil
		}

		return staticLoginPath, nil
	}
}

// makeLogoutRedirectResolver creates a resolver that checks the given config key
// for a custom logout URL. If set, returns "return 200"; otherwise "return 307 /static/login/".
func (g *Generator) makeLogoutRedirectResolver(configKey string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		if _, ok := g.getConfigValue(configKey, sourceGlobal); ok {
			return nginxReturn200, nil
		}

		return nginxReturn307Path, nil
	}
}

// ============================================================================
// Mail Protocol Capability and Greeting Resolvers
// ============================================================================

// resolveIMAPCapabilities returns IMAP capability list formatted for nginx
// Reads zimbraReverseProxyImapEnabledCapability multi-value attribute
// Default: Full IMAP4rev1 capability set matching Java ProxyConfGen
func (g *Generator) resolveIMAPCapabilities(ctx context.Context) (any, error) {
	// Default capabilities matching Java ImapCapaVar.getDefaultImapCapabilities()
	// Plus commonly enabled extensions
	defaultCaps := []string{
		"IMAP4rev1", "ID", "LITERAL+", "SASL-IR", "IDLE", "NAMESPACE",
		"ACL", "BINARY", "CATENATE", "CHILDREN", "CONDSTORE", "ENABLE",
		"ESEARCH", "ESORT", "I18NLEVEL=1", "LIST-EXTENDED", "LIST-STATUS",
		"MULTIAPPEND", "QRESYNC", "QUOTA", "RIGHTS=ektx", "SEARCHRES",
		"SORT", "THREAD=ORDEREDSUBJECT", "UIDPLUS", "UNSELECT", "WITHIN", "XLIST",
	}

	// Try to read from GlobalConfig (multi-valued attribute)
	if val, ok := g.getConfigValue("zimbraReverseProxyImapEnabledCapability", sourceGlobal); ok {
		// Parse comma-separated, space-separated, or newline-separated values
		val = normalizeMultiValue(val)

		caps := strings.Fields(val)
		if len(caps) > 0 {
			return caps, nil
		}
	}
	// Return full default capabilities
	return defaultCaps, nil
}

// formatCapabilities formats protocol capabilities as quoted space-separated strings.
// protocolName is used in error messages (e.g. "IMAP", "POP3").
// Input: []string{"IMAP4rev1", "ID", "LITERAL+"}
// Output: " \"IMAP4rev1\" \"ID\" \"LITERAL+\""
func formatCapabilities(val any, protocolName string) (string, error) {
	caps, ok := val.([]string)
	if !ok {
		return "", fmt.Errorf("expected []string for %s capabilities", protocolName)
	}

	var sb strings.Builder
	for _, cap := range caps {
		sb.WriteString(` "`)
		sb.WriteString(cap)
		sb.WriteString(`"`)
	}

	return sb.String(), nil
}

// formatIMAPCapabilities formats IMAP capabilities as quoted space-separated strings
func formatIMAPCapabilities(val any) (string, error) {
	return formatCapabilities(val, "IMAP")
}

// formatPOP3Capabilities formats POP3 capabilities as quoted space-separated strings
func formatPOP3Capabilities(val any) (string, error) {
	return formatCapabilities(val, "POP3")
}

// resolvePOP3Capabilities returns POP3 capability list formatted for nginx
// Reads zimbraReverseProxyPop3EnabledCapability multi-value attribute
// Default: [pop3ExpireCapability, "TOP", "UIDL", "USER", "XOIP"]
func (g *Generator) resolvePOP3Capabilities(ctx context.Context) (any, error) {
	// Try to read from GlobalConfig (multi-valued attribute)
	val, ok := g.getConfigValue("zimbraReverseProxyPop3EnabledCapability", sourceGlobal)
	if !ok {
		// Return default capabilities (matching Java ProxyConfGen)
		return defaultPOP3Capabilities, nil
	}

	// Multi-value attributes are concatenated with newlines by configmgr
	// Each line is a complete capability (may contain spaces like pop3ExpireCapability)
	lines := strings.Split(val, "\n")
	caps := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			caps = append(caps, trimmed)
		}
	}

	if len(caps) > 0 {
		return caps, nil
	}

	// Return default capabilities (matching Java ProxyConfGen)
	return defaultPOP3Capabilities, nil
}

// resolveIMAPId constructs the IMAP ID extension value
// Java equivalent constructs: "NAME" "Zimbra" "VERSION" "<version>" "RELEASE" "<release>"
// This matches the Java ProxyConfGen behavior where it reads from BuildInfo.
//
// The identifier is deterministic for equivalent inputs: the version is
// derived solely from the LocalConfig-supplied values (the zimbra_home
// .version file and zimbra_buildnum). When no build number is available the
// version is left as-is instead of being stamped with the current date, so
// repeated invocations produce identical output and avoid noisy reloads.
func (g *Generator) resolveIMAPId(_ context.Context) (any, error) {
	// Try to read version from LocalConfig or default location
	version := "UNKNOWN"
	release := "carbonio"

	// Read version from /opt/zextras/.version if available
	if basePath, ok := g.getConfigValue("zimbra_home", sourceLocal); ok {
		versionFile := basePath + "/.version"
		//nolint:gosec // G304: File path comes from trusted zimbra_home configuration
		if data, err := os.ReadFile(versionFile); err == nil {
			version = strings.TrimSpace(string(data))
		}
	}

	// Try to get build info from LocalConfig if available
	if buildNo, ok := g.getConfigValue("zimbra_buildnum", sourceLocal); ok {
		// Only append if not already present
		if !strings.Contains(version, "_") {
			version = version + "_ZEXTRAS_" + buildNo
		}
	}

	// Construct the IMAP ID string
	//nolint:gocritic // sprintfQuotedString: We need literal quotes in the output, not Go-escaped quotes
	imapID := fmt.Sprintf(`"NAME" "Zimbra" "VERSION" "%s" "RELEASE" "%s"`, version, release)

	return imapID, nil
}

// resolveGreeting returns a protocol greeting banner with version if the given attribute is enabled.
func (g *Generator) resolveGreeting(attribute, format string) (any, error) {
	exposeVersion := false

	if val, ok := g.getConfigValue(attribute, sourceGlobal); ok {
		exposeVersion = isTruthy(val)
	}

	if exposeVersion {
		version := g.GetCarboVersion()
		return fmt.Sprintf(format, version), nil
	}

	return "", nil
}

// resolveIMAPGreeting returns IMAP greeting banner with version if enabled
func (g *Generator) resolveIMAPGreeting(_ context.Context) (any, error) {
	return g.resolveGreeting(
		"zimbraReverseProxyImapExposeVersionOnBanner",
		"* OK Carbonio %s IMAP4 ready")
}

// resolvePOP3Greeting returns POP3 greeting banner with version if enabled
func (g *Generator) resolvePOP3Greeting(_ context.Context) (any, error) {
	return g.resolveGreeting(
		"zimbraReverseProxyPop3ExposeVersionOnBanner",
		"+OK Carbonio %s POP3 ready")
}

// ============================================================================
// Error Pages Resolver
// ============================================================================

// resolveErrorPages returns error_page directives for 502 and 504 errors
// Reads zimbraReverseProxyErrorHandlerURL attribute
// If empty: uses default static error pages (/zmerror_upstream_502.html)
// If set: redirects to custom handler URL with error code and upstream params
func (g *Generator) resolveErrorPages(ctx context.Context) (any, error) {
	errURL := ""

	if val, ok := g.getConfigValue("zimbraReverseProxyErrorHandlerURL", sourceGlobal); ok {
		errURL = val
	}

	var sb strings.Builder

	errors := []string{"502", "504"}

	if errURL == "" {
		// Use default static error pages
		for _, errCode := range errors {
			fmt.Fprintf(&sb, "error_page %s /zmerror_upstream_%s.html;\n", errCode, errCode)
		}
	} else {
		// Use custom error handler with parameters
		for _, errCode := range errors {
			fmt.Fprintf(&sb, "error_page %s %s?err=%s&up=$upstream_addr;\n", errCode, errURL, errCode)
		}
	}

	// Trim trailing newline
	result := sb.String()
	if result != "" {
		result = result[:len(result)-1]
	}

	return result, nil
}

// ============================================================================
// SSL Session Cache Size Resolver
// ============================================================================

// resolveSSLSessionCacheSize returns SSL session cache configuration
// Reads zimbraReverseProxySSLSessionCacheSize attribute (default: 10m)
// Returns: "shared:SSL:<size>" format for nginx ssl_session_cache directive
func (g *Generator) resolveSSLSessionCacheSize(ctx context.Context) (any, error) {
	size := "10m"

	if val, ok := g.getConfigValue("zimbraReverseProxySSLSessionCacheSize", sourceGlobal); ok {
		size = val
	}

	return fmt.Sprintf("shared:SSL:%s", size), nil
}

// ============================================================================
// Upstream Fair Share Memory Resolver
// ============================================================================

// resolveUpstreamFairShmSize returns upstream_fair_shm_size configuration
// Reads zimbraReverseProxyUpstreamFairShmSize attribute (minimum: 32k)
// Returns: "upstream_fair_shm_size <size>k;" directive
func (g *Generator) resolveUpstreamFairShmSize(ctx context.Context) (any, error) {
	sizeStr := "32"

	if val, ok := g.getConfigValue("zimbraReverseProxyUpstreamFairShmSize", sourceGlobal); ok {
		sizeStr = val
	}

	// Parse and validate minimum size
	var size int
	if _, err := fmt.Sscanf(sizeStr, "%d", &size); err != nil || size < 32 {
		logger.WarnContext(ctx, "Invalid upstream fair shm size, using default 32",
			"size_str", sizeStr)

		size = 32
	}

	return fmt.Sprintf("upstream_fair_shm_size %dk;", size), nil
}

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

	addressSet := g.collectVirtualIPAddresses(ctx, strictServerNamePrefix)

	// If no addresses found, return expanded placeholder (matches Java: addresses.isEmpty())
	if len(addressSet) == 0 {
		return strictServerNamePrefix, nil
	}

	// Get the expanded value of web.https.port
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
// When LDAP is unavailable or fails, returns nil (fallback handled by the caller).
func (g *Generator) collectVirtualIPAddresses(ctx context.Context, fallback string) map[string]bool {
	if g.LdapClient == nil {
		return nil
	}

	domains, ok := g.queryDomains(ctx, fallback)
	if !ok {
		return nil
	}

	addressSet := make(map[string]bool)

	for _, domain := range domains {
		if domain.VirtualIPAddress != "" {
			addressSet[domain.VirtualIPAddress] = true
		}
	}

	return addressSet
}

// queryDomains fetches domains from LDAP, using the cache when available.
// Returns (domains, true) on success or (nil, false) when the caller should fall back.
func (g *Generator) queryDomains(ctx context.Context, _ string) ([]ldap.Domain, bool) {
	if g.Cache != nil {
		cachedData, err := g.Cache.GetCachedConfig(ctx, "ldap:domains", func() (any, error) {
			return g.LdapClient.QueryDomains(ctx)
		})
		if err != nil {
			return nil, false //nolint:nilerr // Intentional: fallback to placeholder on LDAP failure
		}

		domains, ok := cachedData.([]ldap.Domain)

		return domains, ok
	}

	domains, err := g.LdapClient.QueryDomains(ctx)
	if err != nil {
		return nil, false //nolint:nilerr // Intentional: fallback to placeholder on LDAP failure
	}

	return domains, true
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

		fmt.Fprintf(&sb, "%s    listen %s:%s default_server;", prefix, addr, httpsPort)
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
	servers, err := g.getAllReverseProxyBackends(ctx)
	if err != nil {
		// If we can't get backends, assume none are available (fail safe)
		// We return nil error because this is a variable resolution, not a fatal error
		return false, nil //nolint:nilerr // Intentional: fallback for variable resolution
	}

	return len(servers) > 0, nil
}

// ============================================================================
// SASL Host from IP Resolver
// ============================================================================

// resolveSaslHostFromIP returns hostname for SASL authentication from IP
// Reads zimbraReverseProxySaslHostFromIP attribute
func (g *Generator) resolveSaslHostFromIP(ctx context.Context) (any, error) {
	saslHost := "off"

	if val, ok := g.getConfigValue("zimbraReverseProxySaslHostFromIP", sourceGlobal); ok {
		saslHost = val
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
			sslToUpstream = isTruthy(val)
		}

		if sslToUpstream {
			return "https://" + sslName, nil
		}

		return "http://" + nonSSLName, nil
	}
}

// resolveHTTPEnabled returns true unless zimbraReverseProxyMailMode is 'https'
// Matches Java HttpEnablerVar logic
func (g *Generator) resolveHTTPEnabled(ctx context.Context) (any, error) {
	if mailMode, ok := g.getConfigValue("zimbraReverseProxyMailMode", sourceGlobal); ok {
		if strings.EqualFold(mailMode, "https") {
			return false, nil
		}
	}

	return true, nil
}

// resolveHTTPSEnabled returns true unless zimbraReverseProxyMailMode is 'http'
// Matches Java HttpsEnablerVar logic
func (g *Generator) resolveHTTPSEnabled(ctx context.Context) (any, error) {
	if mailMode, ok := g.getConfigValue("zimbraReverseProxyMailMode", sourceGlobal); ok {
		if strings.EqualFold(mailMode, "http") {
			return false, nil
		}
	}

	return true, nil
}

// resolveUpstreamDisable is a generic helper that returns "#" to comment out upstream blocks if no servers configured.
// Returns "" (empty string) if servers are configured to enable the upstream block.
func (g *Generator) resolveUpstreamDisable(
	ctx context.Context,
	attributeName string,
	upstreamName string) (any, error) {
	servers, err := g.getUpstreamServersByAttribute(ctx, attributeName)
	logger.DebugContext(ctx, "Resolving upstream disable",
		"upstream", upstreamName,
		"server_count", len(servers),
		"error", err)

	if err != nil || len(servers) == 0 {
		// No servers configured - comment out the upstream block
		logger.InfoContext(ctx, upstreamName+" upstream disabled - no servers configured")

		return "#", nil //nolint:nilerr // Intentional: disable upstream block on error or empty servers
	}
	// Servers are configured - enable the upstream block
	logger.InfoContext(ctx, upstreamName+" upstream enabled",
		"server_count", len(servers))

	return "", nil
}

// resolveEwsUpstreamDisable returns "#" to comment out EWS upstream block if no servers configured
// Returns "" (empty string) if servers are configured to enable the upstream block
// Matches Java EwsEnablerVar logic
func (g *Generator) resolveEwsUpstreamDisable(ctx context.Context) (any, error) {
	return g.resolveUpstreamDisable(ctx, "zimbraReverseProxyUpstreamEwsServers", "EWS")
}

// resolveZxUpstreamDisable returns "#" to comment out ZX upstream block if no servers configured
// Returns "" (empty string) if servers are configured to enable the upstream block
func (g *Generator) resolveZxUpstreamDisable(ctx context.Context) (any, error) {
	return g.resolveUpstreamDisable(ctx, "zimbraReverseProxyUpstreamZxServers", "ZX")
}

// resolveWebclientUpstreamDisable returns "#" to comment out webclient upstream block if no servers configured
// Returns "" (empty string) if servers are configured to enable the upstream block
func (g *Generator) resolveWebclientUpstreamDisable(ctx context.Context) (any, error) {
	return g.resolveUpstreamDisable(ctx, "zimbraReverseProxyUpstreamClientServers", "Webclient")
}

// resolveAdminUpstreamDisable returns "#" to comment out admin upstream block if no servers configured
// Returns "" (empty string) if servers are configured to enable the upstream block
func (g *Generator) resolveAdminUpstreamDisable(ctx context.Context) (any, error) {
	return g.resolveUpstreamDisable(ctx, "zimbraReverseProxyUpstreamAdminServers", "Admin")
}
