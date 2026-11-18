// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// pop3ExpireCapability is the POP3 capability string for message expiry.
const pop3ExpireCapability = "EXPIRE 31 USER"

// IMAP/POP3 capability constants
const (
	imapCapIMAPRev1  = "IMAP4rev1"
	imapCapID        = "ID"
	imapCapLiteral   = "LITERAL+"
	imapCapTOP       = "TOP"
	imapCapIDLE      = "IDLE"
	imapCapUIDL      = "UIDL"
	imapCapUSER      = "USER"
	imapCapSASLIR    = "SASL-IR"
	imapCapNAMESPACE = "NAMESPACE"
	loopbackIPv4     = "127.0.0.1"
)

// defaultPOP3Capabilities is the default set of POP3 capabilities (matching Java ProxyConfGen).
var defaultPOP3Capabilities = []string{pop3ExpireCapability, imapCapTOP, imapCapUIDL, imapCapUSER, "XOIP"}

// compressionMIMETypes is the shared list of MIME types for gzip and brotli compression.
// Entries are nested 4 spaces under the directive that owns the list (relative,
// the renderer adds the placeholder's column). The trailing blank line mirrors
// the legacy installed ProxyCompressionServerVar output.
const compressionMIMETypes = `    application/atom+xml
    application/geo+json
    application/javascript
    application/x-javascript
    application/json
    application/graphql-response+json
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

// resolveLookupHandlers constructs the nginx zm_lookup_handlers value as a
// space-separated list of "https://<host>:<port>/service/extension/nginx-lookup"
// URLs, one per lookup target.
func (g *Generator) resolveLookupHandlers(ctx context.Context) (any, error) {
	targets, err := g.lookupTargets(ctx)
	if err != nil {
		return nil, err
	}

	if len(targets) == 0 {
		host := loopbackIPv4

		if val, ok := g.getConfigValue("zimbra_server_hostname", sourceLocal); ok {
			if hosts := validLookupHosts(ctx, []string{val}); len(hosts) != 0 {
				host = hosts[0]
			}
		}

		targets = []lookupTarget{{host: host}}
	}

	globalPort, _ := g.getConfigValue(extensionBindPortAttr, sourceGlobal)
	defaultPort := lookupPort(ctx, globalPort, "7072")
	urls := make([]string, 0, len(targets))

	for _, target := range targets {
		urls = append(urls, fmt.Sprintf("https://%s:%s/service/extension/nginx-lookup",
			resolveHostToIP(ctx, target.host), lookupPort(ctx, target.port, defaultPort)))
	}

	return strings.Join(urls, " "), nil
}

const extensionBindPortAttr = "zimbraExtensionBindPort"

type lookupTarget struct {
	host string
	port string // Empty means inherit the global extension port.
}

func lookupPort(ctx context.Context, port, fallback string) string {
	if port != "" {
		if validPort(port) {
			return port
		}

		logger.ErrorContext(ctx, "Ignoring invalid extension bind port", "port", truncateForLog(port))
	}

	return fallback
}

// lookupTargets respects an explicit list without requiring LDAP. Discovery
// uses the extension listener, independent of the mailbox's HTTP(S) port.
func (g *Generator) lookupTargets(ctx context.Context) ([]lookupTarget, error) {
	rawHosts, _ := g.getConfigValue("zimbraReverseProxyAvailableLookupTargets", sourceServer, sourceGlobal)
	if strings.TrimSpace(rawHosts) != "" {
		hosts := validLookupHosts(ctx, strings.Fields(normalizeMultiValue(rawHosts)))
		if len(hosts) == 0 {
			return nil, fmt.Errorf("zimbraReverseProxyAvailableLookupTargets has no valid host")
		}

		targets := make([]lookupTarget, 0, len(hosts))
		for _, host := range hosts {
			targets = append(targets, lookupTarget{host: host})
		}

		return targets, nil
	}

	byHost, err := g.getServerAttrsByHostname(ctx)
	if err != nil {
		return nil, fmt.Errorf("query lookup target servers: %w", err)
	}

	var targets []lookupTarget

	for _, host := range sortedKeys(byHost) {
		attrs := byHost[host]
		if attrs[zimbraServiceHostnameAttr] != "" && strings.EqualFold(attrs[zimbraReverseProxyLookupTargetAttr], "TRUE") {
			targets = append(targets, lookupTarget{host: host, port: attrs[extensionBindPortAttr]})
		}
	}

	return targets, nil
}

// validLookupHosts drops the hosts that are not plain host tokens, since they
// are spliced into the zm_lookup_handlers URLs.
func validLookupHosts(ctx context.Context, hosts []string) []string {
	return slices.DeleteFunc(hosts, func(host string) bool {
		if validHostToken(host) {
			return false
		}

		logger.ErrorContext(ctx, "Skipping invalid lookup target host", "host", truncateForLog(host))

		return true
	})
}

// hostLookupTimeout bounds the DNS lookup of one lookup target.
const hostLookupTimeout = 5 * time.Second

// resolveHostToIP prefers IPv4, brackets IPv6 for URLs, and keeps the hostname if DNS fails.
func resolveHostToIP(ctx context.Context, host string) string {
	ctx, cancel := context.WithTimeout(ctx, hostLookupTimeout)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err == nil && len(ips) > 0 {
		ip := ips[0].IP

		for _, candidate := range ips {
			if candidate.IP.To4() != nil {
				ip = candidate.IP

				break
			}
		}

		if ip.To4() == nil {
			return "[" + ip.String() + "]"
		}

		return ip.String()
	}

	logger.WarnContext(ctx, "Failed to resolve lookup host to IP, using hostname",
		"hostname", host,
		"error", err)

	return host
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
		imapCapIMAPRev1, imapCapID, imapCapLiteral, imapCapSASLIR, imapCapIDLE, imapCapNAMESPACE,
		"ACL", "BINARY", "CATENATE", "CHILDREN", "CONDSTORE", "ENABLE",
		"ESEARCH", "ESORT", "I18NLEVEL=1", "LIST-EXTENDED", "LIST-STATUS",
		"MULTIAPPEND", "QRESYNC", "QUOTA", "RIGHTS=ektx", "SEARCHRES",
		"SORT", "THREAD=ORDEREDSUBJECT", "UIDPLUS", "UNSELECT", "WITHIN", "XLIST",
	}

	// Try to read from GlobalConfig (multi-valued attribute)
	if val, ok := g.getConfigValue("zimbraReverseProxyImapEnabledCapability", sourceServer, sourceGlobal); ok {
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

	for _, capability := range caps {
		// Capabilities come from LDAP and are emitted inside double quotes;
		// a quote, backslash or control character would break out of the
		// nginx string and let the value inject directives.
		if strings.ContainsAny(capability, "\"\\\r\n\x00;") {
			continue
		}

		sb.WriteString(` "`)
		sb.WriteString(capability)
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
	val, ok := g.getConfigValue("zimbraReverseProxyPop3EnabledCapability", sourceServer, sourceGlobal)
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

// resolveIMAPId constructs the IMAP ID extension value, matching the legacy
// mail.imapid default: "NAME" "Zimbra" "VERSION" "<BuildInfo.VERSION>" "RELEASE"
// "<BuildInfo.RELEASE>" (ProxyConfGen.java mail.imapid). The values come from the
// buildInfo.properties resource of mailbox.jar (see buildInfo).
func (g *Generator) resolveIMAPId(_ context.Context) (any, error) {
	version, release := g.buildInfo()

	//nolint:gocritic // sprintfQuotedString: We need literal quotes in the output, not Go-escaped quotes
	return fmt.Sprintf(`"NAME" "Zimbra" "VERSION" "%s" "RELEASE" "%s"`, version, release), nil
}

// resolveGreeting returns a protocol greeting banner with version if the given attribute is enabled.
func (g *Generator) resolveGreeting(attribute, format string) (any, error) {
	exposeVersion := false

	if val, ok := g.getConfigValue(attribute, sourceServer, sourceGlobal); ok {
		exposeVersion = config.IsTruthy(val)
	}

	if exposeVersion {
		version, _ := g.buildInfo()

		return fmt.Sprintf(format, version), nil
	}

	return "", nil
}

// resolveIMAPGreeting returns IMAP greeting banner with version if enabled
func (g *Generator) resolveIMAPGreeting(_ context.Context) (any, error) {
	return g.resolveGreeting(
		"zimbraReverseProxyImapExposeVersionOnBanner",
		"* OK Zimbra %s IMAP4 ready")
}

// resolvePOP3Greeting returns POP3 greeting banner with version if enabled
func (g *Generator) resolvePOP3Greeting(_ context.Context) (any, error) {
	return g.resolveGreeting(
		"zimbraReverseProxyPop3ExposeVersionOnBanner",
		"+OK Zimbra %s POP3 ready")
}
