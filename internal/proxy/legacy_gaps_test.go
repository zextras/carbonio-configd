// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func newConfigGenerator(t *testing.T, global, server map[string]string) *Generator {
	t.Helper()

	g := &Generator{
		env:          sys.Real(),
		ConfDir:      t.TempDir(),
		WorkingDir:   t.TempDir(),
		GlobalConfig: &config.GlobalConfig{Data: config.NewConfigMap()},
		ServerConfig: &config.ServerConfig{Data: config.NewConfigMap()},
		Variables: map[string]*Variable{
			"ssl.clientcertca.enabled": {Keyword: "ssl.clientcertca.enabled", ValueType: ValueTypeEnabler},
			"ssl.clientcertmode.default": {
				Keyword: "ssl.clientcertmode.default", ValueType: ValueTypeString, Value: "off"},
			"ssl.clientcertca.default": {
				Keyword: "ssl.clientcertca.default", ValueType: ValueTypeString, Value: "/conf/nginx.client.ca.crt"},
		},
	}
	g.GlobalConfig.Data.Replace(global)
	g.ServerConfig.Data.Replace(server)

	return g
}

func TestPerVirtualHostnameEnabled(t *testing.T) {
	t.Parallel()

	key := perVirtualHostnameAttr
	assert.True(t, newConfigGenerator(t, nil, nil).perVirtualHostnameEnabled(), "unset => inherited schema default TRUE")
	assert.True(t, newConfigGenerator(t, map[string]string{key: "TRUE"}, nil).perVirtualHostnameEnabled())
	assert.False(t, newConfigGenerator(t, map[string]string{key: "TRUE"},
		map[string]string{key: "FALSE"}).perVirtualHostnameEnabled(), "server overrides global")
	assert.False(t, newConfigGenerator(t, map[string]string{key: "FALSE"}, nil).perVirtualHostnameEnabled())
}

func TestLoadDomains_GatedByPerVirtualHostname(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, map[string]string{perVirtualHostnameAttr: "FALSE"}, nil)
	g.LdapClient = &ldap.Ldap{NativeClient: &ldap.Client{}}
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())

	domains, err := tp.loadDomains(context.Background())
	require.NoError(t, err)
	assert.Empty(t, domains)
}

func TestSyncDomainCerts_GateOffDeletesAllDomainCertFiles(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, map[string]string{perVirtualHostnameAttr: "FALSE"}, nil)
	g.LdapClient = &ldap.Ldap{NativeClient: &ldap.Client{}}
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())

	dir := g.domainCertsDir()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "old.com.crt"), []byte("x"), 0o600))

	require.NoError(t, tp.syncDomainCerts(context.Background()))
	assert.NoFileExists(t, filepath.Join(dir, "old.com.crt"))
}

func TestLoadAllClientCertCA_DedupAndOrder(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, map[string]string{clientCertCAAttr: "GLOBAL"}, nil)
	got := g.loadAllClientCertCA([]DomainInfo{
		{Name: "a.com", ClientCertCA: "CA1"},
		{Name: "b.com", ClientCertCA: "GLOBAL"},
		{Name: "c.com", ClientCertCA: "CA2"},
		{Name: "d.com", ClientCertCA: "CA1"},
		{Name: "e.com"},
	})
	assert.Equal(t, "GLOBAL\nCA1\nCA2", got)
}

func TestSyncClientCA_WritesOnlyWhenChanged(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, map[string]string{clientCertCAAttr: "GLOBAL"}, nil)
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
	ctx := context.Background()
	path := filepath.Join(g.ConfDir, clientCertCAFilename)

	require.NoError(t, tp.syncClientCA(ctx, nil))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "GLOBAL", string(content))
	assert.Equal(t, true, g.Variables["ssl.clientcertca.enabled"].Value)

	before, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, tp.syncClientCA(ctx, nil))

	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, before.ModTime().Equal(after.ModTime()), "unchanged content must not rewrite the file")

	require.NoError(t, tp.syncClientCA(ctx, []DomainInfo{{Name: "a.com", ClientCertCA: "CA1"}}))

	content, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "GLOBAL\nCA1", string(content))
}

func TestSyncClientCA_EmptyCA(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	g := newConfigGenerator(t, nil, nil)
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
	require.NoError(t, tp.syncClientCA(ctx, nil))
	assert.Equal(t, false, g.Variables["ssl.clientcertca.enabled"].Value)
	assert.NoFileExists(t, filepath.Join(g.ConfDir, clientCertCAFilename))

	g = newConfigGenerator(t, map[string]string{"zimbraReverseProxyClientCertMode": "on"}, nil)
	tp = NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
	require.ErrorIs(t, tp.syncClientCA(ctx, nil), errClientCAMissing)

	g = newConfigGenerator(t, nil, nil)
	tp = NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())
	require.ErrorIs(t, tp.syncClientCA(ctx, []DomainInfo{{Name: "a.com", ClientCertMode: "optional"}}),
		errClientCAMissing)
	require.NoError(t, tp.syncClientCA(ctx, []DomainInfo{{Name: "a.com", ClientCertMode: "optional_no_ca"}}))
}

func TestSetDomainClientCertVariables(t *testing.T) {
	t.Parallel()

	g := newConfigGenerator(t, nil, nil)
	g.Variables["web.sso.certauth.enabled"] = &Variable{Keyword: "web.sso.certauth.enabled", Value: false}
	tp := NewTemplateProcessor(g, g.env.FS, t.TempDir(), t.TempDir())

	tp.setDomainVariables(&DomainInfo{Name: "a.com", ClientCertMode: "on", ClientCertCA: "CA"})
	assert.Equal(t, "on", g.Variables["ssl.clientcertmode"].Value)
	assert.Equal(t, true, g.Variables["web.sso.certauth.enabled"].Value)
	assert.Equal(t, filepath.Join(g.ConfDir, "domaincerts", "a.com.client.ca.crt"), g.Variables["ssl.clientcertca"].Value)
	tp.clearDomainVariables()
	assert.NotContains(t, g.Variables, "ssl.clientcertmode")
	assert.NotContains(t, g.Variables, "ssl.clientcertca")
	assert.Equal(t, false, g.Variables["web.sso.certauth.enabled"].Value, "restored")

	tp.setDomainVariables(&DomainInfo{Name: "b.com", ClientCertMode: "optional_no_ca"})
	assert.Equal(t, false, g.Variables["web.sso.certauth.enabled"].Value)
	tp.clearDomainVariables()

	tp.setDomainVariables(&DomainInfo{Name: "c.com"})
	assert.Equal(t, "off", g.Variables["ssl.clientcertmode"].Value, "falls back to .default")
	assert.Equal(t, "/conf/nginx.client.ca.crt", g.Variables["ssl.clientcertca"].Value)
	assert.Equal(t, false, g.Variables["web.sso.certauth.enabled"].Value)
}

// newCertbotGenerator returns a generator whose host boundary is an in-memory
// filesystem and a scripted command runner, with WorkingDir /opt/zextras.
func newCertbotGenerator(t *testing.T) (*Generator, *systest.Fakes) {
	t.Helper()

	f := systest.New()
	g := newConfigGenerator(t, nil, nil)
	g.env = f.System()
	g.WorkingDir = "/opt/zextras"
	g.ConfDir = "/opt/zextras/conf"

	return g, f
}

// mkCertbotLive creates live/<name> in the fake filesystem holding files.
func mkCertbotLive(t *testing.T, g *Generator, f *systest.Fakes, name string, files map[string]string) {
	t.Helper()

	require.NoError(t, g.env.FS.MkdirAll(g.certbotLiveDir(), 0o755))

	dir := filepath.Join(g.certbotLiveDir(), name)
	f.FS.PutDir(dir, 0o755)

	for file, c := range files {
		f.FS.Put(filepath.Join(dir, file), []byte(c), 0o600)
	}
}

func TestDeleteCertbotConfigs_ArgvAndSubstringRetention(t *testing.T) {
	t.Parallel()

	g, f := newCertbotGenerator(t)
	tp := NewTemplateProcessor(g, g.env.FS, "/tpl", "/out")

	for _, n := range []string{"keep.com", "sub.keep.com", "gone.com", "-evil", "also-gone.org"} {
		mkCertbotLive(t, g, f, n, nil)
	}

	tp.deleteCertbotConfigs(context.Background(), []DomainInfo{{Name: "keep.com"}})

	// ReadDir order is sorted; "sub.keep.com" contains "keep.com" so it stays.
	assert.Equal(t, []string{"/opt/zextras/libexec/certbot delete also-gone.org gone.com"}, f.Exec.Lines())
}

func TestDeleteCertbotConfigs_NoStaleOrDryRunDoesNotExec(t *testing.T) {
	t.Parallel()

	g, f := newCertbotGenerator(t)
	tp := NewTemplateProcessor(g, g.env.FS, "/tpl", "/out")

	tp.deleteCertbotConfigs(context.Background(), nil)
	mkCertbotLive(t, g, f, "gone.com", nil)

	g.DryRun = true
	tp.deleteCertbotConfigs(context.Background(), nil)
	assert.Empty(t, f.Exec.Lines())
}

func TestDeleteCertbotConfigs_FailureIsLoggedNotReturned(t *testing.T) {
	t.Parallel()

	g, f := newCertbotGenerator(t)
	tp := NewTemplateProcessor(g, g.env.FS, "/tpl", "/out")
	mkCertbotLive(t, g, f, "gone.com", nil)
	f.Exec.OnPath(g.certbotBinary(), systest.Fail(1, "boom"))

	tp.deleteCertbotConfigs(context.Background(), nil)

	assert.Equal(t, []string{"/opt/zextras/libexec/certbot delete gone.com"}, f.Exec.Lines())
}

func TestSaveCertbotPairsToLDAP(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	g, f := newCertbotGenerator(t)
	tp := NewTemplateProcessor(g, g.env.FS, "/tpl", "/out")
	mkCertbotLive(t, g, f, "le.com", map[string]string{certbotFullchainName: "FULL", certbotPrivkeyName: "PRIV"})
	mkCertbotLive(t, g, f, "partial.com", map[string]string{certbotFullchainName: "FULL"})

	// Unchanged values: no LDAP write needed (no client configured) and no error.
	domains, err := tp.saveCertbotPairsToLDAP(ctx, []DomainInfo{
		{Name: "le.com", SSLCertificate: "FULL", SSLPrivateKey: "PRIV"},
		{Name: "partial.com"},
		{Name: "other.com"},
	})
	require.NoError(t, err)
	assert.Equal(t, "FULL", domains[0].SSLCertificate)
	assert.Empty(t, domains[1].SSLCertificate, "incomplete pair is ignored")

	// Changed values need LDAP; without a client generation aborts.
	_, err = tp.saveCertbotPairsToLDAP(ctx, []DomainInfo{{Name: "le.com", SSLCertificate: "OLD"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to update certificate and private key for domain le.com")

	// Dry-run touches nothing and does not fail.
	g.DryRun = true
	_, err = tp.saveCertbotPairsToLDAP(ctx, []DomainInfo{{Name: "le.com", SSLCertificate: "OLD"}})
	require.NoError(t, err)
}

func TestGeneratorWriteSideEffectsThroughFS(t *testing.T) {
	t.Parallel()

	g, f := newCertbotGenerator(t)
	g.IncludesDir = "/opt/zextras/conf/nginx/includes"
	ctx := context.Background()

	require.NoError(t, g.ensureIncludesDir(ctx))
	require.NoError(t, g.writeFile(ctx, "/opt/zextras/conf/nginx/includes/a.conf", []byte("a")))
	require.NoError(t, writeIfChanged(ctx, g.env.FS, "/opt/zextras/conf/domaincerts/x.crt", "PEM"))
	// Unchanged content with the right mode is not rewritten.
	require.NoError(t, writeIfChanged(ctx, g.env.FS, "/opt/zextras/conf/domaincerts/x.crt", "PEM"))

	assert.Equal(t, []string{
		"fs.mkdir /opt/zextras/conf/nginx/includes mode=0755",
		"fs.write-atomic /opt/zextras/conf/nginx/includes/a.conf mode=0640",
		"fs.write-atomic /opt/zextras/conf/domaincerts/x.crt mode=0640",
	}, f.Rec.Strings())

	g.DryRun = true
	require.NoError(t, g.writeFile(ctx, "/opt/zextras/conf/nginx/includes/b.conf", []byte("b")))
	assert.False(t, f.FS.Exists("/opt/zextras/conf/nginx/includes/b.conf"))
}
