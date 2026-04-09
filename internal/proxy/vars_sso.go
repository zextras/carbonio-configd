// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerSSOVariables registers SSO (Single Sign-On) configuration variables
func (g *Generator) registerSSOVariables() {
	g.registerVar("web.sso.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Enable SSO configuration blocks"),
	)
	g.registerVar("web.sso.default.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Enable SSO configuration for default virtual host"),
	)
	g.registerVar("web.sso.certauth.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Enable certificate-based authentication for SSO"),
	)
	g.registerVar("web.sso.certauth.default.enabled", false,
		withAttribute("zimbraReverseProxyClientCertMode"),
		withValueType(ValueTypeEnabler),
		withOverrideType(OverrideConfig),
		withDescription("Enable certificate-based authentication for SSO on default virtual host"),
	)
	g.registerVar("web.sso.certauth.port", 9443,
		withAttribute("zimbraMailProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideServer),
		withDescription("Port for SSO certificate authentication"),
	)
}
