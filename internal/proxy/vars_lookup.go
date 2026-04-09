// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerLookupVariables registers lookup service configuration variables
func (g *Generator) registerLookupVariables() {
	g.registerVar("lookup.target", "",
		withAttribute("zimbraReverseProxyLookupTarget"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideConfig),
		withDescription("Lookup target for route lookup service"),
	)
	g.registerVar("lookup.target.available", false,
		withAttribute("zimbraReverseProxyLookupTarget"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideCustom),
		withDescription("Whether lookup target is configured and available"),
		withCustomResolver(g.resolveLookupTargetAvailable),
	)
	g.registerVar("lookup.caching.enabled", true,
		withAttribute("zimbraReverseProxyZmlookupCachingEnabled"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Enable caching for route lookups"),
	)
	g.registerVar("zmlookup.:handlers", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("List of available lookup handler servers"),
		withCustomResolver(g.resolveLookupHandlers),
	)
	g.registerVar("zmlookup.timeout", "15000ms",
		withAttribute("zimbraReverseProxyRouteLookupTimeout"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for route lookup operations"),
	)
	g.registerVar("zmlookup.retryinterval", "60000ms",
		withValueType(ValueTypeTime),
		withDescription("Interval to retry failed lookup handlers"),
	)
	g.registerVar("zmlookup.caching", true,
		withAttribute("zimbraReverseProxyZmlookupCachingEnabled"),
		withValueType(ValueTypeBoolean),
		withOverrideType(OverrideConfig),
		withDescription("Enable lookup result caching"),
	)
	g.registerVar("zmlookup.dpasswd", "",
		withAttribute("ldap_nginx_password"),
		withValueType(ValueTypeString),
		withOverrideType(OverrideLocalConfig),
		withDescription("Master authentication password for lookup service"),
	)
	g.registerVar("zmprefix.url", "https://carbonio.carbonio-system.svc.cluster.local",
		withAttribute("zimbraMailURL"),
		withOverrideType(OverrideConfig),
		withValueType(ValueTypeString),
		withDescription("URL prefix for upstream servers"),
	)
	g.registerVar("zmroute.timeout.connect", "5s",
		withAttribute("zimbraReverseProxyRouteLookupTimeoutConnect"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for connecting to route lookup service"),
	)
	g.registerVar("zmroute.timeout.read", "60s",
		withAttribute("zimbraReverseProxyRouteLookupTimeoutRead"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for reading from route lookup service"),
	)
	g.registerVar("zmroute.timeout.send", "60s",
		withAttribute("zimbraReverseProxyRouteLookupTimeoutSend"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for sending to route lookup service"),
	)
}
