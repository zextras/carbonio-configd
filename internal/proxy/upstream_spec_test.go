// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
)

// newSpecTestGenerator builds a Generator with the upstream cache pre-populated
// with the given servers (indexed by hostname), so resolvers run without LDAP.
func newSpecTestGenerator(serversByHost map[string]map[string]string) *Generator {
	return &Generator{
		GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig: &config.ServerConfig{Data: config.NewConfigMap()},
		upstreamCache: &upstreamQueryCache{
			serverAttrsByHost: serversByHost,
		},
	}
}

func TestIsValidUpstream(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]string
		want  bool
	}{
		{"valid https", map[string]string{zimbraReverseProxyLookupTargetAttr: "TRUE", zimbraMailModeAttr: "https"}, true},
		{"valid http", map[string]string{zimbraReverseProxyLookupTargetAttr: "true", zimbraMailModeAttr: "HTTP"}, true},
		{"valid both", map[string]string{zimbraReverseProxyLookupTargetAttr: "TRUE", zimbraMailModeAttr: "both"}, true},
		{"valid redirect", map[string]string{zimbraReverseProxyLookupTargetAttr: "TRUE", zimbraMailModeAttr: "redirect"}, true},
		{"not a target", map[string]string{zimbraReverseProxyLookupTargetAttr: "FALSE", zimbraMailModeAttr: "https"}, false},
		{"missing target", map[string]string{zimbraMailModeAttr: "https"}, false},
		{"empty mailmode", map[string]string{zimbraReverseProxyLookupTargetAttr: "TRUE", zimbraMailModeAttr: ""}, false},
		{"bad mailmode", map[string]string{zimbraReverseProxyLookupTargetAttr: "TRUE", zimbraMailModeAttr: "smtp"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidUpstream(tc.attrs); got != tc.want {
				t.Errorf("isValidUpstream(%v) = %v, want %v", tc.attrs, got, tc.want)
			}
		})
	}
}

func TestGenerateServerDirective(t *testing.T) {
	cases := []struct {
		name  string
		host  string
		attrs map[string]string
		port  int
		want  string
	}{
		{
			name:  "default timeout/maxfails",
			host:  "srv1.example.com",
			attrs: map[string]string{},
			port:  8080,
			want:  "srv1.example.com:8080 fail_timeout=60s",
		},
		{
			name:  "fixed ZX port",
			host:  "srv1.example.com",
			attrs: map[string]string{},
			port:  zxUpstreamPort,
			want:  "srv1.example.com:8742 fail_timeout=60s",
		},
		{
			name:  "custom reconnect timeout",
			host:  "srv1.example.com",
			attrs: map[string]string{attrMailProxyReconnectTimeout: "30"},
			port:  8080,
			want:  "srv1.example.com:8080 fail_timeout=30s",
		},
		{
			name:  "max_fails != 1 appended",
			host:  "srv1.example.com",
			attrs: map[string]string{attrMailProxyMaxFails: "3"},
			port:  8080,
			want:  "srv1.example.com:8080 fail_timeout=60s max_fails=3",
		},
		{
			name:  "no port resolves to empty",
			host:  "srv1.example.com",
			attrs: map[string]string{},
			port:  0,
			want:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := generateServerDirective(tc.host, tc.attrs, tc.port)
			if got != tc.want {
				t.Errorf("generateServerDirective = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatServersVar(t *testing.T) {
	if got := formatServersVar(nil); got != "" {
		t.Errorf("empty = %q, want empty", got)
	}

	got := formatServersVar([]string{"a:1 fail_timeout=60s", "b:2 fail_timeout=60s"})
	want := "server    a:1 fail_timeout=60s;\n        server    b:2 fail_timeout=60s;\n"

	if got != want {
		t.Errorf("formatServersVar =\n%q\nwant\n%q", got, want)
	}
}

func TestServerEnablesAnyService(t *testing.T) {
	// Exact match, not substring: "zimbra" must NOT match "zimbraAdmin".
	adminOnly := map[string]string{zimbraServiceEnabledAttr: "zimbraAdmin\nldap"}
	if serverEnablesAnyService(adminOnly, []string{serviceWebclient}) {
		t.Error("webclient service 'zimbra' should not match 'zimbraAdmin'")
	}

	if !serverEnablesAnyService(adminOnly, []string{serviceAdminclient}) {
		t.Error("adminclient service 'zimbraAdmin' should match")
	}

	multi := map[string]string{zimbraServiceEnabledAttr: "mailbox\nproxy"}
	if !serverEnablesAnyService(multi, []string{serviceMailclient, serviceMailbox}) {
		t.Error("mailbox should match in multi-valued list")
	}
}

func TestMakeUpstreamResolver_ServiceUnion(t *testing.T) {
	gen := newSpecTestGenerator(map[string]map[string]string{
		"mailbox-srv": {
			zimbraServiceHostnameAttr:          "mailbox-srv.example.com",
			zimbraServiceEnabledAttr:           "mailbox",
			zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr:                 "https",
			zimbraMailPortAttr:                 "8080",
		},
		"admin-srv": {
			zimbraServiceHostnameAttr:          "admin-srv.example.com",
			zimbraServiceEnabledAttr:           "zimbraAdmin",
			zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr:                 "https",
			zimbraMailPortAttr:                 "8080",
		},
	})

	// webclient = mailbox + zimbra; admin-srv (zimbraAdmin) must be excluded.
	resolver := gen.makeUpstreamResolver(&upstreamSpec{
		services:        []string{serviceMailbox, serviceWebclient},
		portAttrKey:     attrHTTPPortAttribute,
		portAttrDefault: defaultHTTPPortAttr,
	})

	out, err := resolver(context.Background())
	if err != nil {
		t.Fatalf("resolver error: %v", err)
	}

	want := "server    mailbox-srv.example.com:8080 fail_timeout=60s;\n"
	if out != want {
		t.Errorf("resolver =\n%q\nwant\n%q", out, want)
	}
}

func TestMakeUpstreamResolver_AttrList(t *testing.T) {
	gen := newSpecTestGenerator(map[string]map[string]string{
		"ews-srv": {
			zimbraServiceHostnameAttr:          "ews-srv.example.com",
			zimbraServiceEnabledAttr:           "mailbox",
			zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr:                 "https",
			zimbraMailPortAttr:                 "8080",
		},
		"other-srv": {
			zimbraServiceHostnameAttr:          "other-srv.example.com",
			zimbraServiceEnabledAttr:           "mailbox",
			zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr:                 "https",
			zimbraMailPortAttr:                 "8080",
		},
	})
	gen.GlobalConfig = &config.GlobalConfig{Data: config.NewConfigMapFrom(map[string]string{
		"zimbraReverseProxyUpstreamEwsServers": "ews-srv.example.com",
	})}

	resolver := gen.makeUpstreamResolver(&upstreamSpec{
		attrList:        "zimbraReverseProxyUpstreamEwsServers",
		portAttrKey:     attrHTTPPortAttribute,
		portAttrDefault: defaultHTTPPortAttr,
	})

	out, err := resolver(context.Background())
	if err != nil {
		t.Fatalf("resolver error: %v", err)
	}

	want := "server    ews-srv.example.com:8080 fail_timeout=60s;\n"
	if out != want {
		t.Errorf("attrList resolver =\n%q\nwant\n%q", out, want)
	}
}

func TestMakeUpstreamResolver_SkipsInvalid(t *testing.T) {
	gen := newSpecTestGenerator(map[string]map[string]string{
		"good": {
			zimbraServiceHostnameAttr:          "good.example.com",
			zimbraServiceEnabledAttr:           "mailbox",
			zimbraReverseProxyLookupTargetAttr: "TRUE",
			zimbraMailModeAttr:                 "https",
			zimbraMailPortAttr:                 "8080",
		},
		"not-target": {
			zimbraServiceHostnameAttr:          "not-target.example.com",
			zimbraServiceEnabledAttr:           "mailbox",
			zimbraReverseProxyLookupTargetAttr: "FALSE",
			zimbraMailModeAttr:                 "https",
			zimbraMailPortAttr:                 "8080",
		},
	})

	resolver := gen.makeUpstreamResolver(&upstreamSpec{
		services:        []string{serviceMailbox},
		portAttrKey:     attrHTTPPortAttribute,
		portAttrDefault: defaultHTTPPortAttr,
	})

	out, _ := resolver(context.Background())
	want := "server    good.example.com:8080 fail_timeout=60s;\n"

	if out != want {
		t.Errorf("resolver =\n%q\nwant\n%q (invalid server should be skipped)", out, want)
	}
}

// TestResolveServerPort exercises all branches: fixed port, attribute lookup,
// global config fallback, and zero default.
func TestResolveServerPort(t *testing.T) {
	gen := &Generator{
		GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig: &config.ServerConfig{Data: config.NewConfigMap()},
	}

	t.Run("fixed port wins", func(t *testing.T) {
		got := gen.resolveServerPort(nil, 8443, "")
		if got != 8443 {
			t.Errorf("got %d, want 8443", got)
		}
	})

	t.Run("attribute port", func(t *testing.T) {
		attrs := map[string]string{"zimbraMailPort": "7025"}
		got := gen.resolveServerPort(attrs, 0, "zimbraMailPort")
		if got != 7025 {
			t.Errorf("got %d, want 7025", got)
		}
	})

	t.Run("global config fallback", func(t *testing.T) {
		gen.GlobalConfig.Data.Set("zimbraMailPort", "8025")
		got := gen.resolveServerPort(nil, 0, "zimbraMailPort")
		if got != 8025 {
			t.Errorf("got %d, want 8025", got)
		}
	})

	t.Run("zero default when nothing found", func(t *testing.T) {
		got := gen.resolveServerPort(nil, 0, "noSuchAttr")
		if got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}
func TestUpstreamHasServers_ErrorPath(t *testing.T) {
	g := &Generator{
		GlobalConfig:  &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig:  &config.ServerConfig{Data: config.NewConfigMap()},
		upstreamCache: &upstreamQueryCache{serverAttrsByHost: map[string]map[string]string{}},
	}
	spec := &upstreamSpec{services: []string{"mailbox"}, fixedPort: 8080}
	result := g.upstreamHasServers(context.Background(), spec)
	if result {
		t.Errorf("expected false (no servers), got true")
	}
}

func TestMakeUpstreamResolver_ErrorReturnsEmpty(t *testing.T) {
	g := newSpecTestGenerator(map[string]map[string]string{})
	spec := &upstreamSpec{services: []string{"mailbox"}, fixedPort: 8080}
	resolver := g.makeUpstreamResolver(spec)
	val, err := resolver(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	s, _ := val.(string)
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}

func TestResolveUpstreamPortAttr_FixedPort(t *testing.T) {
	g := newSpecTestGenerator(nil)
	spec := &upstreamSpec{fixedPort: 7025, portAttrKey: "zimbraMailPort", portAttrDefault: "25"}
	result := g.resolveUpstreamPortAttr(spec)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestResolveUpstreamPortAttr_GlobalConfigValue(t *testing.T) {
	g := newSpecTestGenerator(nil)
	g.GlobalConfig.Data.Set("zimbraMailPort", "8025")
	spec := &upstreamSpec{fixedPort: 0, portAttrKey: "zimbraMailPort", portAttrDefault: "25"}
	result := g.resolveUpstreamPortAttr(spec)
	if result != "8025" {
		t.Errorf("expected 8025, got %q", result)
	}
}

func TestGetServerAttrsByHostname_NilLdapClient(t *testing.T) {
	g := &Generator{
		GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig: &config.ServerConfig{Data: config.NewConfigMap()},
	}
	result, err := g.getServerAttrsByHostname(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}

func TestGetServerAttrsByHostname_CacheHit(t *testing.T) {
	servers := map[string]map[string]string{
		"srv1": {"zimbraServiceHostname": "srv1.example.com"},
	}
	g := newSpecTestGenerator(servers)
	result, err := g.getServerAttrsByHostname(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Errorf("want 1 server, got %d", len(result))
	}
}

// TestGetServerAttrsByHostname_LDAPQueryError verifies the LDAP error path:
// when LdapClient and NativeClient are set but the LDAP connection fails,
// getServerAttrsByHostname returns nil map and a non-nil error.
func TestGetServerAttrsByHostname_LDAPQueryError(t *testing.T) {
	// New zero-value Ldap manager with a zero-value NativeClient.
	// zero-value Client has no URLs → GetAllServersWithAttributes fails immediately.
	l := ldap.NewLdap(context.Background(), &config.Config{})
	l.NativeClient = new(ldap.Client) // zero-value: no URLs → query will fail

	g := &Generator{
		GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig: &config.ServerConfig{Data: config.NewConfigMap()},
		LdapClient:   l,
		// upstreamCache is nil — forces the LDAP path
	}

	_, err := g.getServerAttrsByHostname(context.Background())
	if err == nil {
		t.Error("expected error from failing LDAP query, got nil")
	}
}

// TestMakeUpstreamResolver_LDAPError verifies the error-fallback path in the
// makeUpstreamResolver closure: when upstreamCandidates fails (LDAP error),
// the resolver returns ("", nil) instead of propagating the error.
func TestMakeUpstreamResolver_LDAPError(t *testing.T) {
	l := ldap.NewLdap(context.Background(), &config.Config{})
	l.NativeClient = new(ldap.Client) // zero-value → LDAP query fails

	g := &Generator{
		GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig: &config.ServerConfig{Data: config.NewConfigMap()},
		LdapClient:   l,
	}

	spec := &upstreamSpec{services: []string{"mailbox"}, fixedPort: 8080}
	resolver := g.makeUpstreamResolver(spec)
	val, err := resolver(context.Background())
	if err != nil {
		t.Errorf("expected nil error (fallback on LDAP error), got %v", err)
	}
	s, _ := val.(string)
	if s != "" {
		t.Errorf("expected empty string on LDAP error, got %q", s)
	}
}

// TestUpstreamHasServers_LDAPError verifies upstreamHasServers returns false
// when upstreamCandidates returns an error (LDAP failure).
func TestUpstreamHasServers_LDAPError(t *testing.T) {
	l := ldap.NewLdap(context.Background(), &config.Config{})
	l.NativeClient = new(ldap.Client)

	g := &Generator{
		GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig: &config.ServerConfig{Data: config.NewConfigMap()},
		LdapClient:   l,
	}

	spec := &upstreamSpec{services: []string{"mailbox"}, fixedPort: 8080}
	if g.upstreamHasServers(context.Background(), spec) {
		t.Error("expected false when LDAP query fails, got true")
	}
}
