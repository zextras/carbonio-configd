// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

type existFakeLDAP struct {
	server    map[string]string
	serverErr error
	global    map[string]string
	globalErr error
	cos       map[string]string
	hostAsked string
	closed    bool
}

func (f *existFakeLDAP) GetServerConfig(host string) (map[string]string, error) {
	f.hostAsked = host

	return f.server, f.serverErr
}

func (f *existFakeLDAP) GetGlobalConfig() (map[string]string, error) { return f.global, f.globalErr }

func (f *existFakeLDAP) ReadAttribute(dn, attr string) (string, bool, error) {
	if dn != "cn=default,cn=cos,cn=zimbra" {
		return "", false, errors.New("no such object " + dn)
	}

	v, ok := f.cos[attr]

	return v, ok, nil
}

func (*existFakeLDAP) BaseDN() string { return "cn=zimbra" }

func (f *existFakeLDAP) Close() error { f.closed = true; return nil }

func existEnv(t *testing.T, lc map[string]string, enabled ...string) *Env {
	t.Helper()

	e, _, _ := testEnv(t)

	var sb strings.Builder

	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<localconfig>\n")

	for k, v := range lc {
		sb.WriteString("  <key name=\"" + k + "\"><value>" + v + "</value></key>\n")
	}

	sb.WriteString("</localconfig>\n")

	e.Paths.LocalConfig = filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(e.Paths.LocalConfig, []byte(sb.String()), 0o600))

	e.Facts = &Facts{
		Installed:       map[string]bool{},
		Enabled:         map[string]bool{},
		EnabledServices: map[string]bool{},
	}

	for _, p := range enabled {
		e.Facts.Installed[p] = true
		e.Facts.Enabled[p] = true
	}

	return e
}

func existOpen(f *existFakeLDAP) existOpenLDAP {
	return func() (existLDAPReader, error) { return f, nil }
}

func existFailOpen() existOpenLDAP {
	return func() (existLDAPReader, error) { return nil, errors.New("connection refused") }
}

func existBaseLC() map[string]string {
	return map[string]string{
		"zimbra_server_hostname":    "Mail1.Example.com",
		"ldap_master_url":           "ldaps://ldap1.example.com:636 ldap://ldap2.example.com:389",
		"ldap_port":                 "636",
		"ldap_root_password":        "rootpw",
		"zimbra_ldap_password":      "adminpw",
		"mailboxd_java_heap_size":   "1024",
		"smtp_source":               "src@example.com",
		"smtp_destination":          "dst@example.com",
		"av_notify_user":            "av@example.com",
		"ldap_dit_base_dn_config":   "cn=zimbra",
		"ldap_postfix_password":     "postpw",
		"ldap_amavis_password":      "",
		"ldap_nginx_password":       "nginxpw",
		"ldap_replication_password": "",
	}
}

func TestExisting_LocalConfig(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgMTA)

	got, err := existingValues(e, existFailOpen())
	require.NoError(t, err)

	assert.Equal(t, "mail1.example.com", got["host.hostname"], "hostname is lower-cased")
	assert.Equal(t, "ldap1.example.com", got["ldap.host"], "first master URL, no scheme or port")
	assert.Equal(t, "636", got["ldap.port"])
	assert.Equal(t, "rootpw", got["ldap.root_password"])
	assert.Equal(t, "adminpw", got["ldap.admin_password"])
	assert.Equal(t, "1024", got["store.mailboxd_memory_mb"])
	assert.Equal(t, "src@example.com", got["notifications.smtp_source"])
	assert.Equal(t, "dst@example.com", got["notifications.smtp_destination"])
	assert.Equal(t, "av@example.com", got["notifications.av_notify_user"])
	assert.Equal(t, "cn=zimbra", got["ldap.base_dn"])

	// MTA: postfix and amavis passwords (empty one falls back to admin);
	// no directory server, so no replication password; no proxy, no nginx.
	assert.Equal(t, "postpw", got["ldap.postfix_password"])
	assert.Equal(t, "adminpw", got["ldap.amavis_password"])
	assert.NotContains(t, got, "ldap.replication_password")
	assert.NotContains(t, got, "ldap.nginx_password")
}

func TestExisting_LDAPHostFallbackAndDirectoryPasswords(t *testing.T) {
	lc := existBaseLC()
	lc["ldap_master_url"] = ""
	lc["ldap_host"] = "ldap9.example.com"
	e := existEnv(t, lc, existPkgDirectory)

	got, err := existingValues(e, existFailOpen())
	require.NoError(t, err)

	assert.Equal(t, "ldap9.example.com", got["ldap.host"])
	assert.Equal(t, "adminpw", got["ldap.replication_password"], "empty replication password falls back to admin")
	assert.Equal(t, "nginxpw", got["ldap.nginx_password"])
}

func TestExisting_EnabledDependencies(t *testing.T) {
	t.Run("directory server with empty ldap host uses itself", func(t *testing.T) {
		lc := existBaseLC()
		lc["ldap_master_url"] = ""
		lc["ldap_host"] = ""
		e := existEnv(t, lc, existPkgDirectory)

		got, err := existingValues(e, existFailOpen())
		require.NoError(t, err)
		assert.Equal(t, "mail1.example.com", got["ldap.host"])
	})

	t.Run("no directory server but ldap host is itself drops host and passwords", func(t *testing.T) {
		lc := existBaseLC()
		lc["ldap_master_url"] = "ldap://mail1.example.com:389"
		e := existEnv(t, lc, PkgMTA)

		got, err := existingValues(e, existFailOpen())
		require.NoError(t, err)
		assert.NotContains(t, got, "ldap.host")
		assert.NotContains(t, got, "ldap.admin_password")
		assert.NotContains(t, got, "ldap.root_password")
	})

	t.Run("appserver with mta sends mail through itself", func(t *testing.T) {
		e := existEnv(t, existBaseLC(), PkgAppserver, PkgMTA)

		got, err := existingValues(e, existFailOpen())
		require.NoError(t, err)
		assert.Equal(t, "mail1.example.com", got["store.smtp_host"])
	})

	t.Run("mta service switches come from the enabled services", func(t *testing.T) {
		e := existEnv(t, existBaseLC(), PkgMTA)
		e.Facts.EnabledServices["antispam"] = true
		e.Facts.EnabledServices["cbpolicyd"] = true

		got, err := existingValues(e, existFailOpen())
		require.NoError(t, err)
		assert.Equal(t, "true", got["mta.antispam"])
		assert.Equal(t, "false", got["mta.antivirus"])
		assert.Equal(t, "false", got["mta.opendkim"])
		assert.Equal(t, "true", got["mta.cbpolicyd"])
	})

	t.Run("clamav decides antivirus without mta", func(t *testing.T) {
		e := existEnv(t, existBaseLC(), existPkgClamav)
		e.Facts.EnabledServices["antivirus"] = true

		got, err := existingValues(e, existFailOpen())
		require.NoError(t, err)
		assert.Equal(t, "true", got["mta.antivirus"])
		assert.NotContains(t, got, "mta.antispam")
	})
}

func existServer() map[string]string {
	return map[string]string{
		"zimbraId":                      "abc",
		"zimbraIPMode":                  "ipv4",
		"zimbraMailMode":                "https",
		"zimbraReverseProxyMailMode":    "redirect",
		"zimbraReverseProxyMailEnabled": "TRUE",
		"zimbraReverseProxyHttpEnabled": "FALSE",
		"zimbraSmtpHostname":            "smtp.example.com",
		"zimbraMtaMyNetworks":           "127.0.0.0/8 [::1]/128\n10.0.0.0/8",
		"zimbraImapBindPort":            "143",
		"zimbraImapSSLBindPort":         "993",
		"zimbraPop3BindPort":            "110",
		"zimbraPop3SSLBindPort":         "995",
		"zimbraImapProxyBindPort":       "143",
		"zimbraImapSSLProxyBindPort":    "993",
		"zimbraPop3ProxyBindPort":       "110",
		"zimbraPop3SSLProxyBindPort":    "995",
		"zimbraMailPort":                "80",
		"zimbraMailSSLPort":             "443",
		"zimbraMailProxyPort":           "8080",
		"zimbraMailSSLProxyPort":        "8443",
	}
}

func existGlobal() map[string]string {
	return map[string]string{
		"zimbraPublicServiceHostname":   "mail.example.com",
		"zimbraDefaultDomainName":       "example.com",
		"zimbraSpamIsSpamAccount":       "spam.x@example.com",
		"zimbraSpamIsNotSpamAccount":    "ham.x@example.com",
		"zimbraAmavisQuarantineAccount": "virus-quarantine.x@example.com",
	}
}

func TestExisting_LDAP(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgMTA, PkgAppserver, existPkgProxy)
	f := &existFakeLDAP{
		server: existServer(),
		global: existGlobal(),
		cos: map[string]string{
			"zimbraPrefUseKeyboardShortcuts": "TRUE",
			"zimbraPrefTimeZoneId":           "Europe/Rome",
		},
	}

	got, err := existingValues(e, existOpen(f))
	require.NoError(t, err)

	assert.Equal(t, "mail1.example.com", f.hostAsked)
	assert.True(t, f.closed)

	want := map[string]string{
		"host.ip_mode":                   "ipv4",
		"store.mode":                     "https",
		"proxy.mode":                     "redirect",
		"proxy.mail_enabled":             "true",
		"proxy.web_enabled":              "false",
		"store.smtp_host":                "smtp.example.com",
		"mta.my_networks":                "127.0.0.0/8,[::1]/128,10.0.0.0/8",
		"host.public_hostname":           "mail.example.com",
		"domain.name":                    "example.com",
		"admin.account":                  "zextras@example.com",
		"store.spam_account":             "spam.x@example.com",
		"store.ham_account":              "ham.x@example.com",
		"store.virus_quarantine_account": "virus-quarantine.x@example.com",
		"store.keyboard_shortcuts":       "true",
		"host.timezone":                  "Europe/Rome",
	}
	for k, v := range want {
		assert.Equal(t, v, got[k], k)
	}

	// Mail proxy on a node with the proxy: mailbox ports move to 7xxx, the
	// proxy keeps the standard ones. Web proxy off and the proxy is enabled:
	// web ports untouched.
	assert.Equal(t, "7143", got["store.ports.imap"])
	assert.Equal(t, "7993", got["store.ports.imaps"])
	assert.Equal(t, "7110", got["store.ports.pop3"])
	assert.Equal(t, "7995", got["store.ports.pop3s"])
	assert.Equal(t, "143", got["proxy.ports.imap"])
	assert.Equal(t, "993", got["proxy.ports.imaps"])
	assert.Equal(t, "110", got["proxy.ports.pop3"])
	assert.Equal(t, "995", got["proxy.ports.pop3s"])
	assert.Equal(t, "80", got["store.ports.http"])
	assert.Equal(t, "443", got["store.ports.https"])
	assert.Equal(t, "8080", got["proxy.ports.http"])
	assert.Equal(t, "8443", got["proxy.ports.https"])
}

func TestExisting_LDAPPortsWithoutProxy(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgAppserver)
	srv := existServer()
	srv["zimbraReverseProxyMailEnabled"] = "FALSE"
	f := &existFakeLDAP{server: srv, global: existGlobal()}

	got, err := existingValues(e, existOpen(f))
	require.NoError(t, err)

	// Same port on both sides without a proxy here: the proxy side moves.
	assert.Equal(t, "143", got["store.ports.imap"])
	assert.Equal(t, "7143", got["proxy.ports.imap"])
	assert.Equal(t, "7993", got["proxy.ports.imaps"])
	assert.Equal(t, "7110", got["proxy.ports.pop3"])
	assert.Equal(t, "7995", got["proxy.ports.pop3s"])
	assert.Equal(t, "80", got["store.ports.http"])
	assert.Equal(t, "8080", got["proxy.ports.http"])
}

func TestExisting_LDAPWebProxyPorts(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgAppserver, existPkgProxy)
	srv := existServer()
	srv["zimbraReverseProxyHttpEnabled"] = "TRUE"
	srv["zimbraReverseProxyMailEnabled"] = "FALSE"
	srv["zimbraMailProxyPort"] = ""
	srv["zimbraMailSSLProxyPort"] = ""
	f := &existFakeLDAP{server: srv, global: existGlobal()}

	got, err := existingValues(e, existOpen(f))
	require.NoError(t, err)

	// Proxy ports unset and the mailbox on 80/443: the proxy takes 80/443 and
	// the mailbox moves to 8080/8443.
	assert.Equal(t, "8080", got["store.ports.http"])
	assert.Equal(t, "8443", got["store.ports.https"])
	assert.Equal(t, "80", got["proxy.ports.http"])
	assert.Equal(t, "443", got["proxy.ports.https"])
}

func TestExisting_LDAPUnreachableGivesLocalValuesOnly(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgMTA)

	got, err := existingValues(e, existFailOpen())
	require.NoError(t, err)

	assert.Equal(t, "ldap1.example.com", got["ldap.host"])
	assert.NotContains(t, got, "domain.name")
	assert.NotContains(t, got, "store.ports.http")

	f := &existFakeLDAP{globalErr: errors.New("timeout")}

	got, err = existingValues(e, existOpen(f))
	require.NoError(t, err)
	assert.NotContains(t, got, "domain.name")
	assert.True(t, f.closed)
}

func TestExisting_LDAPNotReadWhenLocalMasterIsNotConfigured(t *testing.T) {
	lc := existBaseLC()
	lc["ldap_master_url"] = "ldap://mail1.example.com:389"
	e := existEnv(t, lc, PkgMTA)

	opened := false
	got, err := existingValues(e, func() (existLDAPReader, error) {
		opened = true

		return &existFakeLDAP{}, nil
	})
	require.NoError(t, err)
	assert.False(t, opened, "ldap host is this node without a directory server")
	assert.NotContains(t, got, "ldap.host")

	lc = existBaseLC()
	delete(lc, "zimbra_ldap_password")
	lc["zimbra_ldap_password"] = ""
	e = existEnv(t, lc, PkgMTA)

	_, err = existingValues(e, func() (existLDAPReader, error) {
		opened = true

		return &existFakeLDAP{}, nil
	})
	require.NoError(t, err)
	assert.False(t, opened, "remote master without admin password is incomplete")
}

func TestExisting_MissingServerEntryStillReadsGlobal(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgMTA)
	f := &existFakeLDAP{serverErr: errors.New("server not found"), global: existGlobal()}

	got, err := existingValues(e, existOpen(f))
	require.NoError(t, err)

	assert.Equal(t, "example.com", got["domain.name"])
	assert.NotContains(t, got, "store.ports.http")
	assert.NotContains(t, got, "store.mode")
}

func TestExisting_GlobalSMTPHostFallback(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgMTA)
	srv := existServer()
	srv["zimbraSmtpHostname"] = ""
	g := existGlobal()
	g["zimbraSmtpHostname"] = "relay.example.com"

	got, err := existingValues(e, existOpen(&existFakeLDAP{server: srv, global: g}))
	require.NoError(t, err)
	assert.Equal(t, "relay.example.com", got["store.smtp_host"])

	g["zimbraSmtpHostname"] = "localhost"

	got, err = existingValues(e, existOpen(&existFakeLDAP{server: srv, global: g}))
	require.NoError(t, err)
	assert.NotContains(t, got, "store.smtp_host")
}

func TestExisting_ValuesApplyToConfig(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgMTA, PkgAppserver)
	f := &existFakeLDAP{
		server: existServer(), global: existGlobal(),
		cos: map[string]string{"zimbraPrefUseKeyboardShortcuts": "FALSE"},
	}

	got, err := existingValues(e, existOpen(f))
	require.NoError(t, err)

	var cfg bootstrap.Config

	for path, val := range got {
		fld := bootstrap.FieldByPath(path)
		require.NotNil(t, fld, "%s is not a config key", path)

		if fld.TypeName() == "list" || strings.HasSuffix(fld.TypeName(), "list") {
			require.NoError(t, fld.SetList(&cfg, strings.Split(val, ",")), path)

			continue
		}

		require.NoError(t, fld.Set(&cfg, val), "%s=%q", path, val)
	}

	assert.Equal(t, []string{"127.0.0.0/8", "[::1]/128", "10.0.0.0/8"}, cfg.MTA.MyNetworks)
	assert.False(t, cfg.Store.KeyboardShortcuts)
	assert.Equal(t, 1024, cfg.Store.MailboxdMemoryMB)
}

func TestExisting_MissingLocalConfigUsesDefaultsWithoutError(t *testing.T) {
	e := existEnv(t, map[string]string{})
	e.Paths.LocalConfig = filepath.Join(t.TempDir(), "missing.xml")

	got, err := existingValues(e, existFailOpen())
	require.NoError(t, err)
	assert.NotContains(t, got, "domain.name")
}
