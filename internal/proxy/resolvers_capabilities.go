// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

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
