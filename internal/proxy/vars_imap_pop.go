// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerIMAPPOPVariables registers IMAP/POP proxy configuration variables
func (g *Generator) registerIMAPPOPVariables() {
	g.registerVar("mail.imap.greeting", "",
		withAttribute("zimbraReverseProxyImapExposeVersionOnBanner"),
		withOverrideType(OverrideCustom),
		withDescription("IMAP greeting banner (with version if enabled)"),
		withCustomResolver(g.resolveIMAPGreeting),
	)
	g.registerVar("mail.imap.enabled_capability", "",
		withAttribute("zimbraReverseProxyImapEnabledCapability"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("Enabled IMAP capabilities to advertise"),
	)
	g.registerVar("mail.imap.starttls", "only",
		withAttribute("zimbraReverseProxyImapStartTlsMode"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("IMAP STARTTLS mode (on, off, only)"),
	)
	g.registerSASLConfigVar("mail.imap.sasl.plain.enabled", "zimbraReverseProxyImapSaslPlainEnabled",
		true, "Enable IMAP SASL PLAIN authentication")
	g.registerSASLConfigVar("mail.imap.sasl.gssapi.enabled", "zimbraReverseProxyImapSaslGssapiEnabled",
		false, "Enable IMAP SASL GSSAPI authentication")
	g.registerVar("mail.pop3.greeting", "",
		withAttribute("zimbraReverseProxyPop3ExposeVersionOnBanner"),
		withOverrideType(OverrideCustom),
		withDescription("POP3 greeting banner (with version if enabled)"),
		withCustomResolver(g.resolvePOP3Greeting),
	)
	g.registerVar("mail.pop3.enabled_capability", "",
		withAttribute("zimbraReverseProxyPop3EnabledCapability"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("Enabled POP3 capabilities to advertise"),
	)
	g.registerVar("mail.pop3.starttls", "only",
		withAttribute("zimbraReverseProxyPop3StartTlsMode"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("POP3 STARTTLS mode (on, off, only)"),
	)
	g.registerSASLConfigVar("mail.pop3.sasl.plain.enabled", "zimbraReverseProxyPop3SaslPlainEnabled",
		true, "Enable POP3 SASL PLAIN authentication")
	g.registerSASLConfigVar("mail.pop3.sasl.gssapi.enabled", "zimbraReverseProxyPop3SaslGssapiEnabled",
		false, "Enable POP3 SASL GSSAPI authentication")
	g.registerVar("mail.upstream.pop3xoip", true,
		withAttribute("zimbraReverseProxySendPop3Xoip"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Send XOIP (X-Originating-IP) for POP3 connections"),
	)
	g.registerVar("mail.saslhost.from.ip", "off",
		withAttribute("zimbraReverseProxySaslHostFromIP"),
		withOverrideType(OverrideCustom),
		withDescription("SASL hostname from IP address configuration"),
		withCustomResolver(g.resolveSaslHostFromIP),
	)
	g.registerVar("mail.imapcapa", []string{"IMAP4rev1", "ID", "LITERAL+", "SASL-IR", "IDLE", "NAMESPACE"},
		withAttribute("zimbraReverseProxyImapEnabledCapability"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("IMAP capability list to advertise"),
		withCustomResolver(g.resolveIMAPCapabilities),
		withCustomFormatter(formatIMAPCapabilities),
	)
	g.registerVar("mail.pop3capa", []string{"TOP", "USER", "UIDL", "EXPIRE 31 USER"},
		withAttribute("zimbraReverseProxyPop3EnabledCapability"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("POP3 capability list to advertise"),
		withCustomResolver(g.resolvePOP3Capabilities),
		withCustomFormatter(formatPOP3Capabilities),
	)
	g.registerVar("mail.imapid", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideConfig),
		withDescription("IMAP ID extension value (key-value pairs)"),
		withCustomResolver(g.resolveIMAPId),
	)
	g.registerAuthEnablerVar("mail.imap.authplain.enabled", "zimbraReverseProxyImapSaslPlainEnabled",
		true, "Whether SASL PLAIN is enabled for IMAP")
	g.registerAuthEnablerVar("mail.imap.authgssapi.enabled", "zimbraReverseProxyImapSaslGssapiEnabled",
		false, "Whether SASL GSSAPI is enabled for IMAP")
	g.registerAuthEnablerVar("mail.pop3.authplain.enabled", "zimbraReverseProxyPop3SaslPlainEnabled",
		true, "Whether SASL PLAIN is enabled for POP3")
	g.registerAuthEnablerVar("mail.pop3.authgssapi.enabled", "zimbraReverseProxyPop3SaslGssapiEnabled",
		false, "Whether SASL GSSAPI is enabled for POP3")
	g.registerVar("mail.imap.literalauth", true,
		withAttribute("zimbraReverseProxyImapLiteralAuth"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Enable IMAP literal authentication"),
	)
}

// registerSASLConfigVar registers a SASL enabled/disabled config variable
// (ValueTypeBoolean, OverrideConfig) for IMAP or POP3.
func (g *Generator) registerSASLConfigVar(key, attr string, defaultVal bool, desc string) {
	g.registerVar(key, defaultVal,
		withAttribute(attr),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription(desc),
	)
}

// registerAuthEnablerVar registers an auth enabler variable
// (ValueTypeEnabler, OverrideServer) for IMAP or POP3.
func (g *Generator) registerAuthEnablerVar(key, attr string, defaultVal bool, desc string) {
	g.registerVar(key, defaultVal,
		withAttribute(attr),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideServer),
		withDescription(desc),
	)
}
