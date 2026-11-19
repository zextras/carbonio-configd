// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/ldap"
)

// ldapDown is an LDAP client every query fails on (zero-value native client).
func ldapDown() *ldap.Ldap { return &ldap.Ldap{NativeClient: &ldap.Client{}} }

func TestDeployDomainFiles_WritesClientCA(t *testing.T) {
	t.Parallel()

	tp, dir := newDomainCertProcessor(t, []DomainInfo{
		{Name: "ca.com", VirtualHostname: "mail.ca.com", ClientCertCA: "CA-PEM"},
		{Name: "noca.com", VirtualHostname: "mail.noca.com", SSLCertificate: testCertPEM, SSLPrivateKey: testKeyPEM},
	})

	require.NoError(t, tp.syncDomainCerts(context.Background()))

	got, err := os.ReadFile(filepath.Join(dir, "ca.com.client.ca.crt"))
	require.NoError(t, err)
	assert.Equal(t, "CA-PEM", string(got))
	assert.NoFileExists(t, filepath.Join(dir, "noca.com.client.ca.crt"))
	assert.NoFileExists(t, filepath.Join(dir, "ca.com.crt"), "no cert pair, no cert file")

	// The CA file goes away with its domain.
	tp.domainProvider = func() []DomainInfo { return nil }

	require.NoError(t, tp.syncDomainCerts(context.Background()))
	assert.NoFileExists(t, filepath.Join(dir, "ca.com.client.ca.crt"))
}

func TestCleanupDomains(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loaded := []DomainInfo{{Name: "a.com"}}

	t.Run("gate off never cleans", func(t *testing.T) {
		t.Parallel()

		g := newConfigGenerator(t, map[string]string{perVirtualHostnameAttr: "FALSE"}, nil)
		tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
		tp.domainProvider = func() []DomainInfo { return loaded }

		_, ok := tp.cleanupDomains(ctx, loaded)
		assert.False(t, ok)
	})

	t.Run("provider is authoritative", func(t *testing.T) {
		t.Parallel()

		g := newConfigGenerator(t, nil, nil)
		tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
		tp.domainProvider = func() []DomainInfo { return loaded }

		got, ok := tp.cleanupDomains(ctx, loaded)
		assert.True(t, ok)
		assert.Equal(t, loaded, got)
	})

	t.Run("unreadable domain list never cleans", func(t *testing.T) {
		t.Parallel()

		g := newConfigGenerator(t, nil, nil)
		g.LdapClient = ldapDown()
		tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())

		_, ok := tp.cleanupDomains(ctx, loaded)
		assert.False(t, ok)
	})
}

func TestSyncDomainCerts_UnreadableDomainListKeepsCertbotAndFiles(t *testing.T) {
	t.Parallel()

	g, f := newCertbotGenerator(t)
	mkCertbotLive(t, g, f, "gone.com", map[string]string{"fullchain.pem": "c", "privkey.pem": "k"})

	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
	tp.domainProvider = func() []DomainInfo { return nil }
	g.GlobalConfig.Data.Replace(map[string]string{perVirtualHostnameAttr: "FALSE"})

	require.NoError(t, tp.syncDomainCerts(context.Background()))

	for _, line := range f.Exec.Lines() {
		assert.NotContains(t, line, "delete", "certbot delete must not run with the gate off")
	}
}

func TestGenerateAll_EarlyFailureReplacesStaleMarker(t *testing.T) {
	t.Parallel()

	g, f := newFakeConfGenerator("")
	g.confBody = nil
	g.TemplateDir = "/tpl"
	g.IncludesDir = "/inc"
	f.FS.PutDir("/tpl", 0o755)
	f.FS.Put(testConfPath, []byte(testConfBody+"\n#"+confGenSuccessMarker+"\n"), generatedFileMode)

	for range 2 {
		require.Error(t, g.GenerateAll(context.Background()))
	}

	data, _ := f.FS.Data(testConfPath)
	s := string(data)
	assert.Equal(t, 1, strings.Count(s, "#"+confGenErrorMarker), "one error marker, not one per run:\n%s", s)
	assert.NotContains(t, s, confGenSuccessMarker, "stale success marker must be gone")
	assert.True(t, strings.HasPrefix(s, testConfBody))
}

func TestAppendConfGenResult_FlattensNewlines(t *testing.T) {
	t.Parallel()

	g, f := newFakeConfGenerator(testConfBody)
	f.FS.Put(testConfPath, []byte(testConfBody), generatedFileMode)

	g.appendConfGenResult(context.Background(), confGenErrorMarker+"a\nworker_processes 9;\rb")

	data, _ := f.FS.Data(testConfPath)
	assert.NotContains(t, string(data), "\nworker_processes 9;")
	assert.Contains(t, string(data), "a worker_processes 9; b")
}

func TestTrimResultMarkers(t *testing.T) {
	t.Parallel()

	body := "a;\n"
	in := body + "\n#" + confGenErrorMarker + "x\n\n#" + confGenSuccessMarker + "\n"
	assert.Equal(t, body, string(trimResultMarkers([]byte(in))))
	assert.Equal(t, body, string(trimResultMarkers([]byte(body))))
}

func TestLDAPFailureFailsClosed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("LDAP configured", func(t *testing.T) {
		t.Parallel()

		g := newConfigGenerator(t, nil, nil)
		g.LdapClient = ldapDown()
		tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())

		_, err := g.resolveWebAvailable(ctx)
		require.Error(t, err)

		_, err = tp.getServersWithService(ctx, "mailbox")
		require.Error(t, err)

		_, err = tp.getDomains(ctx)
		require.Error(t, err)

		_, err = g.collectVirtualIPAddresses(ctx)
		require.Error(t, err)
	})

	t.Run("offline stays empty", func(t *testing.T) {
		t.Parallel()

		g := newConfigGenerator(t, nil, nil)

		got, err := g.resolveWebAvailable(ctx)
		require.NoError(t, err)
		assert.Equal(t, false, got)
	})
}

func TestSelectByServices_SkipsServerWithoutHostname(t *testing.T) {
	t.Parallel()

	got := selectByServices(map[string]map[string]string{
		"cn-only": {zimbraServiceEnabledAttr: "mailbox"},
		"h1":      {zimbraServiceHostnameAttr: "h1.example.com", zimbraServiceEnabledAttr: "mailbox"},
	}, []string{"mailbox"})

	assert.Len(t, got, 1)
	assert.Contains(t, got, "h1.example.com")
}

func TestGetServersWithService_DropsEmptyHostname(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, nil, nil)
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
	tp.serverProvider = func(string) []ServerInfo {
		return []ServerInfo{{ID: "1"}, {ID: "2", Hostname: "h2"}}
	}

	got, err := tp.getServersWithService(context.Background(), "mailbox")
	require.NoError(t, err)
	assert.Equal(t, []ServerInfo{{ID: "2", Hostname: "h2"}}, got)
}

func TestResolveMailWhitelistIPs_SkipsInjection(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, map[string]string{
		"zimbraReverseProxyIPThrottleWhitelist": "10.0.0.1\n1.2.3.4;}\n10.1.0.0/16 evil\"",
	}, nil)

	got, err := g.resolveMailWhitelistIPs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mail_whitelist_ip    10.0.0.1;\nmail_whitelist_ip    10.1.0.0/16;\n", got)
}

func TestResolveLookupHandlers_Validation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	g := newConfigGenerator(t, map[string]string{
		"zimbraExtensionBindPort":                  "70;72",
		"zimbraReverseProxyAvailableLookupTargets": "127.0.0.1\nbad;host",
	}, nil)

	got, err := g.resolveLookupHandlers(ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://127.0.0.1:7072/service/extension/nginx-lookup", got)

	g = newConfigGenerator(t, map[string]string{"zimbraReverseProxyAvailableLookupTargets": "bad;host"}, nil)
	_, err = g.resolveLookupHandlers(ctx)
	require.Error(t, err, "targets configured but none valid must not fall back silently")
}

func TestUnsafeNginxValuesFallBackToDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	g := newConfigGenerator(t, map[string]string{
		"zimbraReverseProxyErrorHandlerURL":        "http://h/x; } server {",
		"zimbraReverseProxySaslHostFromIP":         "a b;",
		"zimbraWebClientLoginURL":                  "/login;}",
		"zimbraReverseProxySSLSessionCacheSize":    "10m; x",
		"zimbraReverseProxyIpThrottleMsg":          `bad"msg`,
		"zimbraReverseProxyUserThrottleMsg":        "bad\nmsg",
		"zimbraReverseProxyIPLoginLimitTime":       "60",
		"zimbraReverseProxyIPLoginLimit":           "1",
		"zimbraReverseProxyUserLoginLimit":         "1",
		"zimbraReverseProxyUserLoginLimitTime":     "60",
		"zimbraReverseProxyClientCertMode":         "off",
		"zimbraReverseProxyUpstreamFairShmSize":    "32",
		"zimbraReverseProxyAvailableLookupTargets": "",
	}, nil)

	got, err := g.resolveErrorPages(ctx)
	require.NoError(t, err)
	assert.Equal(t, "error_page 502 /zmerror_upstream_502.html;\nerror_page 504 /zmerror_upstream_504.html;", got)

	got, err = g.resolveSaslHostFromIP(ctx)
	require.NoError(t, err)
	assert.Equal(t, nginxOff, got)

	got, err = g.makeLoginURLResolver("zimbraWebClientLoginURL")(ctx)
	require.NoError(t, err)
	assert.Equal(t, staticLoginPath, got)

	got, err = g.resolveSSLSessionCacheSize(ctx)
	require.NoError(t, err)
	assert.Equal(t, "shared:SSL:10m", got)

	g.registerMailVariables()
	g.registerThrottlingVariables()

	for _, kw := range []string{"mail.iprej", "mail.limit.userthrottlemsg"} {
		v := g.Variables[kw]
		require.NotNil(t, v, kw)

		val, err := v.CustomResolver(ctx)
		require.NoError(t, err)
		assert.Equal(t, v.DefaultValue, val, kw)
	}
}

func TestSafeNginxValuesAreKept(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	g := newConfigGenerator(t, map[string]string{
		"zimbraReverseProxyErrorHandlerURL":     "https://err.example.com/h",
		"zimbraReverseProxySSLSessionCacheSize": "64m",
		"zimbraReverseProxyIpThrottleMsg":       "Slow down",
	}, nil)

	got, err := g.resolveErrorPages(ctx)
	require.NoError(t, err)
	assert.Contains(t, got, "error_page 502 https://err.example.com/h?err=502&up=$upstream_addr;")

	got, err = g.resolveSSLSessionCacheSize(ctx)
	require.NoError(t, err)
	assert.Equal(t, "shared:SSL:64m", got)

	g.registerMailVariables()
	g.registerThrottlingVariables()

	val, err := g.Variables["mail.iprej"].CustomResolver(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Slow down", val)
}

func TestListenHost(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1.2.3.4", listenHost("1.2.3.4"))
	assert.Equal(t, "[::1]", listenHost("::1"))
	assert.Equal(t, "[::1]", listenHost("[::1]"))
}

func TestResolveHostToIP_Literals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	assert.Equal(t, "127.0.0.1", resolveHostToIP(ctx, "127.0.0.1"))
	assert.Equal(t, "[::1]", resolveHostToIP(ctx, "::1"))
}

func TestResolveClientCertEnabler(t *testing.T) {
	t.Parallel()

	for mode, want := range map[string]bool{"on": true, "optional": true, "off": false, "": false} {
		g := newConfigGenerator(t, map[string]string{"zimbraReverseProxyClientCertMode": mode}, nil)

		got, err := g.resolveClientCertEnabler(context.Background())
		require.NoError(t, err)
		assert.Equal(t, want, got, "mode %q", mode)
	}
}

func TestVirtualIPPrefix_ResolvesEachHostOnce(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, nil, nil)
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())

	calls := 0
	tp.hostResolver = func(context.Context, string) (net.IP, error) {
		calls++

		return net.ParseIP("10.0.0.1"), nil
	}

	d := &DomainInfo{Name: "a.com", VirtualHostname: "mail.a.com"}
	tp.virtualIPPrefix(context.Background(), d)
	tp.virtualIPPrefix(context.Background(), d)

	assert.Equal(t, 1, calls)
}

func TestWriteVariablesRedactsSecrets(t *testing.T) {
	gen, _ := newLegacyGen(t, Settings{})

	const secret = "s3cr3t-nginx-pass"

	gen.Variables["zmlookup.dpasswd"].Value = secret
	gen.Variables["zmlookup.dpasswd"].DefaultValue = secret

	var out strings.Builder
	require.NoError(t, gen.WriteVariables(context.Background(), &out))
	assert.NotContains(t, out.String(), secret)
	assert.Contains(t, out.String(), "[REDACTED]")
}

func TestVirtualIPPrefix_LastKnownAddressOnDNSFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cache := NewHostIPCache()
	d := &DomainInfo{Name: "a.com", VirtualHostname: "mail.a.com"}

	notFound := &net.DNSError{Err: "no such host", Name: "mail.a.com", IsNotFound: true}
	timeout := &net.DNSError{Err: "i/o timeout", Name: "mail.a.com", IsTimeout: true}

	// SNI off: the prefix carries the address, so a lost address would flip it.
	run := func(ip net.IP, err error) string {
		g := newConfigGenerator(t, map[string]string{"zimbraReverseProxySNIEnabled": "FALSE"}, nil)
		g.hostIPs = cache // each run has its own Generator and processor
		tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
		tp.hostResolver = func(context.Context, string) (net.IP, error) { return ip, err }

		return tp.virtualIPPrefix(ctx, d)
	}

	assert.Equal(t, "10.0.0.1:", run(net.ParseIP("10.0.0.1"), nil))
	assert.Equal(t, "10.0.0.1:", run(nil, timeout), "a timeout keeps the last good address")
	assert.Equal(t, "10.0.0.1:", run(nil, errors.New("resolver unreachable")), "so does any non-NXDOMAIN error")
	assert.Equal(t, "10.0.0.2:", run(net.ParseIP("10.0.0.2"), nil), "a new answer replaces it")
	assert.Equal(t, "", run(nil, notFound), "NXDOMAIN: the host has no record")
	assert.Equal(t, "", run(nil, timeout), "NXDOMAIN forgot the address, nothing is left to keep")

	assert.Equal(t, "10.0.0.3:", run(net.ParseIP("10.0.0.3"), nil))
	assert.Equal(t, "", run(nil, nil), "an empty answer without error also means no record")
	assert.Equal(t, "", run(nil, timeout))

	// No cache (first run, or the caller keeps none): an unresolvable host has no prefix.
	g := newConfigGenerator(t, nil, nil)
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
	tp.hostResolver = func(context.Context, string) (net.IP, error) { return nil, timeout }
	assert.Equal(t, "", tp.virtualIPPrefix(ctx, d))
}
