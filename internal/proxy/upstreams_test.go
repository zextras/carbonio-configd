// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - upstream server discovery tests
package proxy

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/ldap"
)

// TestParseReverseProxyBackends tests parsing of zmprov gas output for reverse proxy backends
func TestParseReverseProxyBackends(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []UpstreamServer
	}{
		{
			name: "single server with lookup target true",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: http
zimbraMailPort: 8080
zimbraMailSSLPort: 8443`,
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
			},
		},
		{
			name: "server with lookup target false",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: FALSE
zimbraMailMode: http
zimbraMailPort: 8080`,
			expected: []UpstreamServer{},
		},
		{
			name: "server with https mode uses SSL port",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: https
zimbraMailPort: 8080
zimbraMailSSLPort: 8443`,
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8443},
			},
		},
		{
			name: "server with mixed mode uses HTTP port",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: mixed
zimbraMailPort: 8080
zimbraMailSSLPort: 8443`,
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
			},
		},
		{
			name: "server with both mode uses HTTP port",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: both
zimbraMailPort: 8080
zimbraMailSSLPort: 8443`,
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
			},
		},
		{
			name: "multiple servers, mixed lookup targets",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: http
zimbraMailPort: 8080
zimbraMailSSLPort: 8443

# name server2.example.com
zimbraServiceHostname: server2.example.com
zimbraReverseProxyLookupTarget: FALSE
zimbraMailMode: http
zimbraMailPort: 8080

# name server3.example.com
zimbraServiceHostname: server3.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: https
zimbraMailPort: 80
zimbraMailSSLPort: 443`,
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
				{Host: "server3.example.com", Port: 443},
			},
		},
		{
			name: "default ports when not specified",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: http`,
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 80},
			},
		},
		{
			name: "default SSL port",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: https`,
			expected: []UpstreamServer{
				{Host: "server1.example.com", Port: 443},
			},
		},
		{
			name:     "no servers",
			input:    "",
			expected: []UpstreamServer{},
		},
		{
			name: "server missing hostname",
			input: `# name server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: http
zimbraMailPort: 8080`,
			expected: []UpstreamServer{},
		},
	}

	g := &Generator{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := g.parseReverseProxyBackends(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("parseReverseProxyBackends() returned %d servers, expected %d",
					len(result), len(tt.expected))
				t.Logf("Got: %+v", result)
				t.Logf("Expected: %+v", tt.expected)
				return
			}

			for i, server := range result {
				if server.Host != tt.expected[i].Host || server.Port != tt.expected[i].Port {
					t.Errorf("parseReverseProxyBackends() server[%d] = %+v, expected %+v",
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
		data     serverData
		expected UpstreamServer
	}{
		{
			name: "http mode uses mail port",
			data: serverData{
				hostname:    "server1.example.com",
				mailMode:    "http",
				mailPort:    8080,
				mailSSLPort: 8443,
			},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8080},
		},
		{
			name: "https mode uses SSL port",
			data: serverData{
				hostname:    "server1.example.com",
				mailMode:    "https",
				mailPort:    8080,
				mailSSLPort: 8443,
			},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
		{
			name: "mixed mode uses mail port",
			data: serverData{
				hostname:    "server1.example.com",
				mailMode:    "mixed",
				mailPort:    8080,
				mailSSLPort: 8443,
			},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8080},
		},
		{
			name: "both mode uses mail port",
			data: serverData{
				hostname:    "server1.example.com",
				mailMode:    "both",
				mailPort:    8080,
				mailSSLPort: 8443,
			},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8080},
		},
		{
			name: "redirect mode uses SSL port",
			data: serverData{
				hostname:    "server1.example.com",
				mailMode:    "redirect",
				mailPort:    8080,
				mailSSLPort: 8443,
			},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
		{
			name: "unknown mode uses SSL port",
			data: serverData{
				hostname:    "server1.example.com",
				mailMode:    "unknown",
				mailPort:    8080,
				mailSSLPort: 8443,
			},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
		{
			name: "empty mode uses SSL port",
			data: serverData{
				hostname:    "server1.example.com",
				mailMode:    "",
				mailPort:    8080,
				mailSSLPort: 8443,
			},
			expected: UpstreamServer{Host: "server1.example.com", Port: 8443},
		},
	}

	g := &Generator{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := g.buildUpstreamServer(tt.data)

			if result.Host != tt.expected.Host || result.Port != tt.expected.Port {
				t.Errorf("buildUpstreamServer() = %+v, expected %+v", result, tt.expected)
			}
		})
	}
}

// TestParseMemcachedServers tests parsing of memcached servers
func TestParseMemcachedServers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []MemcacheServer
	}{
		{
			name: "single memcached server",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraServiceEnabled: memcached
zimbraMemcachedBindPort: 11211`,
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
			},
		},
		{
			name: "server without memcached service",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraServiceEnabled: mailbox
zimbraMemcachedBindPort: 11211`,
			expected: []MemcacheServer{},
		},
		{
			name: "multiple servers, some with memcached",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraServiceEnabled: memcached
zimbraMemcachedBindPort: 11211

# name server2.example.com
zimbraServiceHostname: server2.example.com
zimbraServiceEnabled: mailbox

# name server3.example.com
zimbraServiceHostname: server3.example.com
zimbraServiceEnabled: memcached
zimbraMemcachedBindPort: 11212`,
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
				{Hostname: "server3.example.com", Port: 11212},
			},
		},
		{
			name: "custom port",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraServiceEnabled: memcached
zimbraMemcachedBindPort: 12345`,
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 12345},
			},
		},
		{
			name: "default port when not specified",
			input: `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraServiceEnabled: memcached`,
			expected: []MemcacheServer{
				{Hostname: "server1.example.com", Port: 11211},
			},
		},
		{
			name:     "no servers",
			input:    "",
			expected: []MemcacheServer{},
		},
		{
			name: "server missing hostname",
			input: `# name server1.example.com
zimbraServiceEnabled: memcached
zimbraMemcachedBindPort: 11211`,
			expected: []MemcacheServer{},
		},
	}

	g := &Generator{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := g.parseMemcachedServers(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("parseMemcachedServers() returned %d servers, expected %d",
					len(result), len(tt.expected))
				t.Logf("Got: %+v", result)
				t.Logf("Expected: %+v", tt.expected)
				return
			}

			for i, server := range result {
				if server.Hostname != tt.expected[i].Hostname || server.Port != tt.expected[i].Port {
					t.Errorf("parseMemcachedServers() server[%d] = %+v, expected %+v",
						i, server, tt.expected[i])
				}
			}
		})
	}
}

// TestFormatUpstreamServers tests formatting of upstream servers for nginx config
func TestFormatUpstreamServers(t *testing.T) {
	tests := []struct {
		name     string
		servers  []UpstreamServer
		expected string
	}{
		{
			name: "single server",
			servers: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
			},
			expected: "    server    server1.example.com:8080 fail_timeout=10s;\n",
		},
		{
			name: "multiple servers",
			servers: []UpstreamServer{
				{Host: "server1.example.com", Port: 8080},
				{Host: "server2.example.com", Port: 8081},
				{Host: "server3.example.com", Port: 8082},
			},
			expected: "    server    server1.example.com:8080 fail_timeout=10s;\n" +
				"    server    server2.example.com:8081 fail_timeout=10s;\n" +
				"    server    server3.example.com:8082 fail_timeout=10s;\n",
		},
		{
			name:     "empty list",
			servers:  []UpstreamServer{},
			expected: "",
		},
		{
			name: "localhost fallback",
			servers: []UpstreamServer{
				{Host: "localhost", Port: 8080},
			},
			expected: "    server    localhost:8080 fail_timeout=10s;\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatUpstreamServers(tt.servers)

			if result != tt.expected {
				t.Errorf("formatUpstreamServers() = %q, expected %q", result, tt.expected)
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

// TestParseMultiValuedAttribute tests parsing of multi-valued LDAP attributes
func TestParseMultiValuedAttribute(t *testing.T) {
	t.Skip("Skipping test - parseMultiValuedAttribute is no longer available")
}

// TestParseServerDetails tests parsing of server hostname and port from zmprov output
func TestParseServerDetails(t *testing.T) {
	t.Skip("Skipping test - parseServerDetails is no longer available")
}

// TestCollectAttributeServerNames tests collectAttributeServerNames
func TestCollectAttributeServerNames(t *testing.T) {
	tests := []struct {
		name          string
		gasOutput     string
		attributeName string
		expected      map[string]bool
	}{
		{
			name: "finds server listed under attribute",
			gasOutput: `# name globalconfig
zimbraReverseProxyUpstreamEwsServers: server1.example.com
`,
			attributeName: "zimbraReverseProxyUpstreamEwsServers",
			expected:      map[string]bool{"server1.example.com": true},
		},
		{
			name: "finds multiple servers listed under attribute",
			gasOutput: `# name globalconfig
zimbraReverseProxyUpstreamEwsServers: server1.example.com
zimbraReverseProxyUpstreamEwsServers: server2.example.com
`,
			attributeName: "zimbraReverseProxyUpstreamEwsServers",
			expected:      map[string]bool{"server1.example.com": true, "server2.example.com": true},
		},
		{
			name:          "returns empty map when attribute not present",
			gasOutput:     "# name globalconfig\nzimbraOtherAttr: somevalue\n",
			attributeName: "zimbraReverseProxyUpstreamEwsServers",
			expected:      map[string]bool{},
		},
		{
			name:          "empty input returns empty map",
			gasOutput:     "",
			attributeName: "zimbraReverseProxyUpstreamEwsServers",
			expected:      map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectAttributeServerNames(tt.gasOutput, tt.attributeName)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d names, got %d: %v", len(tt.expected), len(result), result)
			}
			for k := range tt.expected {
				if !result[k] {
					t.Errorf("expected %q in result, got %v", k, result)
				}
			}
		})
	}
}

// TestParseReverseProxyBackendsSSL tests parseReverseProxyBackendsSSL always uses SSL port
func TestParseReverseProxyBackendsSSL(t *testing.T) {
	input := `# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: http
zimbraMailPort: 8080
zimbraMailSSLPort: 8443`

	g := &Generator{}
	result := g.parseReverseProxyBackendsSSL(input)

	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0].Port != 8443 {
		t.Errorf("expected SSL port 8443, got %d", result[0].Port)
	}
	if result[0].Host != "server1.example.com" {
		t.Errorf("expected server1.example.com, got %q", result[0].Host)
	}
}

// TestBuildUpstreamServerSSL tests buildUpstreamServerSSL always uses SSL port
func TestBuildUpstreamServerSSL(t *testing.T) {
	g := &Generator{}
	data := serverData{
		hostname:    "server1.example.com",
		mailMode:    "http", // even with http mode, SSL method uses SSL port
		mailPort:    8080,
		mailSSLPort: 8443,
	}
	result := g.buildUpstreamServerSSL(data)
	if result.Port != 8443 {
		t.Errorf("expected SSL port 8443, got %d", result.Port)
	}
	if result.Host != "server1.example.com" {
		t.Errorf("expected server1.example.com, got %q", result.Host)
	}
}

// TestParseServersFromGasOutput tests parseServersFromGasOutput two-pass logic
func TestParseServersFromGasOutput(t *testing.T) {
	// Gas output simulating a global config listing an upstream and a server block
	gasOutput := `# name globalconfig
zimbraReverseProxyUpstreamEwsServers: server1.example.com

# name server1.example.com
zimbraServiceHostname: server1.example.com
zimbraMailPort: 8080
zimbraMailSSLPort: 8443
`
	g := &Generator{}
	ctx := context.Background()

	t.Run("finds server when listed in attribute", func(t *testing.T) {
		servers := g.parseServersFromGasOutput(ctx, gasOutput, "zimbraReverseProxyUpstreamEwsServers", "zimbraMailPort")
		if len(servers) != 1 {
			t.Fatalf("expected 1 server, got %d: %v", len(servers), servers)
		}
		if servers[0].Host != "server1.example.com" {
			t.Errorf("expected server1.example.com, got %q", servers[0].Host)
		}
		if servers[0].Port != 8080 {
			t.Errorf("expected port 8080, got %d", servers[0].Port)
		}
	})

	t.Run("returns nil when no server listed under attribute", func(t *testing.T) {
		servers := g.parseServersFromGasOutput(ctx, gasOutput, "zimbraReverseProxyUpstreamLoginServers", "zimbraMailPort")
		if servers != nil {
			t.Errorf("expected nil, got %v", servers)
		}
	})
}

// TestGetUpstreamServersByAttributeWithPortCached tests cache hit path
func TestGetUpstreamServersByAttributeWithPortCached(t *testing.T) {
	ctx := context.Background()

	cachedServers := []UpstreamServer{{Host: "cached.example.com", Port: 9090}}
	attrName := "zimbraReverseProxyUpstreamEwsServers"

	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			attributeServers: map[string][]UpstreamServer{
				attrName: cachedServers,
			},
			attributeServersSSL: make(map[string][]UpstreamServer),
		},
	}

	result, err := g.getUpstreamServersByAttributeWithPort(ctx, attrName, "zimbraMailPort", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 server from cache, got %d", len(result))
	}
	if result[0].Host != "cached.example.com" {
		t.Errorf("expected cached server, got %q", result[0].Host)
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

// TestGetOrCacheServersOutputStoresInCache tests that getOrCacheServersOutput stores gasOutput in cache
func TestGetOrCacheServersOutputStoresInCache(t *testing.T) {
	gasOutput := "# name server1.example.com\nzimbraServiceHostname: server1.example.com\n"
	// Pre-populate cache gasOutput to bypass getAllServersOutput
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			gasOutput: gasOutput,
		},
	}

	got, err := g.getOrCacheServersOutput()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != gasOutput {
		t.Errorf("expected %q, got %q", gasOutput, got)
	}
	// Verify cache was not cleared
	if g.upstreamCache.gasOutput != gasOutput {
		t.Error("gasOutput should remain in cache")
	}
}

// TestGetAllReverseProxyBackendsWithGasOutput tests cache-miss path via getOrCacheServersOutput (gasOutput pre-filled)
func TestGetAllReverseProxyBackendsWithGasOutput(t *testing.T) {
	ctx := context.Background()
	gasOutput := `# name backend.example.com
zimbraServiceHostname: backend.example.com
zimbraReverseProxyLookupTarget: TRUE
zimbraMailMode: http
zimbraMailPort: 8080
zimbraMailSSLPort: 8443
`
	// getAllReverseProxyBackendsBy calls getOrCacheServersOutput which checks gasOutput first
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated: false,
			gasOutput: gasOutput,
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
	// Gas output with no lookup targets → empty servers → fallback to localhost:8080
	gasOutput := `# name backend.example.com
zimbraServiceHostname: backend.example.com
zimbraReverseProxyLookupTarget: FALSE
zimbraMailMode: http
zimbraMailPort: 8080
`
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			populated: false,
			gasOutput: gasOutput,
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

// TestGetUpstreamServersByAttributeWithPortCacheMissGasOutput tests cache-miss path using pre-filled gasOutput
func TestGetUpstreamServersByAttributeWithPortCacheMissGasOutput(t *testing.T) {
	ctx := context.Background()
	attrName := "zimbraReverseProxyUpstreamEwsServers"
	gasOutput := `# name globalconfig
zimbraReverseProxyUpstreamEwsServers: ews.example.com

# name ews.example.com
zimbraServiceHostname: ews.example.com
zimbraMailPort: 8080
`
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			attributeServers:    map[string][]UpstreamServer{},
			attributeServersSSL: map[string][]UpstreamServer{},
			gasOutput:           gasOutput, // pre-filled to bypass getAllServersOutput
		},
	}

	servers, err := g.getUpstreamServersByAttributeWithPort(ctx, attrName, "zimbraMailPort", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d: %+v", len(servers), servers)
	}
	if servers[0].Host != "ews.example.com" {
		t.Errorf("expected ews.example.com, got %q", servers[0].Host)
	}
	// Verify result is stored in cache
	if cached, ok := g.upstreamCache.attributeServers[attrName]; !ok || len(cached) != 1 {
		t.Errorf("expected result stored in cache, got %v", g.upstreamCache.attributeServers)
	}
}

// TestGetUpstreamServersByAttributeWithPortNilCacheMap tests with nil cacheMap (no upstreamCache)
func TestGetUpstreamServersByAttributeWithPortNilCacheMap(t *testing.T) {
	ctx := context.Background()
	// No upstreamCache → cacheMap is nil → goes straight to getOrCacheServersOutput which needs LDAP
	g := &Generator{
		upstreamCache: nil,
		LdapClient:    nil,
	}
	_, err := g.getUpstreamServersByAttributeWithPort(ctx, "someAttr", "zimbraMailPort", false)
	if err == nil {
		t.Fatal("expected error when no cache and no LDAP, got nil")
	}
}

// TestGetAllServersOutputNilLdap tests getAllServersOutput when LdapClient is nil
func TestGetAllServersOutputNilLdap(t *testing.T) {
	g := &Generator{LdapClient: nil}
	_, err := g.getAllServersOutput()
	if err == nil {
		t.Fatal("expected error when LdapClient is nil, got nil")
	}
}

// TestGetAllServersOutputNilNativeClient tests getAllServersOutput when NativeClient is nil
func TestGetAllServersOutputNilNativeClient(t *testing.T) {
	g := &Generator{
		LdapClient: &ldap.Ldap{}, // NativeClient field is nil by default
	}
	_, err := g.getAllServersOutput()
	if err == nil {
		t.Fatal("expected error when NativeClient is nil, got nil")
	}
}

// TestGetOrCacheServersOutputCacheHit tests the cache-hit path of getOrCacheServersOutput
func TestGetOrCacheServersOutputCacheHit(t *testing.T) {
	cached := "# name server1.example.com\nzimbraServiceHostname: server1.example.com\n"
	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			gasOutput: cached,
		},
	}
	got, err := g.getOrCacheServersOutput()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cached {
		t.Errorf("expected cached output, got %q", got)
	}
}

// TestGetOrCacheServersOutputCacheMissNilLdap tests cache-miss path that falls through to getAllServersOutput error
func TestGetOrCacheServersOutputCacheMissNilLdap(t *testing.T) {
	g := &Generator{
		upstreamCache: &upstreamQueryCache{gasOutput: ""},
		LdapClient:    nil,
	}
	_, err := g.getOrCacheServersOutput()
	if err == nil {
		t.Fatal("expected error when LDAP unavailable, got nil")
	}
}

// TestGetOrCacheServersOutputNilCache tests getOrCacheServersOutput with nil upstreamCache and nil ldap
func TestGetOrCacheServersOutputNilCacheNilLdap(t *testing.T) {
	g := &Generator{
		upstreamCache: nil,
		LdapClient:    nil,
	}
	_, err := g.getOrCacheServersOutput()
	if err == nil {
		t.Fatal("expected error, got nil")
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

// TestGetUpstreamServersByAttributeSSLCached tests SSL cache hit path
func TestGetUpstreamServersByAttributeSSLCached(t *testing.T) {
	ctx := context.Background()

	cachedServers := []UpstreamServer{{Host: "ssl-cached.example.com", Port: 8443}}
	attrName := "zimbraReverseProxyUpstreamEwsServers"

	g := &Generator{
		upstreamCache: &upstreamQueryCache{
			attributeServers: make(map[string][]UpstreamServer),
			attributeServersSSL: map[string][]UpstreamServer{
				attrName: cachedServers,
			},
		},
	}

	result, err := g.getUpstreamServersByAttributeWithPort(ctx, attrName, "zimbraMailSSLPort", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 server from SSL cache, got %d", len(result))
	}
	if result[0].Host != "ssl-cached.example.com" {
		t.Errorf("expected SSL cached server, got %q", result[0].Host)
	}
}
