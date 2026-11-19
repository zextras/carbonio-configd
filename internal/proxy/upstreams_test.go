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
	"github.com/zextras/carbonio-configd/internal/sys"
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
					zimbraMailSSLPortAttr:              "8443",
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
					zimbraMailSSLPortAttr:              "8443",
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
					zimbraMailSSLPortAttr:              "8443",
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
					zimbraMailSSLPortAttr:              "8443",
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
					zimbraMailSSLPortAttr:              "8443",
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
					zimbraMailSSLPortAttr:              "443",
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
			g := &Generator{env: sys.Real()}
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

	g := &Generator{env: sys.Real()}

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
	g := &Generator{env: sys.Real()}
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

// TestGetAllReverseProxyBackendsByPopulatedCache tests the cache-hit path of getAllReverseProxyBackendsBy
func TestGetAllReverseProxyBackendsByPopulatedCache(t *testing.T) {
	ctx := context.Background()
	cached := []UpstreamServer{{Host: "backend.example.com", Port: 8080}}

	g := &Generator{
		env: sys.Real(),
		upstreamCache: &upstreamQueryCache{
			backendsPopulated:    true,
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
		env: sys.Real(),
		upstreamCache: &upstreamQueryCache{
			backendsSSLPopulated:    true,
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
			zimbraMailSSLPortAttr:              "8443",
		},
	}

	g := &Generator{
		env: sys.Real(),
		upstreamCache: &upstreamQueryCache{
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
	if !g.upstreamCache.backendsPopulated {
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
		env: sys.Real(),
		upstreamCache: &upstreamQueryCache{
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
		env:           sys.Real(),
		upstreamCache: &upstreamQueryCache{},
		LdapClient:    nil,
	}
	_, err := g.getAllReverseProxyBackends(context.Background())
	if err == nil {
		t.Fatal("expected error when LDAP unavailable, got nil")
	}
}

// TestGetAllReverseProxyBackendsByNilLdapAndCache mirrors the memcached nil-cache
// error path for getAllReverseProxyBackendsBy.
func TestGetAllReverseProxyBackendsByNilLdapAndCache(t *testing.T) {
	g := &Generator{
		env:           sys.Real(),
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
		env:           sys.Real(),
		upstreamCache: &upstreamQueryCache{},
		LdapClient:    l,
	}

	_, err := g.getAllReverseProxyBackends(context.Background())
	if err == nil {
		t.Fatal("expected error when LDAP query fails, got nil")
	}
}

// TestGetAllReverseProxyBackends_PlainAndSSLIndependent guards against the shared
// "populated" flag bug: whichever of the plain/SSL lists was requested second
// used to get an empty cached list (ResolveAllVariables asks in map order).
// Both request orders are exercised explicitly so the test is order-independent.
func TestGetAllReverseProxyBackends_PlainAndSSLIndependent(t *testing.T) {
	newGen := func() *Generator {
		return &Generator{env: sys.Real(), upstreamCache: &upstreamQueryCache{
			serverAttrsByHost: map[string]map[string]string{
				"backend.example.com": {
					zimbraServiceHostnameAttr:          "backend.example.com",
					zimbraReverseProxyLookupTargetAttr: "TRUE",
					zimbraMailModeAttr:                 "http",
					zimbraMailPortAttr:                 "8080",
					zimbraMailSSLPortAttr:              "8443",
				},
			},
		}}
	}

	orders := map[string][]bool{"plain-then-ssl": {false, true}, "ssl-then-plain": {true, false}}
	ports := map[bool]int{false: 8080, true: 8443}

	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			g := newGen()

			for _, ssl := range order {
				got, err := g.getAllReverseProxyBackendsBy(context.Background(), ssl)
				if err != nil {
					t.Fatalf("ssl=%v: %v", ssl, err)
				}

				if len(got) != 1 || got[0].Host != "backend.example.com" || got[0].Port != ports[ssl] {
					t.Errorf("ssl=%v: got %+v, want backend.example.com:%d", ssl, got, ports[ssl])
				}
			}
		})
	}
}

// TestGetAllReverseProxyBackends_EmptyCachedFallsBack verifies a cached empty list
// yields the same localhost fallback as the first (uncached) call.
func TestGetAllReverseProxyBackends_EmptyCachedFallsBack(t *testing.T) {
	g := &Generator{env: sys.Real(), upstreamCache: &upstreamQueryCache{
		serverAttrsByHost: map[string]map[string]string{},
	}}

	first, err := g.getAllReverseProxyBackends(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	second, err := g.getAllReverseProxyBackends(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Errorf("first=%+v second=%+v, want identical localhost fallback", first, second)
	}
}
