// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPaths_AreUnderTheCarbonioHome(t *testing.T) {
	p := DefaultPaths()

	require.NotEmpty(t, p.Base)
	assert.Equal(t, filepath.Join(p.Base, "conf", "localconfig.xml"), p.LocalConfig)
	assert.Equal(t, filepath.Join(p.Base, ".install_history"), p.History)
	assert.Equal(t, filepath.Join(p.Base, "libexec", "x"), p.Path("libexec", "x"))
}

func TestOpenLogFile_IsPrivateNamedByTimeAndNeverReused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "log", "sub")
	now := time.Date(2026, 11, 9, 8, 7, 6, 0, time.UTC)

	f, err := OpenLogFile(dir, now)
	require.NoError(t, err)

	defer func() { _ = f.Close() }()

	assert.Equal(t, filepath.Join(dir, "bootstrap.20261109-080706.log"), f.Name())

	st, err := os.Stat(f.Name())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "the log can hold sensitive details")

	_, err = OpenLogFile(dir, now)
	require.ErrorIs(t, err, os.ErrExist, "two runs in the same second must not share a file")
}

func TestOpenLogFile_FailsWhenTheDirectoryCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	_, err := OpenLogFile(filepath.Join(blocker, "sub"), time.Now())
	require.Error(t, err)
}

func TestStepConstructors_LDAP(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)

	setup := StepConfigSetupLdap()
	assert.Equal(t, "configSetupLdap", setup.Name)
	assert.Nil(t, setup.When, "always applies")

	done, err := setup.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	e.Facts.Configured["configSetupLdap"] = true
	done, err = setup.Done(e)
	require.NoError(t, err)
	assert.True(t, done)

	start := StepStartLdap()
	assert.True(t, start.Optional)
	assert.False(t, start.When(e), "new install")

	e.Facts.NewInstall = false
	assert.False(t, start.When(e), "no database yet")
	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)
	assert.True(t, start.When(e))

	upd := StepUpdatePasswordsInLocalConfig()
	assert.True(t, upd.When(e), "existing directory server with a database")

	e.Facts.Enabled[ldapPkgDirectory] = false
	e.Facts.Enabled[PkgMTA] = true
	assert.True(t, upd.When(e), "a node with the MTA keeps the passwords")

	e.Facts.Enabled[PkgMTA] = false
	assert.False(t, upd.When(e), "nothing here needs the passwords")

	e.Facts.Enabled[PkgMTA], e.Cfg.LDAP.Host = true, "other.example.com"
	done, err = upd.Done(e)
	require.NoError(t, err)
	assert.False(t, done, "localconfig does not hold the passwords yet")
}

func TestStepConstructors_Server(t *testing.T) {
	e, _, _ := srvTestEnv(t, &srvFakeDir{})

	assert.Equal(t, "configInitCore", StepConfigInitCore().Name)
	require.NoError(t, StepConfigInitCore().Apply(e))

	web := StepRemoveUnusedWebapps()
	srvEnabled(e, PkgCore)
	assert.False(t, web.When(e))

	srvEnabled(e, PkgCore, PkgAppserver)
	require.True(t, web.When(e))
	require.NoError(t, web.Apply(e))
	assert.True(t, e.Facts.ServiceWebapp)
}

func TestStepConfigSetStoreProxy_WhenAndArguments(t *testing.T) {
	e, _, _ := srvTestEnv(t, &srvFakeDir{servers: map[string]srvEntry{pcTestHost: pcEnt("zimbraServiceEnabled", "mailbox")}})
	w := pcUseFake(t, e, "")
	s := StepConfigSetStoreProxy()

	assert.True(t, s.Optional)
	assert.Nil(t, s.Done, "zmproxyconfig only sets attributes")

	srvEnabled(e, PkgCore, PkgAppserver)
	e.Facts.NewInstall = true
	assert.False(t, s.When(e), "neither proxy asked")

	e.Cfg.Store.MailProxy = true
	require.True(t, s.When(e))

	e.Facts.NewInstall = false
	assert.False(t, s.When(e), "only on a new install")

	e.Facts.NewInstall = true
	srvEnabled(e, PkgCore)
	assert.False(t, s.When(e), "needs the appserver")

	srvEnabled(e, PkgCore, PkgAppserver)
	require.NoError(t, s.Apply(e))

	require.Len(t, w.writes, 1)

	got := pcFlatten(w.writes[0].replace)
	assert.Contains(t, got, "zimbraImapBindPort", "mail proxy enabled")
	assert.Equal(t, "TRUE", got["zimbraReverseProxyLookupTarget"])
	assert.NotContains(t, got, "zimbraMailPort", "the web proxy is left alone")
}

func TestSrvOnIf(t *testing.T) {
	assert.Equal(t, srvOn, srvOnIf(true))
	assert.Equal(t, srvLeave, srvOnIf(false), "a proxy that was not asked is left as it is, not disabled")
}

func TestSrvSameSet(t *testing.T) {
	assert.True(t, srvSameSet([]string{"b", "a", "a"}, []string{"a", "b"}), "order and duplicates do not matter")
	assert.True(t, srvSameSet(nil, []string{}))
	assert.False(t, srvSameSet([]string{"a"}, []string{"a", "b"}))
	assert.False(t, srvSameSet([]string{"a", "c"}, []string{"a", "b"}))

	in := []string{"b", "a"}
	srvSameSet(in, in)
	assert.Equal(t, []string{"b", "a"}, in, "the arguments are not reordered")
}

func TestSrvCOSMatchesAndGlobalHas(t *testing.T) {
	dir := &srvFakeDir{
		global: srvEntry{"zimbraa": {"1"}, "zimbrab": {"2"}},
		cos:    map[string]srvEntry{"default": {"zimbrapreftimezoneid": {"Europe/Rome"}}},
	}
	e, _, _ := srvTestEnv(t, dir)

	ok, err := srvCOSMatches(e, "default", map[string]string{"zimbraPrefTimeZoneId": "Europe/Rome"})
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = srvCOSMatches(e, "default", map[string]string{"zimbraPrefTimeZoneId": "UTC"})
	require.NoError(t, err)
	assert.False(t, ok)

	ok, err = srvCOSMatches(e, "missing", map[string]string{})
	require.NoError(t, err)
	assert.False(t, ok, "a missing class of service never matches")

	ok, err = srvGlobalHas(e, map[string]string{"zimbraA": "1", "zimbraB": "2"})
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = srvGlobalHas(e, map[string]string{"zimbraA": "1", "zimbraB": "3"})
	require.NoError(t, err)
	assert.False(t, ok, "every attribute must match")
}
