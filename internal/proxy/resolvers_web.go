// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

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
