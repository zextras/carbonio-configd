// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"fmt"
	"strings"
)

// registerMailVariables registers mail proxy configuration variables
func (g *Generator) registerMailVariables() {
	g.registerVar("mail.enabled", false,
		withAttribute("zimbraReverseProxyMailEnabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether mail proxy is enabled"),
	)
	g.registerVar("mail.imap.port", 143,
		withAttribute("zimbraImapProxyBindPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("IMAP port for mail proxy"),
	)
	g.registerVar("mail.imaps.port", 993,
		withAttribute("zimbraImapSSLProxyBindPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("IMAPS port for mail proxy"),
	)
	g.registerVar("mail.pop3.port", 110,
		withAttribute("zimbraPop3ProxyBindPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("POP3 port for mail proxy"),
	)
	g.registerVar("mail.pop3s.port", 995,
		withAttribute("zimbraPop3SSLProxyBindPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("POP3S port for mail proxy"),
	)
	g.registerVar("mail.imap.enabled", true,
		withAttribute("zimbraReverseProxyMailImapEnabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether IMAP proxy is enabled"),
	)
	g.registerVar("mail.imaps.enabled", true,
		withAttribute("zimbraReverseProxyMailImapsEnabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether IMAPS proxy is enabled"),
	)
	g.registerVar("mail.pop3.enabled", true,
		withAttribute("zimbraReverseProxyMailPop3Enabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether POP3 proxy is enabled"),
	)
	g.registerVar("mail.pop3s.enabled", true,
		withAttribute("zimbraReverseProxyMailPop3sEnabled"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Indicates whether POP3S proxy is enabled"),
	)
	g.registerVar("mail.mode", "https",
		withAttribute("zimbraReverseProxyMailMode"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Mail proxy mode (http, https, both, redirect, mixed)"),
	)
	g.registerVar("mail.defaultrealm", "",
		withAttribute("zimbraReverseProxyDefaultRealm"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("Default realm for mail proxy authentication"),
	)
	g.registerVar("mail.passerrors", true,
		withAttribute("zimbraReverseProxyPassErrors"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Pass authentication errors to mail clients"),
	)
	g.registerVar("mail.imap.proxytimeout", 2100,
		withAttribute("imap_authenticated_max_idle_time"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("IMAP network timeout after authentication (base + 300s offset)"),
		withCustomResolver(g.makeTimeoutResolver("imap_authenticated_max_idle_time", 1800, 300)),
	)
	g.registerVar("mail.ctimeout", 120000,
		withAttribute("zimbraReverseProxyConnectTimeout"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideServer),
		withDescription("Time interval (ms) after which a POP/IMAP proxy connection to a remote host will give up"),
	)
	g.registerVar("mail.usermax", 0,
		withAttribute("zimbraReverseProxyUserLoginLimit"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Maximum login attempts per user (0 = no limit)"),
	)
	g.registerVar("mail.userttl", 3600000,
		withAttribute("zimbraReverseProxyUserLoginLimitTime"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Time interval (ms) after which User Login Counter is reset"),
	)
	g.registerVar("mail.userrej", "Login rejected for this user",
		withAttribute("zimbraReverseProxyUserThrottleMsg"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Rejection message for User throttle"),
	)
	g.registerVar("mail.whitelist.ttl", 300000,
		withAttribute("zimbraReverseProxyIPThrottleWhitelistTime"),
		withValueType(ValueTypeTimeInSec),
		withOverrideType(OverrideConfig),
		withDescription("Time-to-live, in seconds, for an entry in the whitelist table"),
	)
	g.registerVar("mail.upstream.imapid", true,
		withAttribute("zimbraReverseProxySendImapId"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Issue IMAP ID to upstream servers"),
	)
	g.registerVar("mail.proxy.ssl", true,
		withAttribute("zimbraReverseProxySSLToUpstreamEnabled"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideServer),
		withDescription("Indicates whether using SSL to connect to upstream mail server"),
	)
	g.registerVar("mail.ssl.preferserverciphers", true,
		withAttribute("zimbraReverseProxySSLPreferServerCiphers"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Prefer server cipher order over client"),
	)
	g.registerVar("mail.ssl.protocols", "TLSv1.2 TLSv1.3",
		withAttribute("zimbraReverseProxySSLProtocols"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideServer),
		withDescription("Enabled SSL/TLS protocol versions for mail proxy (space-separated)"),
		withCustomResolver(g.resolveWebSSLProtocols),
		withCustomFormatter(func(val any) (string, error) {
			if arr, ok := val.([]string); ok {
				return " " + strings.Join(arr, " "), nil
			}

			return fmt.Sprintf(" %v", val), nil
		}),
	)
	g.registerVar("mail.ssl.ciphers", "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256",
		withAttribute("zimbraReverseProxySSLCiphers"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("SSL cipher suite configuration"),
	)
	g.registerVar("mail.ssl.ecdh.curve", "auto",
		withAttribute("zimbraReverseProxySSLECDHCurve"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("ECDH curve for key exchange"),
	)
	g.registerVar("mail.saslapp", "nginx",
		withAttribute("zimbraReverseProxySaslApp"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("SASL application name for authentication"),
	)
	g.registerVar("mail.sasl_host_from_ip", false,
		withAttribute("zimbraReverseProxySaslHostFromIP"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Use client IP address as SASL host"),
	)
	g.registerThrottleLimitVar("mail.imapmax", "zimbraReverseProxyIPLoginImapLimit",
		"IMAP Login Limit (Throttle) - 0 means infinity")
	g.registerThrottleLimitVar("mail.pop3max", "zimbraReverseProxyIPLoginPop3Limit",
		"POP3 Login Limit (Throttle) - 0 means infinity")
	g.registerThrottleLimitVar("mail.ipmax", "zimbraReverseProxyIPLoginLimit",
		"IP Login Limit (Throttle) - 0 means infinity")
	g.registerThrottleTTLVar("mail.imapttl", "zimbraReverseProxyIPLoginImapLimitTime",
		"Time interval (ms) after which IMAP Login Counter is reset")
	g.registerThrottleTTLVar("mail.pop3ttl", "zimbraReverseProxyIPLoginPop3LimitTime",
		"Time interval (ms) after which POP3 Login Counter is reset")
	g.registerThrottleTTLVar("mail.ipttl", "zimbraReverseProxyIPLoginLimitTime",
		"Time interval (ms) after which IP Login Counter is reset")
	g.registerVar("mail.iprej", "Login rejected from this IP",
		withAttribute("zimbraReverseProxyIpThrottleMsg"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("Rejection message for IP throttle"),
	)
}

// registerThrottleLimitVar registers a login throttle limit variable (ValueTypeInteger, OverrideConfig, default 0).
func (g *Generator) registerThrottleLimitVar(key, attr, desc string) {
	g.registerVar(key, 0,
		withAttribute(attr),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription(desc),
	)
}

// registerThrottleTTLVar registers a login throttle TTL variable (ValueTypeTime, OverrideConfig, default 3600000ms).
func (g *Generator) registerThrottleTTLVar(key, attr, desc string) {
	g.registerVar(key, 3600000,
		withAttribute(attr),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription(desc),
	)
}
