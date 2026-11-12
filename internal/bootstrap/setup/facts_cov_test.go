// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

type factsCovCtl struct {
	systemd    bool
	running    bool
	statusErr  error
	startErr   error
	startCalls int
}

func (c *factsCovCtl) IsSystemdMode() bool { return c.systemd }

func (c *factsCovCtl) ServiceStatus(_ context.Context, name string) (bool, error) {
	if name != "ldap" {
		return false, errors.New("unexpected service " + name)
	}

	return c.running, c.statusErr
}

func (c *factsCovCtl) ServiceStart(_ context.Context, name string) error {
	if name != "ldap" {
		return errors.New("unexpected service " + name)
	}

	c.startCalls++

	return c.startErr
}

type factsCovLDAP struct {
	entry     map[string]string
	err       error
	hostAsked string
	closed    bool
}

func (l *factsCovLDAP) GetServerConfig(host string) (map[string]string, error) {
	l.hostAsked = host

	return l.entry, l.err
}

func (l *factsCovLDAP) Close() error { l.closed = true; return nil }

// factsCovInstall swaps the seams for the test.
func factsCovInstall(t *testing.T, ctl *factsCovCtl, l *factsCovLDAP) {
	t.Helper()

	oldCtl, oldOpen := factsServicesOf, factsOpenLDAP

	factsServicesOf = func(*Env) factsServices { return ctl }
	factsOpenLDAP = func(*Env) (factsLDAPReader, error) {
		if l == nil {
			return nil, errors.New("connection refused")
		}

		return l, nil
	}

	t.Cleanup(func() { factsServicesOf, factsOpenLDAP = oldCtl, oldOpen })
}

func factsCovOSRelease(fakes *systest.Fakes, id string) {
	fakes.FS.Put("/etc/os-release", []byte("NAME=\"X\"\nID="+id+"\nVERSION_ID=\"1\"\n"), 0o644)
}

const factsCovDpkg = "carbonio-core install ok installed\n" +
	"carbonio-mta deinstall ok config-files\n" +
	"carbonio-proxy install ok installed\n" +
	"carbonio-appserver not-installed\n" +
	"carbonio-unknown install ok installed\n"

func TestDetectFacts_NewInstallDpkgEnablesEveryInstalledPackage(t *testing.T) {
	e, fakes, _ := testEnv(t)
	factsCovInstall(t, &factsCovCtl{systemd: true}, nil)
	factsCovOSRelease(fakes, "\"ubuntu\"")
	fakes.Exec.OnPath("dpkg-query", systest.Reply(factsCovDpkg))
	fakes.Exec.OnPath("rpm", systest.Fail(1, "rpm must not be used on ubuntu"))

	f, err := DetectFacts(e)
	require.NoError(t, err)

	assert.Equal(t, "ubuntu", f.Platform)
	assert.True(t, f.NewInstall, "no history: a new install")
	assert.True(t, f.Systemd)
	assert.Equal(t, map[string]bool{PkgCore: true, "carbonio-proxy": true}, f.Installed,
		"only 'install ok installed' counts, unknown packages are ignored")
	assert.Equal(t, f.Installed, f.Enabled)
	assert.False(t, f.ServiceWebapp, "no appserver")
	assert.Equal(t, []string{"proxy"}, f.EnabledRoles())
}

func TestDetectFacts_NewInstallRPMListsNamesAndKeepsAppserverWebapp(t *testing.T) {
	e, fakes, _ := testEnv(t)
	factsCovInstall(t, &factsCovCtl{}, nil)
	factsCovOSRelease(fakes, "rocky")
	fakes.Exec.OnPath("rpm", systest.Reply(
		"carbonio-core install ok installed\ncarbonio-appserver install ok installed\ncarbonio-mta install ok installed\n"))
	fakes.Exec.OnPath("dpkg-query", systest.Fail(1, "dpkg must not be used on rocky"))
	fakes.FS.Put(e.Paths.History, []byte("1: INSTALLED carbonio-core\n"), 0o644)

	f, err := DetectFacts(e)
	require.NoError(t, err)

	assert.True(t, f.NewInstall)
	assert.True(t, f.ServiceWebapp)
	assert.Equal(t, []string{"mta", "appserver"}, f.EnabledRoles())
	assert.False(t, f.Systemd)
}

func TestDetectFacts_PackageQueryFailureIsNotAnError(t *testing.T) {
	e, fakes, _ := testEnv(t)
	factsCovInstall(t, &factsCovCtl{}, nil)
	factsCovOSRelease(fakes, "ubuntu")
	fakes.Exec.OnPath("dpkg-query", func(*sys.Cmd) (sys.Result, error) {
		return sys.Result{ExitCode: 1}, nil
	})

	f, err := DetectFacts(e)
	require.NoError(t, err)
	assert.Empty(t, f.Installed, "dpkg exits 1 when nothing matches")
}

func TestDetectFacts_ExecFailureFails(t *testing.T) {
	e, fakes, _ := testEnv(t)
	factsCovInstall(t, &factsCovCtl{}, nil)
	fakes.Exec.FailStart(func(*sys.Cmd) bool { return true }, errors.New("exec format error"))

	_, err := DetectFacts(e)
	require.Error(t, err)
}

func factsCovExisting(t *testing.T, lc map[string]string, entry map[string]string, installed ...string) (*Env, *factsCovCtl, *factsCovLDAP, *systest.Fakes) {
	t.Helper()

	e := existEnv(t, lc)
	fakes := systest.New()
	e.Sys = fakes.System()
	factsCovOSRelease(fakes, "ubuntu")

	e.Paths.History = "/opt/zextras/.install_history"
	fakes.FS.Put(e.Paths.History, []byte("1: INSTALLED carbonio-core\n2: CONFIGURED END\n"), 0o644)

	out := ""
	for _, p := range installed {
		out += p + " install ok installed\n"
	}

	fakes.Exec.OnPath("dpkg-query", systest.Reply(out))

	ctl := &factsCovCtl{}
	l := &factsCovLDAP{entry: entry}
	factsCovInstall(t, ctl, l)

	return e, ctl, l, fakes
}

func TestDetectFacts_ExistingInstallMapsServerEntry(t *testing.T) {
	lc := map[string]string{"zimbra_server_hostname": "mail1.example.com", "ldap_url": "ldap://ldap1.example.com:389"}
	entry := map[string]string{
		"zimbraServiceInstalled": "mailbox\nmta\nproxy",
		"zimbraServiceEnabled":   "mailbox\nimapproxy\nservice\nantispam\nbogus\nmemcached",
	}
	e, ctl, l, _ := factsCovExisting(t, lc, entry, PkgCore, PkgAppserver, PkgMTA, "carbonio-proxy", "carbonio-clamav")

	f, err := DetectFacts(e)
	require.NoError(t, err)

	assert.False(t, f.NewInstall)
	assert.True(t, f.ServiceWebapp, "an existing install always has the service webapp")
	assert.Equal(t, "mail1.example.com", l.hostAsked)
	assert.True(t, l.closed)
	assert.Zero(t, ctl.startCalls, "ldap_url names another host: the local LDAP is left alone")

	assert.Equal(t, map[string]bool{PkgAppserver: true, PkgMTA: true, "carbonio-proxy": true}, f.PrevInstalled)
	assert.True(t, f.IsServiceEnabled("proxy"), "imapproxy is read as proxy")
	assert.False(t, f.IsServiceEnabled("imapproxy"))
	assert.True(t, f.IsServiceEnabled("antispam"))
	assert.False(t, f.IsServiceEnabled("bogus"))

	// enabled by LDAP and installed; carbonio-clamav is installed but new to
	// the server entry, so it is enabled too; carbonio-memcached is listed
	// but not installed, so it is dropped.
	assert.True(t, f.IsEnabled(PkgCore))
	assert.True(t, f.IsEnabled(PkgAppserver))
	assert.True(t, f.IsEnabled(PkgMTA))
	assert.True(t, f.IsEnabled("carbonio-proxy"))
	assert.True(t, f.IsEnabled("carbonio-clamav"), "newly installed package")
	assert.False(t, f.IsEnabled("carbonio-memcached"), "enabled in LDAP but not installed")
	assert.True(t, f.IsServiceEnabled("memcached"))

	assert.Equal(t, []string{"mta", "appserver", "proxy", "clamav"}, f.EnabledRoles())
}

func TestDetectFacts_ExistingInstallWarnsAboutUnknownServices(t *testing.T) {
	lc := map[string]string{"zimbra_server_hostname": "mail1.example.com", "ldap_url": "ldap://ldap1.example.com:389"}
	entry := map[string]string{"zimbraServiceInstalled": "mystery", "zimbraServiceEnabled": "other"}
	e, _, _, _ := factsCovExisting(t, lc, entry, PkgCore)

	var out bytes.Buffer

	e.Log = NewLog(&out, nil)

	_, err := DetectFacts(e)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "unknown package installed for mystery")
	assert.Contains(t, out.String(), "unknown package installed for other")
}

func TestDetectFacts_ExistingInstallStartsTheLocalLDAP(t *testing.T) {
	lc := map[string]string{"zimbra_server_hostname": "ldap1.example.com", "ldap_url": "ldap://ldap1.example.com:389"}
	e, ctl, _, _ := factsCovExisting(t, lc, map[string]string{}, PkgCore, "carbonio-directory-server")
	ctl.systemd = true

	_, err := DetectFacts(e)
	require.NoError(t, err)
	assert.Equal(t, 1, ctl.startCalls, "ldap_url names this host and slapd is down")
	assert.True(t, e.Flags.LDAPStarted)

	ctl.running, ctl.startCalls = true, 0
	e.Flags.LDAPStarted = false

	_, err = DetectFacts(e)
	require.NoError(t, err)
	assert.Zero(t, ctl.startCalls, "a running slapd is not started again")
	assert.False(t, e.Flags.LDAPStarted)
}

func TestDetectFacts_ExistingInstallErrors(t *testing.T) {
	lc := map[string]string{"zimbra_server_hostname": "ldap1.example.com", "ldap_url": "ldap://ldap1.example.com:389"}

	t.Run("ldap start fails", func(t *testing.T) {
		e, ctl, _, _ := factsCovExisting(t, lc, nil, PkgCore)
		ctl.systemd = true
		ctl.startErr = errors.New("slapd: bad config")

		_, err := DetectFacts(e)
		require.ErrorContains(t, err, "starting ldap")
		assert.False(t, e.Flags.LDAPStarted)
	})

	t.Run("ldap unreachable", func(t *testing.T) {
		e, _, _, _ := factsCovExisting(t, map[string]string{"zimbra_server_hostname": "m", "ldap_url": "ldap://x"}, nil, PkgCore)
		factsCovInstall(t, &factsCovCtl{}, nil)

		_, err := DetectFacts(e)
		require.ErrorContains(t, err, "connection refused")
	})

	t.Run("server entry unreadable", func(t *testing.T) {
		e, _, l, _ := factsCovExisting(t, map[string]string{"zimbra_server_hostname": "m", "ldap_url": "ldap://x"}, nil, PkgCore)
		l.err = errors.New("no such object")

		_, err := DetectFacts(e)
		require.ErrorContains(t, err, "reading server m")
		assert.True(t, l.closed, "the client is closed on error")
	})
}

func TestEnsureLDAPRunning_IgnoresStatusErrorAndStarts(t *testing.T) {
	e, _, _ := testEnv(t)
	ctl := &factsCovCtl{systemd: true, statusErr: errors.New("status failed"), running: true}
	factsCovInstall(t, ctl, nil)

	require.NoError(t, e.EnsureLDAPRunning())
	assert.Equal(t, 1, ctl.startCalls, "a failed status check is treated as not running")
	assert.True(t, e.Flags.LDAPStarted)
}

// Without the carbonio systemd units, slapd must be started as zextras:
// started by the root bootstrap it ran as root and zmcontrol could not stop it.
func TestEnsureLDAPRunning_WithoutSystemdStartsAsZextras(t *testing.T) {
	e, fakes, _ := testEnv(t)
	ctl := &factsCovCtl{}
	factsCovInstall(t, ctl, nil)

	require.NoError(t, e.EnsureLDAPRunning())
	assert.Zero(t, ctl.startCalls, "not started in-process as root")
	assert.True(t, e.Flags.LDAPStarted)

	calls := fakes.Exec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "runuser", calls[0].Path)
	assert.Equal(t, []string{"/opt/zextras/bin/ldap", "start"}, calls[0].Args[len(calls[0].Args)-2:])
}

func TestSplitValues(t *testing.T) {
	assert.Equal(t, []string{"a", "b c"}, splitValues("a\n\nb c\n"))
	assert.Empty(t, splitValues(""))
}

func TestOsReleaseID(t *testing.T) {
	e, fakes, _ := testEnv(t)
	assert.Empty(t, osReleaseID(e), "no os-release")

	fakes.FS.Put("/etc/os-release", []byte("NAME=Foo\nVERSION_ID=\"22.04\"\nID=\"ubuntu\"\nID_LIKE=debian\n"), 0o644)
	assert.Equal(t, "ubuntu", osReleaseID(e))

	fakes.FS.Put("/etc/os-release", []byte("NAME=Foo\n"), 0o644)
	assert.Empty(t, osReleaseID(e))
}
