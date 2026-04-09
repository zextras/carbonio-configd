// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerTimeoutVariables registers timeout and performance configuration variables
func (g *Generator) registerTimeoutVariables() {
	g.registerVar("mail.authwait", 10000,
		withAttribute("zimbraReverseProxyAuthWaitInterval"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Time to wait before sending auth request to upstream"),
	)
	g.registerVar("mail.inactivity.timeout", "1h",
		withAttribute("zimbraReverseProxyInactivityTimeout"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Inactivity timeout for mail connections"),
	)
	g.registerVar("web.upstream.connect.timeout", 25,
		withAttribute("zimbraReverseProxyUpstreamConnectTimeout"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for connecting to upstream servers (in seconds)"),
	)
	g.registerVar("web.upstream.read.timeout", 60000,
		withAttribute("zimbraReverseProxyUpstreamReadTimeout"),
		withValueType(ValueTypeTimeInSec),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for reading from upstream servers"),
	)
	g.registerVar("web.upstream.send.timeout", 60000,
		withAttribute("zimbraReverseProxyUpstreamSendTimeout"),
		withValueType(ValueTypeTimeInSec),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for sending to upstream servers"),
	)
	g.registerVar("web.upstream.polling.timeout", 3600000,
		withAttribute("zimbraReverseProxyUpstreamPollingTimeout"),
		withValueType(ValueTypeTimeInSec),
		withOverrideType(OverrideConfig),
		withDescription("The response timeout for Microsoft Active Sync polling"),
	)
	g.registerVar("lookup.timeout", "15s",
		withAttribute("zimbraReverseProxyRouteLookupTimeout"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for route lookup requests"),
	)
	g.registerVar("lookup.retryinterval", "60s",
		withAttribute("zimbraReverseProxyCacheReconnectInterval"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Interval to retry connecting to route lookup cache"),
	)
	g.registerVar("lookup.dpasswd.cachettl", "1h",
		withAttribute("zimbraReverseProxyCacheEntryTTL"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Time-to-live for route lookup cache entries"),
	)
	g.registerVar("lookup.cachefetchtimeout", "10s",
		withAttribute("zimbraReverseProxyCacheFetchTimeout"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for fetching from route lookup cache"),
	)
	g.registerVar("web.upstream.noop.timeout", 1220,
		withAttribute("zimbra_noop_max_timeout"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("Response timeout for NoOpRequest (base + 20s offset)"),
		withCustomResolver(g.makeTimeoutResolver("zimbra_noop_max_timeout", 1200, 20)),
	)
	g.registerVar("web.upstream.waitset.timeout", 1220,
		withAttribute("zimbra_waitset_max_request_timeout"),
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("Response timeout for WaitSetRequest (base + 20s offset)"),
		withCustomResolver(g.makeTimeoutResolver("zimbra_waitset_max_request_timeout", 1200, 20)),
	)
}
