// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerXMPPVariables registers XMPP/Bosh configuration variables
func (g *Generator) registerXMPPVariables() {
	g.registerVar("web.xmpp.bosh.hostname", "",
		withAttribute("zimbraReverseProxyXmppBoshHostname"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("XMPP Bosh hostname for chat functionality"),
	)
	g.registerVar("web.xmpp.bosh.port", 5222,
		withAttribute("zimbraReverseProxyXmppBoshPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("XMPP Bosh port number"),
	)
	g.registerVar("web.xmpp.local.bind.url", "",
		withAttribute("zimbraReverseProxyXmppBoshLocalHttpBindURL"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("Local HTTP bind URL for XMPP Bosh"),
	)
	g.registerVar("web.xmpp.remote.bind.url", "",
		withAttribute("zimbraReverseProxyXmppBoshRemoteHttpBindURL"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("Remote HTTP bind URL for XMPP Bosh"),
	)
}
