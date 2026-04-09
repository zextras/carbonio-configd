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
	g.registerVar("memcache.timeout", "3000",
		withAttribute("zimbraMemcachedClientTimeoutMillis"),
		withValueType(ValueTypeInteger),
		withOverrideType(OverrideConfig),
		withDescription("Timeout for memcache connections (in milliseconds, no unit suffix)"),
	)
	g.registerVar("memcache.reconnect", "60000ms",
		withValueType(ValueTypeTime),
		withDescription("Memcache server reconnect interval"),
	)
	g.registerVar("memcache.ttl", "3600000ms",
		withValueType(ValueTypeTime),
		withDescription("Memcache entry time-to-live"),
	)
	g.registerVar("memcache.servers", []string{},
		withOverrideType(OverrideConfig),
		withDescription("List of memcached servers for route caching"),
		withCustomResolver(g.resolveMemcacheServers),
	)
}
