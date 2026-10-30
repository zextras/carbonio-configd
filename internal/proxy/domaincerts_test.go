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
)

const (
	testCertPEM = "-----BEGIN CERTIFICATE-----\nMIIBcert\n-----END CERTIFICATE-----\n"
	testKeyPEM  = "-----BEGIN PRIVATE KEY-----\nMIIBkey\n-----END PRIVATE KEY-----\n"
)

func newDomainCertProcessor(t *testing.T, domains []DomainInfo) (*TemplateProcessor, string) {
	t.Helper()

	confDir := t.TempDir()
	gen := &Generator{
		ConfDir: confDir,
		Variables: map[string]*Variable{
			"ssl.crt.default": {Keyword: "ssl.crt.default", ValueType: ValueTypeString, Value: confDir + "/nginx.crt"},
			"ssl.key.default": {Keyword: "ssl.key.default", ValueType: ValueTypeString, Value: confDir + "/nginx.key"},
		},
	}
	tp := NewTemplateProcessor(gen, t.TempDir(), t.TempDir())
	tp.domainProvider = func() []DomainInfo { return domains }

	return tp, filepath.Join(confDir, "domaincerts")
}

func TestSetDomainVariables_MapsPEMToDomainCertsPath(t *testing.T) {
	t.Parallel()

	tp, dir := newDomainCertProcessor(t, nil)

	tp.setDomainVariables(&DomainInfo{Name: "example.com", SSLCertificate: testCertPEM, SSLPrivateKey: testKeyPEM})
	assert.Equal(t, filepath.Join(dir, "example.com.crt"), tp.generator.Variables["ssl.crt"].Value)
	assert.Equal(t, filepath.Join(dir, "example.com.key"), tp.generator.Variables["ssl.key"].Value)
	tp.clearDomainVariables()

	tp.setDomainVariables(&DomainInfo{Name: "plain.com"})
	assert.Equal(t, tp.generator.ConfDir+"/nginx.crt", tp.generator.Variables["ssl.crt"].Value)
	assert.Equal(t, tp.generator.ConfDir+"/nginx.key", tp.generator.Variables["ssl.key"].Value)
}

func TestSyncDomainCerts_WritesFilesAndSkipsUnchanged(t *testing.T) {
	t.Parallel()

	tp, dir := newDomainCertProcessor(t, []DomainInfo{
		{Name: "example.com", VirtualHostname: "mail.example.com", SSLCertificate: testCertPEM, SSLPrivateKey: testKeyPEM},
		{Name: "nocert.com", VirtualHostname: "mail.nocert.com"},
		{Name: "halfpair.com", VirtualHostname: "mail.half.com", SSLCertificate: testCertPEM},
	})

	require.NoError(t, tp.syncDomainCerts(context.Background()))

	crt := filepath.Join(dir, "example.com.crt")
	key := filepath.Join(dir, "example.com.key")

	got, err := os.ReadFile(crt)
	require.NoError(t, err)
	assert.Equal(t, testCertPEM, string(got))

	got, err = os.ReadFile(key)
	require.NoError(t, err)
	assert.Equal(t, testKeyPEM, string(got))

	info, err := os.Stat(key)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	assert.NoFileExists(t, filepath.Join(dir, "nocert.com.crt"))
	assert.NoFileExists(t, filepath.Join(dir, "halfpair.com.crt"))

	// Second run with identical content must not rewrite (same inode/mtime).
	before, err := os.Stat(crt)
	require.NoError(t, err)

	require.NoError(t, tp.syncDomainCerts(context.Background()))

	after, err := os.Stat(crt)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "unchanged cert must not be rewritten")
	assert.Equal(t, before.ModTime(), after.ModTime())

	// Changed content is rewritten.
	tp.domainProvider = func() []DomainInfo {
		return []DomainInfo{{Name: "example.com", VirtualHostname: "mail.example.com",
			SSLCertificate: "NEW-CERT", SSLPrivateKey: testKeyPEM}}
	}

	require.NoError(t, tp.syncDomainCerts(context.Background()))

	got, err = os.ReadFile(crt)
	require.NoError(t, err)
	assert.Equal(t, "NEW-CERT", string(got))
}

func TestSyncDomainCerts_RemovesObsoleteFiles(t *testing.T) {
	t.Parallel()

	tp, dir := newDomainCertProcessor(t, []DomainInfo{
		{Name: "keep.com", VirtualHostname: "mail.keep.com", SSLCertificate: testCertPEM, SSLPrivateKey: testKeyPEM},
	})

	require.NoError(t, os.MkdirAll(dir, 0o755))

	for _, name := range []string{"gone.com.crt", "gone.com.key", "other.org.crt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}

	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0o755))

	require.NoError(t, tp.syncDomainCerts(context.Background()))

	assert.NoFileExists(t, filepath.Join(dir, "gone.com.crt"))
	assert.NoFileExists(t, filepath.Join(dir, "gone.com.key"))
	assert.NoFileExists(t, filepath.Join(dir, "other.org.crt"))
	assert.FileExists(t, filepath.Join(dir, "keep.com.crt"))
	assert.FileExists(t, filepath.Join(dir, "keep.com.key"))
	assert.DirExists(t, filepath.Join(dir, "subdir"))
}

func TestSyncDomainCerts_UnsafeDomainNameSkipped(t *testing.T) {
	t.Parallel()

	tp, dir := newDomainCertProcessor(t, []DomainInfo{
		{Name: "../../evil", VirtualHostname: "e.com", SSLCertificate: testCertPEM, SSLPrivateKey: testKeyPEM},
		{Name: "a/b", VirtualHostname: "f.com", SSLCertificate: testCertPEM, SSLPrivateKey: testKeyPEM},
	})

	require.NoError(t, tp.syncDomainCerts(context.Background()))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)

	// Nothing escaped the conf dir either.
	assert.NoFileExists(t, filepath.Join(filepath.Dir(tp.generator.ConfDir), "evil.crt"))
}

func TestSyncDomainCerts_DryRunTouchesNothing(t *testing.T) {
	t.Parallel()

	tp, dir := newDomainCertProcessor(t, []DomainInfo{
		{Name: "example.com", VirtualHostname: "m.example.com", SSLCertificate: testCertPEM, SSLPrivateKey: testKeyPEM},
	})
	tp.generator.DryRun = true

	require.NoError(t, tp.syncDomainCerts(context.Background()))
	assert.NoDirExists(t, dir)
}
