// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepConfigLCMailboxd_JettyDirectoryDecidesTheServer(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{})
	s := StepConfigLCMailboxd()

	srvEnabled(e, PkgCore)
	assert.False(t, s.When(e))

	srvEnabled(e, PkgCore, PkgAppserver)
	require.True(t, s.When(e))

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, s.Apply(e))

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "/opt/zextras/conf/keystore", lc["mailboxd_keystore"], "no jetty: the conf keystore")
	assert.Empty(t, lc["mailboxd_server"])

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)

	fakes.FS.PutDir("/opt/zextras/mailboxd", 0o755)
	done, err = s.Done(e)
	require.NoError(t, err, "the jetty directory appeared: the stored values are stale")
	assert.False(t, done)

	require.NoError(t, s.Apply(e))

	lc, err = e.LC()
	require.NoError(t, err)
	assert.Equal(t, "/opt/zextras/mailboxd/etc/keystore", lc["mailboxd_keystore"])
	assert.Equal(t, "jetty", lc["mailboxd_server"])
}

func TestSrvMailboxdMemoryMB(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{})

	_, ok := srvMailboxdMemoryMB(e)
	assert.False(t, ok, "no /proc/meminfo")

	e.Cfg.Store.MailboxdMemoryMB = 3000
	mb, ok := srvMailboxdMemoryMB(e)
	assert.True(t, ok)
	assert.Equal(t, 3000, mb, "the configured value wins")

	e.Cfg.Store.MailboxdMemoryMB = 0

	for name, tc := range map[string]struct {
		meminfo string
		want    int
		ok      bool
	}{
		"8 GB: a quarter":          {"MemTotal:        8388608 kB\nMemFree: 1 kB\n", 2048, true},
		"32 GB: a fifth":           {"MemFree: 1 kB\nMemTotal: 33554432 kB\n", 6553, true},
		"exactly 16 GB: a quarter": {"MemTotal: 16777216 kB\n", 4096, true},
		"no MemTotal":              {"MemFree: 1 kB\n", 0, false},
		"empty value":              {"MemTotal:\n", 0, false},
		"not a number":             {"MemTotal: lots kB\n", 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			fakes.FS.Put("/proc/meminfo", []byte(tc.meminfo), 0o444)

			mb, ok := srvMailboxdMemoryMB(e)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, mb)
		})
	}
}

func TestStepConfigSetStoreDefaults_DoneOnExistingInstall(t *testing.T) {
	dir := &srvFakeDir{servers: map[string]srvEntry{}}
	e, _, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, PkgAppserver)
	e.Facts.ServiceWebapp = true
	s := StepConfigSetStoreDefaults()

	require.True(t, s.When(e))

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done, "no server entry")

	dir.servers["mail.example.com"] = srvEntry{
		"zimbrareverseproxylookuptarget": {"TRUE"}, "zimbramtaauthtarget": {"FALSE"},
	}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.False(t, done, "auth target must follow the service webapp")

	dir.servers["mail.example.com"]["zimbramtaauthtarget"] = []string{"TRUE"}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestStepConfigSetStoreDefaults_NewInstallDoneNeedsTheHostRegistered(t *testing.T) {
	dir := &srvFakeDir{
		global: srvEntry{},
		servers: map[string]srvEntry{"mail.example.com": {
			"zimbrareverseproxylookuptarget": {"TRUE"}, "zimbramtaauthtarget": {"TRUE"},
		}},
	}
	e, fakes, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, PkgAppserver)
	e.Facts.ServiceWebapp, e.Facts.NewInstall = true, true
	s := StepConfigSetStoreDefaults()

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done, "the host is not a lookup target yet")

	// listed as lookup target but not as EWS upstream
	dir.global = srvEntry{"zimbrareverseproxyavailablelookuptargets": {"mail.example.com"}}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	dir.global["zimbrareverseproxyupstreamewsservers"] = []string{"mail.example.com"}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)

	// another host holds the lookup list: this one must be added to it
	dir.global = srvEntry{"zimbrareverseproxyavailablelookuptargets": {"other.example.com"}}
	provTestLDAP.set(provGlobalDN, "zimbraReverseProxyAvailableLookupTargets", "other.example.com")
	require.NoError(t, s.Apply(e))
	assert.Equal(t, []string{"other.example.com", "mail.example.com"},
		provTestLDAP.values(provGlobalDN, "zimbraReverseProxyAvailableLookupTargets"))
	assert.Equal(t, []string{"mail.example.com"}, provTestLDAP.values(provGlobalDN, "zimbraReverseProxyUpstreamEwsServers"))
	assert.Empty(t, fakes.Exec.Calls(), "the attributes are written to LDAP, no zmprov")
}

func TestStepConfigSetStoreDefaults_NoHostAddWhenAnotherStoreIsAlreadyATarget(t *testing.T) {
	dir := &srvFakeDir{
		global:  srvEntry{},
		servers: map[string]srvEntry{"other.example.com": {"zimbrareverseproxylookuptarget": {"TRUE"}}},
	}
	e, _, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, PkgAppserver)
	e.Facts.ServiceWebapp, e.Facts.NewInstall = true, true

	require.NoError(t, StepConfigSetStoreDefaults().Apply(e))
	assert.Empty(t, provTestLDAP.values(provGlobalDN, "zimbraReverseProxyAvailableLookupTargets"),
		"an existing lookup target already serves the proxy")
	assert.Equal(t, []string{"mail.example.com"}, provTestLDAP.values(provGlobalDN, "zimbraReverseProxyUpstreamEwsServers"))
}

func TestStepConfigSetEnabledServices_Done(t *testing.T) {
	dir := &srvFakeDir{servers: map[string]srvEntry{}}
	e, _, _ := srvTestEnv(t, dir)
	srvEnabled(e, PkgCore, PkgAppserver)
	e.Facts.ServiceWebapp = true
	s := StepConfigSetEnabledServices()

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done, "no server entry")

	dir.servers["mail.example.com"] = srvEntry{
		"zimbraserviceinstalled": {"stats", "service-discover", "mailbox"},
		"zimbraserviceenabled":   {"mailbox", "stats", "service"},
	}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done, "same sets in any order")

	dir.servers["mail.example.com"]["zimbraserviceenabled"] = []string{"mailbox", "stats", "service", "service-discover"}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done, "service-discover is kept when the entry has it")

	dir.servers["mail.example.com"]["zimbraserviceenabled"] = []string{"mailbox", "stats"}
	done, err = s.Done(e)
	require.NoError(t, err)
	assert.False(t, done, "the service webapp is missing")
}
