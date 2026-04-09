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
	g.registerVar("admin.upstream.:servers", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("List of upstream servers for admin console"),
		withCustomResolver(g.makeBackendResolver(false)),
	)
	g.registerVar("admin.console.proxy.port", 9071,
		withAttribute("zimbraAdminProxyPort"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Admin console proxy port"),
	)
	g.registerVar("admin.console.upstream.adminclient.:servers", []string{},
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("List of upstream admin client servers"),
		withCustomResolver(g.makeBackendResolver(false)),
	)
}
