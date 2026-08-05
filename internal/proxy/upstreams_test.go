// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - upstream server discovery tests
package proxy

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
)

// TestBuildReverseProxyBackends tests filtering/building of reverse proxy
// backends from a structured hostname->attrs map (as returned by
// getServerAttrsByHostname).
func TestBuildReverseProxyBackends(t *testing.T) {
	tests := []struct {
		name     string
		byHost   map[string]map[string]string
		expected []UpstreamServer
	}{
		{
			name: "single server with lookup target true",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "http",
					zimbraMailPortAttr:                 "8080",
					zimbraMailSSLPortAttr:               "8443",
				},
			},
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
			},
		},
		{
			name: "server with lookup target false",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "FALSE",
					zimbraMailModeAttr:                 "http",
					zimbraMailPortAttr:                 "8080",
				},
			},
			expected: nil,
		},
		{
			name: "server with https mode uses SSL port",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "https",
					zimbraMailPortAttr:                 "8080",
					zimbraMailSSLPortAttr:               "8443",
				},
			},
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8443},
			},
		},
		{
			name: "server with mixed mode uses HTTP port",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "mixed",
					zimbraMailPortAttr:                 "8080",
					zimbraMailSSLPortAttr:               "8443",
				},
			},
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
			},
		},
		{
			name: "server with both mode uses HTTP port",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "both",
					zimbraMailPortAttr:                 "8080",
					zimbraMailSSLPortAttr:               "8443",
				},
			},
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
			},
		},
		{
			name: "multiple servers, mixed lookup targets, sorted by hostname",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "http",
					zimbraMailPortAttr:                 "8080",
					zimbraMailSSLPortAttr:               "8443",
				},
				"server2.example.com": {
					zimbraServiceHostnameAttr:          "server2.example.com",
					zimbraReverseProxyLookupTargetAttr: "FALSE",
					zimbraMailModeAttr:                 "http",
					zimbraMailPortAttr:                 "8080",
				},
				"server3.example.com": {
					zimbraServiceHostnameAttr:          "server3.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "https",
					zimbraMailPortAttr:                 "80",
					zimbraMailSSLPortAttr:               "443",
				},
			},
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
				{Host: "server3.example.com", Port: 443},
			},
		},
		{
			name: "default ports when not specified",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "http",
				},
			},
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 80},
			},
		},
		{
			name: "default SSL port",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:          "server1.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "https",
				},
			},
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 443},
			},
		},
		{
			name:     "no servers",
			byHost:   map[string]map[string]string{},
			expected: nil,
		},
		{
			name: "server missing zimbraServiceHostname is excluded",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "http",
					zimbraMailPortAttr:                 "8080",
				},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Generator{}
			result := buildReverseProxyBackends(tt.byHost, g.buildUpstreamServer)

			if len(result) != len(tt.expected) {
				t.Fatalf("buildReverseProxyBackends() returned %d servers, expected %d: got %+v, expected %+v",
					len(result), len(tt.expected), result, tt.expected)
			}

			for i, server := range result {
				if server.Host != tt.expected[i].Host || server.Port != tt.expected[i].Port {
					t.Errorf("buildReverseProxyBackends() server[%d] = %+v, expected %+v",
						i, server, tt.expected[i])
				}
			}
		})
	}
}

// TestBuildUpstreamServer tests building upstream servers based on mail mode
func TestBuildUpstreamServer(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		attrs    map[string]string
		expected UpstreamServer
	}{
		{
			name:     "http mode uses mail port",
			hostname: "server1.example.com",
			attrs:    map[string]string{zimbraMailModeAttr: "http", zimbraMailPortAttr: "8080", zimbraMailSSLPortAttr: "8443"},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8080},
		},
		{
			name:     "https mode uses SSL port",
			hostname: "server1.example.com",
			attrs:    map[string]string{zimbraMailModeAttr: "https", zimbraMailPortAttr: "8080", zimbraMailSSLPortAttr: "8443"},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
		{
			name:     "mixed mode uses mail port",
			hostname: "server1.example.com",
			attrs:    map[string]string{zimbraMailModeAttr: "mixed", zimbraMailPortAttr: "8080", zimbraMailSSLPortAttr: "8443"},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8080},
		},
		{
			name:     "both mode uses mail port",
			hostname: "server1.example.com",
			attrs:    map[string]string{zimbraMailModeAttr: "both", zimbraMailPortAttr: "8080", zimbraMailSSLPortAttr: "8443"},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8080},
		},
		{
			name:     "redirect mode uses SSL port",
			hostname: "server1.example.com",
			attrs:    map[string]string{zimbraMailModeAttr: "redirect", zimbraMailPortAttr: "8080", zimbraMailSSLPortAttr: "8443"},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
		{
			name:     "unknown mode uses SSL port",
			hostname: "server1.example.com",
			attrs:    map[string]string{zimbraMailModeAttr: "unknown", zimbraMailPortAttr: "8080", zimbraMailSSLPortAttr: "8443"},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
		{
			name:     "empty mode uses SSL port",
			hostname: "server1.example.com",
			attrs:    map[string]string{zimbraMailModeAttr: "", zimbraMailPortAttr: "8080", zimbraMailSSLPortAttr: "8443"},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
	}

	g := &Generator{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := g.buildUpstreamServer(tt.hostname, tt.attrs)

			if result.Host != tt.expected.Host || result.Port != tt.expected.Port {
				t.Errorf("buildUpstreamServer() = %+v, expected %+v", result, tt.expected)
			}
		})
	}
}

// TestBuildUpstreamServerSSL tests buildUpstreamServerSSL always uses SSL port
func TestBuildUpstreamServerSSL(t *testing.T) {
	g := &Generator{}
	attrs := map[string]string{
		zimbraMailModeAttr:    "http", // even with http mode, SSL method uses SSL port
		zimbraMailPortAttr:    "8080",
		zimbraMailSSLPortAttr: "8443",
	}
	result := g.buildUpstreamServerSSL("server1.example.com", attrs)
	if result.Port != 8443 {
		t.Errorf("expected SSL port 8443, got %d", result.Port)
	}
	if result.Host != "server1.example.com" {
		t.Errorf("expected server1.example.com, got %q", result.Host)
	}
}

// TestBuildMemcachedServers tests filtering/building of memcached servers
// from a structured hostname->attrs map.
func TestBuildMemcachedServers(t *testing.T) {
	tests := []struct {
		name     string
		byHost   map[string]map[string]string
		expected []MemcacheServer
	}{
		{
			name: "single memcached server",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:   "server1.example.com",
					zimbraServiceEnabledAttr:    "memcached",
					zimbraMemcachedBindPortAttr: "11211",
				},
			},
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
			},
		},
		{
			name: "server without memcached service",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:   "server1.example.com",
					zimbraServiceEnabledAttr:    "mailbox",
					zimbraMemcachedBindPortAttr: "11211",
				},
			},
			expected: nil,
		},
		{
			name: "multiple servers, some with memcached, sorted by hostname",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:   "server1.example.com",
					zimbraServiceEnabledAttr:    "memcached",
					zimbraMemcachedBindPortAttr: "11211",
				},
				"server2.example.com": {
					zimbraServiceHostnameAttr: "server2.example.com",
					zimbraServiceEnabledAttr:  "mailbox",
				},
				"server3.example.com": {
					zimbraServiceHostnameAttr:   "server3.example.com",
					zimbraServiceEnabledAttr:    "memcached",
					zimbraMemcachedBindPortAttr: "11212",
				},
			},
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
				{Hostname: "server3.example.com", Port: 11212},
			},
		},
		{
			name: "multi-valued zimbraServiceEnabled matches memcached regardless of position",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:   "server1.example.com",
					zimbraServiceEnabledAttr:    "mailbox\nmemcached",
					zimbraMemcachedBindPortAttr: "11211",
				},
			},
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
			},
		},
		{
			name: "custom port",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr:   "server1.example.com",
					zimbraServiceEnabledAttr:    "memcached",
					zimbraMemcachedBindPortAttr: "12345",
				},
			},
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 12345},
			},
		},
		{
			name: "default port when not specified",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceHostnameAttr: "server1.example.com",
					zimbraServiceEnabledAttr:  "memcached",
				},
			},
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
			},
		},
		{
			name:     "no servers",
			byHost:   map[string]map[string]string{},
			expected: nil,
		},
		{
			name: "server missing zimbraServiceHostname is excluded",
			byHost: map[string]map[string]string{
				"server1.example.com": {
					zimbraServiceEnabledAttr:    "memcached",
					zimbraMemcachedBindPortAttr: "11211",
				},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildMemcachedServers(tt.byHost)

			if len(result) != len(tt.expected) {
				t.Fatalf("buildMemcachedServers() returned %d servers, expected %d: got %+v, expected %+v",
					len(result), len(tt.expected), result, tt.expected)
			}

			for i, server := range result {
				if server.Hostname != tt.expected[i].Hostname || server.Port != tt.expected[i].Port {
					t.Errorf("buildMemcachedServers() server[%d] = %+v, expected %+v",
						i, server, tt.expected[i])
				}
			}
		})
	}
}

// TestFormatMemcacheServers tests formatting of memcache servers for nginx config
func TestFormatMemcacheServers(t *testing.T) {
	tests := []struct {
		name     string
		servers  []MemcacheServer
		expected string
	}{
		{
			name: "single server",
			servers: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
			},
			expected: "  servers   server1.example.com:11211;",
		},
		{
			name: "multiple servers",
			servers: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
				{Hostname: "server2.example.com", Port: 11212},
				{Hostname: "server3.example.com", Port: 11213},
			},
			expected: "  servers   server1.example.com:11211;\n  servers   server2.example.com:11212;\n  servers   server3.example.com:11213;",
		},
		{
			name:     "empty list",
			servers:  []MemcacheServer{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMemcacheServers(tt.servers)

			if result != tt.expected {
				t.Errorf("formatMemcacheServers() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

// TestGetAllReverseProxyBackendsByPopulatedCache tests the cache-hit path of getAllReverseProxyBackendsBy
func TestGetAllReverseProxyBackendsByPopulatedCache(t *testing.T) {
	ctx := context.Background()
	cached := []UpstreamServer{{Host: "backend.example.com", Port: 8080}}

	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated:            true,
			reverseProxyBackends: cached,
		},
	}

	result, err := g.getAllReverseProxyBackends(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0].Host != "backend.example.com" {
		t.Errorf("unexpected result from cache: %+v", result)
	}
}

// TestGetAllReverseProxyBackendsBySSLPopulatedCache tests the SSL cache-hit path
func TestGetAllReverseProxyBackendsBySSLPopulatedCache(t *testing.T) {
	ctx := context.Background()
	cached := []UpstreamServer{{Host: "ssl-backend.example.com", Port: 8443}}

	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated:               true,
			reverseProxyBackendsSSL: cached,
		},
	}

	result, err := g.getAllReverseProxyBackendsSSL(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0].Host != "ssl-backend.example.com" {
		t.Errorf("unexpected result from SSL cache: %+v", result)
	}
}

// TestGetAllReverseProxyBackendsWithServerAttrsCache tests the cache-miss path
// via getServerAttrsByHostname's structured cache (serverAttrsByHost pre-filled).
func TestGetAllReverseProxyBackendsWithServerAttrsCache(t *testing.T) {
	ctx := context.Background()
	byHost := map[string]map[string]string{
		"backend.example.com": {
			zimbraServiceHostnameAttr:          "backend.example.com",
			zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr:                 "http",
			zimbraMailPortAttr:                 "8080",
			zimbraMailSSLPortAttr:               "8443",
		},
	}

	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated:         false,
			serverAttrsByHost: byHost,
		},
	}

	servers, err := g.getAllReverseProxyBackends(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d: %+v", len(servers), servers)
	}
	if servers[0].Host != "backend.example.com" {
		t.Errorf("expected backend.example.com, got %q", servers[0].Host)
	}
	if !g.upstreamCache.populated {
		t.Error("cache should be populated after first query")
	}
}

// TestGetAllReverseProxyBackendsNoServersUseFallback tests fallback when no servers found
func TestGetAllReverseProxyBackendsNoServersUseFallback(t *testing.T) {
	ctx := context.Background()
	// No lookup targets → empty servers → fallback to localhost:8080
	byHost := map[string]map[string]string{
		"backend.example.com": {
			zimbraServiceHostnameAttr:          "backend.example.com",
			zimbraReverseProxyLookupTargetAttr: "FALSE",
			zimbraMailModeAttr:                 "http",
			zimbraMailPortAttr:                 "8080",
		},
	}
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated:         false,
			serverAttrsByHost: byHost,
		},
	}

	servers, err := g.getAllReverseProxyBackends(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fallback to localhost:8080
	if len(servers) != 1 || servers[0].Host != "localhost" || servers[0].Port != 8080 {
		t.Errorf("expected fallback localhost:8080, got %+v", servers)
	}
}

// TestGetAllReverseProxyBackendsByNilLdap tests the error path when the cache
// is empty and no native LDAP client is available.
func TestGetAllReverseProxyBackendsByNilLdap(t *testing.T) {
	g := &Generator{
		upstreamCache: &upstreamQueryCache{populated: false},
		LdapClient:    nil,
	}
	_, err := g.getAllReverseProxyBackends(context.Background())
	if err == nil {
		t.Fatal("expected error when LDAP unavailable, got nil")
	}
}

// TestGetAllMemcachedServersCacheHit tests cache-hit path of getAllMemcachedServers
func TestGetAllMemcachedServersCacheHit(t *testing.T) {
	ctx := context.Background()
	expected := []MemcacheServer{{Hostname: "mc.example.com", Port: 11211}}
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated:        true,
			memcachedServers: expected,
		},
	}
	got, err := g.getAllMemcachedServers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Hostname != "mc.example.com" {
		t.Errorf("unexpected result: %+v", got)
	}
}

// TestGetAllMemcachedServersWithServerAttrsCache tests the cache-miss path via
// getServerAttrsByHostname's structured cache (serverAttrsByHost pre-filled).
func TestGetAllMemcachedServersWithServerAttrsCache(t *testing.T) {
	ctx := context.Background()
	byHost := map[string]map[string]string{
		"mc.example.com": {
			zimbraServiceHostnameAttr:   "mc.example.com",
			zimbraServiceEnabledAttr:    "memcached",
			zimbraMemcachedBindPortAttr: "11211",
		},
	}
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated:         false,
			serverAttrsByHost: byHost,
		},
	}
	got, err := g.getAllMemcachedServers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Hostname != "mc.example.com" || got[0].Port != 11211 {
		t.Errorf("unexpected result: %+v", got)
	}
	if !g.upstreamCache.populated {
		t.Error("cache should be populated after first query")
	}
}

// TestGetAllMemcachedServersCacheMissNilLdap tests cache-miss path of getAllMemcachedServers when LDAP is nil
func TestGetAllMemcachedServersCacheMissNilLdap(t *testing.T) {
	ctx := context.Background()
	g := &Generator{
		upstreamCache: &upstreamQueryCache{populated: false},
		LdapClient:    nil,
	}
	_, err := g.getAllMemcachedServers(ctx)
	if err == nil {
		t.Fatal("expected error when LDAP unavailable, got nil")
	}
}

// TestGetAllMemcachedServersNilCache tests getAllMemcachedServers with nil upstreamCache and nil ldap
func TestGetAllMemcachedServersNilCache(t *testing.T) {
	ctx := context.Background()
	g := &Generator{
		upstreamCache: nil,
		LdapClient:    nil,
	}
	_, err := g.getAllMemcachedServers(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestGetAllReverseProxyBackendsByNilLdapAndCache mirrors the memcached nil-cache
// error path for getAllReverseProxyBackendsBy.
func TestGetAllReverseProxyBackendsByNilLdapAndCache(t *testing.T) {
	g := &Generator{
		upstreamCache: nil,
		LdapClient:    nil,
	}
	_, err := g.getAllReverseProxyBackends(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestGetAllReverseProxyBackendsByLDAPQueryError verifies that an LDAP query
// failure (NativeClient set but unreachable) propagates as an error.
func TestGetAllReverseProxyBackendsByLDAPQueryError(t *testing.T) {
	l := ldap.NewLdap(context.Background(), &config.Config{})
	l.NativeClient = new(ldap.Client) // zero-value: no URLs → query fails

	g := &Generator{
		upstreamCache: &upstreamQueryCache{populated: false},
		LdapClient:    l,
	}

	_, err := g.getAllReverseProxyBackends(context.Background())
	if err == nil {
		t.Fatal("expected error when LDAP query fails, got nil")
	}
}
