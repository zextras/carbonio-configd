// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerAdminVariables registers admin console configuration variables
func (g *Generator) registerAdminVariables() {
	g.registerVar("admin.console.upstream.name", "zimbra_admin",
		withValueType(ValueTypeString),
		withDescription("Upstream name for admin console"),
	)
	g.registerUpstreamServersVar("admin.upstream.:servers", "List of upstream servers for admin console",
		&upstreamSpec{
			services:        []string{serviceMailbox, serviceMailclient},
			portAttrKey:     attrAdminPortAttribute,
			portAttrDefault: defaultAdminPortAttr,
		})
	g.registerVar("admin.console.proxy.port", 9071,
		withAttribute("zimbraAdminProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console proxy port"),
	)
	g.registerUpstreamServersVar("admin.console.upstream.adminclient.:servers", "List of upstream admin client servers",
		&upstreamSpec{
			services:        []string{serviceMailbox, serviceAdminclient},
			portAttrKey:     attrAdminPortAttribute,
			portAttrDefault: defaultAdminPortAttr,
		})
}
