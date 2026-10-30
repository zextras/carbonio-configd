// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidHostToken(t *testing.T) {
	t.Parallel()

	valid := []string{"mail.example.com", "mbox-1_a.example.com", "10.0.0.1", "[::1]", "::1", "host:8080", "a"}
	for _, s := range valid {
		assert.True(t, validHostToken(s), s)
	}

	invalid := []string{
		"", "a;b", "a b", "a\nb", "a\r", "a}", "a{", "a\x00", "a#b", "a$b", "a\"b", "a'b", "a\\b", "a/b",
		"evil.com;\n    include /etc/passwd", "ü.example.com",
	}
	for _, s := range invalid {
		assert.False(t, validHostToken(s), "%q", s)
	}
}

func TestValidDomainName(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"example.com", "sub-1.example.co.uk", "a_b.example.com"} {
		assert.True(t, validDomainName(s), s)
	}

	for _, s := range []string{"", "..", "a/../b", "../etc", "a/b", "a..b", "a b", "a;b", "a\nb", "[::1]", "a:b", "/abs"} {
		assert.False(t, validDomainName(s), "%q", s)
	}
}

func TestGetDomains_DropsUnsafeValues(t *testing.T) {
	t.Parallel()

	pem := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	gen := &Generator{Variables: map[string]*Variable{}}
	tp := NewTemplateProcessor(gen, t.TempDir(), t.TempDir())
	tp.domainProvider = func() []DomainInfo {
		return []DomainInfo{
			{Name: "good.com", VirtualHostname: "mail.good.com", VirtualIPAddress: "10.0.0.1",
				SSLCertificate: pem, SSLPrivateKey: pem},
			{Name: "inj-vhn.com", VirtualHostname: "x.com;\n  include /etc/shadow"},
			{Name: "inj-vip.com", VirtualHostname: "a.com", VirtualIPAddress: "1.1.1.1; }"},
			{Name: "../../etc/evil", VirtualHostname: "b.com", SSLCertificate: pem, SSLPrivateKey: pem},
			{Name: "a/b.com", VirtualHostname: "c.com"},
			{Name: "novhn.com"},
		}
	}

	got := tp.getDomains(context.Background())

	names := make([]string, 0, len(got))
	for _, d := range got {
		names = append(names, d.Name)
	}

	require.Equal(t, []string{"good.com", "novhn.com"}, names)
}

func TestGetServersWithService_DropsUnsafeHostnames(t *testing.T) {
	t.Parallel()

	gen := &Generator{Variables: map[string]*Variable{}}
	tp := NewTemplateProcessor(gen, t.TempDir(), t.TempDir())
	tp.serverProvider = func(string) []ServerInfo {
		return []ServerInfo{
			{ID: "1", Hostname: "mbox1.example.com"},
			{ID: "2", Hostname: "evil.com:8080;\n    proxy_pass http://x"},
		}
	}

	got := tp.getServersWithService(context.Background(), "mailbox")

	require.Len(t, got, 1)
	assert.Equal(t, "mbox1.example.com", got[0].Hostname)
}

func TestIndexServersByHost_SkipsUnsafeHostnames(t *testing.T) {
	t.Parallel()

	got := indexServersByHost(context.Background(), map[string]map[string]string{
		"ok":     {zimbraServiceHostnameAttr: "mbox1.example.com"},
		"cn1":    {zimbraServiceHostnameAttr: "a.com;\n}"},
		"cn.x":   {},
		"bad cn": {},
	})

	assert.Len(t, got, 2)
	assert.Contains(t, got, "mbox1.example.com")
	assert.Contains(t, got, "cn.x")
}

func TestFormatCapabilities_DropsQuoteBreakout(t *testing.T) {
	t.Parallel()

	got, err := formatIMAPCapabilities([]string{"IMAP4rev1", `X" ; evil on; "`, "ID\ninject", "ENABLE"})
	require.NoError(t, err)
	assert.Equal(t, ` "IMAP4rev1" "ENABLE"`, got)
}
