// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import "context"

// resolveClientCertEnabler enables the SSO blocks when the global client
// certificate mode verifies client certificates. The attribute holds
// on/off/optional/optional_no_ca, which the TRUE-only enabler conversion would
// always read as disabled; this matches the per-domain path (clientCertVerifies).
func (g *Generator) resolveClientCertEnabler(_ context.Context) (any, error) {
	mode, ok := g.getConfigValue("zimbraReverseProxyClientCertMode", sourceGlobal)

	return ok && clientCertVerifies(mode), nil
}

// registerSSOVariables registers SSO (Single Sign-On) configuration variables
func (g *Generator) registerSSOVariables() {
	g.registerVar("web.sso.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withCustomResolver(g.resolveClientCertEnabler),
		withDescription("Enable SSO configuration blocks"),
	)
	g.registerVar("web.sso.default.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withCustomResolver(g.resolveClientCertEnabler),
		withDescription("Enable SSO configuration for default virtual host"),
	)
	g.registerVar("web.sso.certauth.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withCustomResolver(g.resolveClientCertEnabler),
		withDescription("Enable certificate-based authentication for SSO"),
	)
	g.registerVar("web.sso.certauth.default.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withCustomResolver(g.resolveClientCertEnabler),
		withDescription("Enable certificate-based authentication for SSO on default virtual host"),
	)
	g.registerVar("web.sso.certauth.port", 0,
		withAttribute("zimbraMailSSLProxyClientCertPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideServer),
		withDescription("Port for SSO certificate authentication"),
	)
}
