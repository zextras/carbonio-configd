// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

// registerMemcacheVariables registers memcache configuration variables
func (g *Generator) registerMemcacheVariables() {
	g.registerVar("memcache.:servers", "",
		withValueType(ValueTypeCustom),
		withOverrideType(OverrideCustom),
		withDescription("List of memcache servers"),
		withCustomResolver(g.resolveMemcacheServers),
	)
	g.registerVar("memcache.timeout", "3000ms",
		withAttribute("zimbraReverseProxyCacheFetchTimeout"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Time given to a cache-fetch operation to complete"),
	)
	g.registerVar("memcache.reconnect", "60000ms",
		withAttribute("zimbraReverseProxyCacheReconnectInterval"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Memcache server reconnect interval"),
	)
	g.registerVar("memcache.ttl", "3600000ms",
		withAttribute("zimbraReverseProxyCacheEntryTTL"),
		withValueType(ValueTypeTime),
		withOverrideType(OverrideConfig),
		withDescription("Memcache entry time-to-live"),
	)
	g.registerVar("memcache.servers", []string{},
		withOverrideType(OverrideConfig),
		withDescription("List of memcached servers for route caching"),
		withCustomResolver(g.resolveMemcacheServers),
	)
}
