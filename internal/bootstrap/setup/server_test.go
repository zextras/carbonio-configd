// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

type srvFakeDir struct {
	global  srvEntry
	servers map[string]srvEntry
	cos     map[string]srvEntry
	vips    []srvEntry
}

func (*srvFakeDir) Close() error { return nil }

func (d *srvFakeDir) Global() (srvEntry, error) { return d.global, nil }

func (d *srvFakeDir) Server(h string) (srvEntry, bool, error) {
	s, ok := d.servers[h]

	return s, ok, nil
}

func (d *srvFakeDir) COS(n string) (srvEntry, bool, error) {
	s, ok := d.cos[n]

	return s, ok, nil
}

func (d *srvFakeDir) ServerNames(filter string) ([]string, error) {
	attr, val, _ := strings.Cut(strings.Trim(filter, "()"), "=")

	var out []string

	for n, s := range d.servers {
		for _, v := range s.values(attr) {
			if v == val {
				out = append(out, n)
			}
		}
	}

	return out, nil
}

func (d *srvFakeDir) VIPDomains() ([]srvEntry, error) { return d.vips, nil }

func srvTestEnv(t *testing.T, dir *srvFakeDir) (*Env, *systest.Fakes, *bytes.Buffer) {
	t.Helper()

	e, fakes, out := testEnv(t)
	e.Cfg = &bootstrap.Config{}
	e.Cfg.Host.Hostname = "mail.example.com"
	e.Cfg.LDAP.Host = "mail.example.com"
	e.Cfg.LDAP.Port = 389
	e.Paths.LocalConfig = filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(e.Paths.LocalConfig, []byte("<?xml version=\"1.0\"?><localconfig></localconfig>"), 0o600))

	old := srvOpenDir
	srvOpenDir = func(*Env) (srvDirectory, error) { return dir, nil }

	t.Cleanup(func() { srvOpenDir = old })

	return e, fakes, out
}

func srvEnabled(e *Env, pkgs ...string) {
	e.Facts.Installed, e.Facts.Enabled = map[string]bool{}, map[string]bool{}

	for _, p := range pkgs {
		e.Facts.Installed[p], e.Facts.Enabled[p] = true, true
	}
}

// provStdin returns the zmprov batch of the first runuser call.
func provStdin(fakes *systest.Fakes) *string {
	var s string

	fakes.Exec.OnPath("runuser", func(c *sys.Cmd) (sys.Result, error) {
		if c.Stdin != nil {
			b := new(bytes.Buffer)
			_, _ = b.ReadFrom(c.Stdin)
			s += b.String()
		}

		return sys.Result{}, nil
	})

	return &s
}

func TestSrvServicePorts_ProxyOffMovesProxyPorts(t *testing.T) {
	dir := &srvFakeDir{servers: map[string]srvEntry{}}
	e, fakes, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, PkgAppserver)
	e.Cfg.Store.Ports = bootstrap.StorePorts{IMAP: 7143, IMAPS: 7993, POP3: 7110, POP3S: 7995, HTTP: 8080, HTTPS: 8443}
	e.Cfg.Proxy.Ports = bootstrap.ProxyPorts{IMAP: 7143, IMAPS: 7993, POP3: 7110, POP3S: 7995, HTTP: 8080, HTTPS: 8443}
	e.Cfg.Store.Mode = "https"
	stdin := provStdin(fakes)

	s := StepConfigSetServicePorts()
	require.True(t, s.When(e))

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, s.Apply(e))
	assert.Contains(t, *stdin, `"zimbraImapProxyBindPort" "143" "zimbraImapSSLProxyBindPort" "993"`)
	assert.Contains(t, *stdin, `"zimbraPop3SSLBindPort" "7995" "zimbraPop3ProxyBindPort" "110" "zimbraPop3SSLProxyBindPort" "995"`)
	assert.Contains(t, *stdin, `"zimbraMailProxyPort" "80" "zimbraMailSSLProxyPort" "443" "zimbraMailMode" "https"`)

	lc, _ := e.LCValue("zimbra_mail_service_port")
	assert.Equal(t, "8080", lc)

	// With the node in that state the step is done.
	dir.servers["mail.example.com"] = srvEntry{
		"zimbraimapbindport": {"7143"}, "zimbraimapsslbindport": {"7993"}, "zimbraimapproxybindport": {"143"},
		"zimbraimapsslproxybindport": {"993"}, "zimbrapop3bindport": {"7110"}, "zimbrapop3sslbindport": {"7995"},
		"zimbrapop3proxybindport": {"110"}, "zimbrapop3sslproxybindport": {"995"}, "zimbramailport": {"8080"},
		"zimbramailsslport": {"8443"}, "zimbramailproxyport": {"80"}, "zimbramailsslproxyport": {"443"},
		"zimbramailmode": {"https"},
	}

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestSrvServiceLists(t *testing.T) {
	dir := &srvFakeDir{servers: map[string]srvEntry{"mail.example.com": {
		"zimbraserviceenabled": {"service-discover", "stats"},
	}}}
	e, fakes, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, PkgAppserver, PkgMTA, "carbonio-clamav", "carbonio-proxy")
	e.Facts.ServiceWebapp = true
	e.Cfg.MTA.Antispam = true
	e.Cfg.Proxy.WebEnabled = true
	stdin := provStdin(fakes)

	require.NoError(t, StepConfigSetEnabledServices().Apply(e))

	for _, want := range []string{
		`"zimbraServiceEnabled" "stats"`, `"zimbraServiceEnabled" "service" "zimbraServiceEnabled" "mailbox"`,
		`"zimbraServiceEnabled" "amavis"`, `"zimbraServiceEnabled" "antispam"`, `"zimbraServiceEnabled" "proxy"`,
		`"zimbraServiceEnabled" "service-discover"`, `"zimbraServiceInstalled" "service-discover"`,
		`"zimbraServiceEnabled" "antivirus"`,
	} {
		assert.Contains(t, *stdin, want)
	}

	assert.NotContains(t, *stdin, `"zimbraServiceEnabled" "opendkim"`)
	assert.NotContains(t, *stdin, `"zimbraServiceEnabled" "cbpolicyd"`)
}

func TestSrvServiceLists_DoesNotIntroduceServiceDiscoverAndDropsIdleProxy(t *testing.T) {
	dir := &srvFakeDir{servers: map[string]srvEntry{"mail.example.com": {}}}
	e, fakes, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, "carbonio-proxy")
	stdin := provStdin(fakes)

	require.NoError(t, StepConfigSetEnabledServices().Apply(e))
	assert.NotContains(t, *stdin, `"zimbraServiceEnabled" "service-discover"`)
	assert.NotContains(t, *stdin, `"zimbraServiceEnabled" "proxy"`)

	// zmiptool runs after the batch.
	assert.Contains(t, fakes.Exec.Lines()[len(fakes.Exec.Lines())-1], "zmiptool")
}

func TestSrvProxyPrefs_DisablesIdleProxyAndSkipsPublicHostnameWhenSet(t *testing.T) {
	dir := &srvFakeDir{global: srvEntry{"zimbrapublicservicehostname": {"x"}}, servers: map[string]srvEntry{}}
	e, fakes, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, "carbonio-proxy")
	e.Cfg.Host.PublicHostname = "public.example.com"
	e.Cfg.Proxy.StrictServerName = true
	stdin := provStdin(fakes)

	require.NoError(t, StepConfigSetProxyPrefs().Apply(e))
	assert.Contains(t, *stdin, `"ms" "mail.example.com" "zimbraReverseProxyStrictServerNameEnabled" "TRUE"`)
	assert.NotContains(t, *stdin, "zimbraPublicServiceHostname")
	assert.False(t, e.Facts.Enabled["carbonio-proxy"])

	for _, c := range fakes.Exec.Calls() {
		assert.NotContains(t, strings.Join(c.Args, " "), "zmproxyconfig")
	}
}

func TestSrvProxyPrefs_ZmproxyconfigArgv(t *testing.T) {
	dir := &srvFakeDir{global: srvEntry{}, servers: map[string]srvEntry{}}
	e, fakes, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, "carbonio-proxy")
	e.Cfg.Host.RequireInterprocessSecurity = true
	e.Cfg.Proxy.WebEnabled = true
	e.Cfg.Proxy.Mode = "redirect"
	e.Cfg.Store.Mode = "https"
	e.Cfg.Store.Ports = bootstrap.StorePorts{IMAP: 7143, IMAPS: 7993, POP3: 7110, POP3S: 7995, HTTP: 8080, HTTPS: 8443}
	e.Cfg.Proxy.Ports = bootstrap.ProxyPorts{IMAP: 143, IMAPS: 993, POP3: 110, POP3S: 995, HTTP: 80, HTTPS: 443}
	e.Cfg.Host.PublicHostname = "public.example.com"
	stdin := provStdin(fakes)

	require.NoError(t, StepConfigSetProxyPrefs().Apply(e))

	lines := strings.Join(fakes.Exec.Lines(), "\n")
	assert.Contains(t, lines, "zmproxyconfig -m -d -o -i 7143:143:7993:993 -p 7110:110:7995:995 -H mail.example.com")
	assert.Contains(t, lines, "zmproxyconfig -U -w -e -o -x redirect -a 8080:80:8443:443 -H mail.example.com")
	assert.Contains(t, *stdin, `"mcf" "zimbraPublicServiceHostname" "public.example.com"`)
	assert.True(t, e.Facts.Enabled["carbonio-proxy"])
}

func TestSrvStoreDefaults_JoinsLookupTargetsOnlyWhenNoneExist(t *testing.T) {
	for name, tc := range map[string]struct {
		global  srvEntry
		servers map[string]srvEntry
		wantALT bool
	}{
		"nothing set": {srvEntry{}, map[string]srvEntry{}, true},
		"other store is target": {
			srvEntry{}, map[string]srvEntry{"other": {"zimbrareverseproxylookuptarget": {"TRUE"}}}, false,
		},
		"attribute set without us": {
			srvEntry{"zimbrareverseproxyavailablelookuptargets": {"other"}}, map[string]srvEntry{}, true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, fakes, _ := srvTestEnv(t, &srvFakeDir{global: tc.global, servers: tc.servers})
			srvEnabled(e, PkgCore, PkgAppserver)
			e.Facts.NewInstall, e.Facts.ServiceWebapp = true, true
			stdin := provStdin(fakes)

			require.NoError(t, StepConfigSetStoreDefaults().Apply(e))
			assert.Equal(t, tc.wantALT, strings.Contains(*stdin, "+zimbraReverseProxyAvailableLookupTargets"))
			assert.Contains(t, *stdin, `"mcf" "+zimbraReverseProxyUpstreamEwsServers" "mail.example.com"`)
			assert.Contains(t, *stdin, `"zimbraReverseProxyLookupTarget" "TRUE" "zimbraMtaAuthTarget" "TRUE"`)
		})
	}
}

func TestSrvLCValues_NewInstallAndDone(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{})
	srvEnabled(e, PkgCore, PkgAppserver)
	e.Facts.NewInstall, e.Facts.ServiceWebapp = true, true
	e.Cfg.Store.MailboxdMemoryMB = 2048
	e.Cfg.Host.RequireInterprocessSecurity = true
	e.Cfg.Notifications.AVNotifyUser = "av@example.org"
	fakes.Exec.OnPath("id", func(c *sys.Cmd) (sys.Result, error) {
		_, _ = c.Stdout.Write([]byte("999\n"))

		return sys.Result{}, nil
	})

	s := StepConfigLCValues()
	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, s.Apply(e))

	lc := srvLCFile(e)
	assert.Equal(t, "ldap://mail.example.com:389", lc["ldap_master_url"])
	assert.Equal(t, "1", lc["ldap_starttls_supported"])
	assert.Equal(t, "false", lc["zimbra_zmprov_default_to_ldap"])
	assert.Equal(t, "2048", lc["mailboxd_java_heap_size"])
	assert.Equal(t, "example.org", lc["av_notify_domain"])
	assert.Equal(t, "999", lc["zimbra_uid"])
	assert.Equal(t, "uid=zimbra,cn=admins,cn=zimbra", lc["zimbra_ldap_userdn"])
	assert.Len(t, lc["mailboxd_keystore_password"], 10)
	assert.NotContains(t, lc, "ldap_dit_base_dn_config")

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)

	// A second apply keeps the generated keystore password.
	pw := lc["mailboxd_keystore_password"]
	require.NoError(t, s.Apply(e))
	assert.Equal(t, pw, srvLCFile(e)["mailboxd_keystore_password"])
}

func TestSrvLCValues_LDAPS(t *testing.T) {
	e, _, _ := srvTestEnv(t, &srvFakeDir{})
	srvEnabled(e, PkgCore)
	e.Facts.NewInstall = true
	e.Cfg.LDAP.Port = 636

	want := srvLCValues(e)
	assert.Equal(t, "ldaps://mail.example.com:636", want["ldap_url"])
	assert.Equal(t, "0", want["ldap_starttls_supported"])
	assert.Equal(t, "true", want["zimbra_zmprov_default_to_ldap"])
}

func TestSrvCreateServerEntry_OrderByReplica(t *testing.T) {
	dir := &srvFakeDir{servers: map[string]srvEntry{}}
	e, fakes, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, srvPkgDirectory)
	e.Cfg.Roles = []string{bootstrap.RoleDirectoryServer}
	e.Cfg.Host.IPMode = "ipv4"
	stdin := provStdin(fakes)

	assert.True(t, StepConfigCreateServerEntry().When(e))
	assert.False(t, StepConfigCreateServerEntryReplica().When(e))

	e.Cfg.LDAP.ReplicationType = bootstrap.ReplicationReplica
	assert.False(t, StepConfigCreateServerEntry().When(e))
	assert.True(t, StepConfigCreateServerEntryReplica().When(e))

	s := StepConfigCreateServerEntry()
	require.NoError(t, s.Apply(e))
	assert.Equal(t, "\"cs\" \"mail.example.com\"\n\"ms\" \"mail.example.com\" \"zimbraIPMode\" \"ipv4\"\nexit\n", *stdin)

	dir.servers["mail.example.com"] = srvEntry{"zimbraipmode": {"ipv4"}}
	done, err := s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestSrvSetProxyBits_OnlyMissing(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{global: srvEntry{"zimbrareverseproxyiploginlimit": {"5"}}})
	srvEnabled(e, PkgCore, srvPkgDirectory)
	stdin := provStdin(fakes)

	require.NoError(t, StepSetProxyBits().Apply(e))
	assert.NotContains(t, *stdin, "zimbraReverseProxyIPLoginLimit\"")
	assert.Contains(t, *stdin, `"zimbraReverseProxyIPLoginLimitTime" "3600"`)

	e.Facts.NewInstall = true
	assert.False(t, StepSetProxyBits().When(e))
}

func TestSrvHostPool_AddsIDOnce(t *testing.T) {
	dir := &srvFakeDir{
		servers: map[string]srvEntry{"mail.example.com": {"zimbraid": {"id-2"}}},
		cos:     map[string]srvEntry{"default": {"zimbramailhostpool": {"id-1"}}},
	}
	e, fakes, _ := srvTestEnv(t, dir)
	stdin := provStdin(fakes)
	s := StepAddServerToHostPool()

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, s.Apply(e))
	assert.Equal(t, "\"mc\" \"default\" \"zimbraMailHostPool\" \"id-1\" \"zimbraMailHostPool\" \"id-2\"\nexit\n", *stdin)

	dir.cos["default"] = srvEntry{"zimbramailhostpool": {"id-1", "id-2"}}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestSrvProxyPortsOnExisting_Condition(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{servers: map[string]srvEntry{}})
	srvEnabled(e, PkgCore, "carbonio-proxy")
	e.Cfg.Proxy.WebEnabled = true
	e.Cfg.Proxy.Ports = bootstrap.ProxyPorts{HTTP: 80, HTTPS: 443}
	stdin := provStdin(fakes)
	s := StepSetProxyPortsOnExisting()

	assert.True(t, s.When(e))
	require.NoError(t, s.Apply(e))
	assert.Contains(t, *stdin, `"zimbraMailProxyPort" "80" "zimbraMailSSLProxyPort" "443"`)

	e.Facts.NewInstall = true
	assert.False(t, s.When(e))
}

func TestSrvWarnDeprecatedVirtualIPs(t *testing.T) {
	dir := &srvFakeDir{
		global: srvEntry{"zimbrareverseproxysnienabled": {"FALSE"}},
		vips:   []srvEntry{{"zimbradomainname": {"a.example.com"}, "zimbravirtualipaddress": {"10.0.0.1", "10.0.0.2"}}},
	}
	e, _, out := srvTestEnv(t, dir)

	require.NoError(t, StepWarnDeprecatedVirtualIPs().Apply(e))
	assert.Contains(t, out.String(), "domain a.example.com has zimbraVirtualIPAddress set (10.0.0.1, 10.0.0.2)")
	assert.Contains(t, out.String(), "zimbraReverseProxySNIEnabled is FALSE")
}

func TestSrvKeyboardAndTimezoneConditions(t *testing.T) {
	e, _, _ := srvTestEnv(t, &srvFakeDir{})
	srvEnabled(e, PkgCore, PkgAppserver, srvPkgDirectory)
	e.Cfg.Host.Timezone = "Europe/Rome"

	assert.True(t, StepConfigSetKeyboardShortcutsPref().When(e))
	assert.True(t, StepConfigSetTimeZonePref().When(e))
	assert.True(t, StepSetSkinLogoURL().When(e))

	e.Facts.NewInstall = true
	assert.False(t, StepConfigSetKeyboardShortcutsPref().When(e))

	e.Cfg.LDAP.Host = "other.example.com"
	assert.False(t, StepConfigSetTimeZonePref().When(e))
}
