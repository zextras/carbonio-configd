// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

const svcTestAdminPass = "S3cret-Admin-Pass"

type svcFakeDir struct {
	existing []string // substrings of filters that match
	global   map[string]string
	mtas     int
	// keys is zimbraSshPublicKey by server name ("" for a server without one).
	keys map[string]string
	// set records the SetServerAttribute calls as host -> attr -> value.
	set    map[string]map[string]string
	setErr error
}

func (d *svcFakeDir) Exists(filter string) (bool, error) {
	for _, s := range d.existing {
		if strings.Contains(filter, s) {
			return true, nil
		}
	}

	return false, nil
}

func (d *svcFakeDir) Global() (map[string]string, error) { return d.global, nil }
func (d *svcFakeDir) MTAServers() (int, error)           { return d.mtas, nil }
func (d *svcFakeDir) Close() error                       { return nil }

func (d *svcFakeDir) ServerAttribute(string) (map[string]string, error) { return d.keys, nil }

func (d *svcFakeDir) SetServerAttribute(host, attr, value string) error {
	if d.setErr != nil {
		return d.setErr
	}

	if d.set == nil {
		d.set = map[string]map[string]string{}
	}

	if d.set[host] == nil {
		d.set[host] = map[string]string{}
	}

	d.set[host][attr] = value

	return nil
}

func svcWithDir(t *testing.T, d *svcFakeDir) {
	t.Helper()

	old := svcOpenDir
	svcOpenDir = func(*Env) (svcDir, error) { return d, nil }

	t.Cleanup(func() { svcOpenDir = old })
}

func svcEnv(t *testing.T, packages ...string) (*Env, *systest.Fakes) {
	t.Helper()

	e, fakes, _ := testEnv(t)
	e.Cfg = &bootstrap.Config{}
	e.Facts.Enabled = map[string]bool{}

	for _, p := range packages {
		e.Facts.Enabled[p] = true
	}

	return e, fakes
}

// svcStdin returns the stdin of the call (the zmprov batch).
func svcStdin(t *testing.T, c *sys.Cmd) string {
	t.Helper()

	require.NotNil(t, c.Stdin)

	var b strings.Builder

	buf := make([]byte, 4096)

	for {
		n, err := c.Stdin.Read(buf)
		b.Write(buf[:n])

		if err != nil {
			break
		}
	}

	return b.String()
}

// zmprovCalls are the zmprov processes the test started.
func zmprovCalls(fakes *systest.Fakes) []*sys.Cmd {
	var calls []*sys.Cmd

	for _, c := range fakes.Exec.Calls() {
		if strings.Contains(c.String(), "zmprov") {
			calls = append(calls, c)
		}
	}

	return calls
}

func svcProvCall(t *testing.T, fakes *systest.Fakes) (*sys.Cmd, string) {
	t.Helper()

	for _, c := range fakes.Exec.Calls() {
		if strings.Contains(c.String(), "zmprov") {
			return c, svcStdin(t, c)
		}
	}

	require.Fail(t, "no zmprov call")

	return nil, ""
}

func TestSvcCheckPortConflicts_ReportsEnabledPackagesOnly(t *testing.T) {
	e, fakes, out := testEnv(t)
	e.Facts.Enabled = map[string]bool{PkgMTA: true}
	e.Facts.NewInstall = true

	fakes.FS.Put("/proc/net/tcp", []byte(
		"  sl  local_address rem_address   st\n"+
			"   0: 00000000:0019 00000000:0000 0A 0\n"+ // 25 listen, mta
			"   1: 00000000:0050 00000000:0000 0A 0\n"+ // 80 listen, appserver not enabled
			"   2: 0100007F:01BB 0100007F:9999 01 0\n"), 0o644) // 443 established
	fakes.FS.Put("/proc/net/tcp6", []byte("  sl\n   0: 00000000000000000000000000000000:2742 :0 0A 0\n"), 0o644) // 10050

	s := StepCheckPortConflicts()
	require.NoError(t, s.Apply(e))
	assert.Contains(t, out.String(), "Port conflict detected: 25 (carbonio-mta)")
	assert.NotContains(t, out.String(), "80")
	assert.NotContains(t, out.String(), "443")
}

func TestSvcCheckPortConflicts_LdapIgnoredOnExistingInstall(t *testing.T) {
	e, fakes, out := testEnv(t)
	e.Facts.Enabled = map[string]bool{svcPkgDirectory: true}
	fakes.FS.Put("/proc/net/tcp", []byte("sl\n 0: 00000000:0185 00000000:0000 0A 0\n"), 0o644) // 389

	require.NoError(t, StepCheckPortConflicts().Apply(e))
	assert.Empty(t, out.String())

	e.Facts.NewInstall = true
	require.NoError(t, StepCheckPortConflicts().Apply(e))
	assert.Contains(t, out.String(), "389")
}

func TestSvcCheckCoreEnabled(t *testing.T) {
	e, _ := svcEnv(t)
	s := StepCheckCoreEnabled()

	assert.True(t, s.When(e))
	require.ErrorIs(t, s.Apply(e), ErrCoreNotEnabled)

	e.Facts.Enabled[PkgCore] = true
	assert.False(t, s.When(e))
}

func TestSvcConfigInitMta_RunsToolWithoutSecretsAndSetsNetworks(t *testing.T) {
	e, fakes := svcEnv(t, PkgMTA)
	e.Paths.LocalConfig = t.TempDir() + "/localconfig.xml"
	fakes.FS.Put("/opt/zextras/libexec/zmmtainit", []byte("#!/bin/sh"), 0o755)
	e.Cfg.LDAP.Host = "ldap.example.com"
	e.Cfg.LDAP.Port = 636
	e.Cfg.LDAP.PostfixPassword = "PostfixSecret"
	e.Cfg.Host.Hostname = "mta.example.com"
	e.Cfg.MTA.MyNetworks = []string{"127.0.0.0/8", "10.0.0.0/24"}

	s := StepConfigInitMta()
	require.True(t, s.When(e))
	require.NoError(t, s.Apply(e))

	lines := strings.Join(fakes.Exec.Lines(), "\n")
	assert.Contains(t, lines, "/opt/zextras/libexec/zmmtainit ldap.example.com 636")
	assert.NotContains(t, lines, "PostfixSecret")

	assert.Equal(t, []string{"127.0.0.0/8 10.0.0.0/24"},
		provTestLDAP().values(provServerDN("mta.example.com"), "zimbraMtaMyNetworks"))

	v, err := e.LCValue("postfix_mail_owner")
	require.NoError(t, err)
	assert.Equal(t, "postfix", v)
}

func TestSvcConfigInitMta_SkippedWithoutMta(t *testing.T) {
	e, _ := svcEnv(t, PkgAppserver)
	assert.False(t, StepConfigInitMta().When(e))
}

func TestSvcSetTrainSACleanupHost(t *testing.T) {
	for _, tc := range []struct {
		mtas int
		want string
	}{{1, "true"}, {2, "false"}} {
		e, _ := svcEnv(t, PkgMTA)
		e.Facts.NewInstall = true
		e.Paths.LocalConfig = t.TempDir() + "/localconfig.xml"
		svcWithDir(t, &svcFakeDir{mtas: tc.mtas})

		s := StepSetTrainSACleanupHost()
		require.True(t, s.When(e))
		require.NoError(t, s.Apply(e))

		v, err := e.LCValue("zmtrainsa_cleanup_host")
		require.NoError(t, err)
		assert.Equal(t, tc.want, v)
	}
}

func svcDomainCfg(e *Env) {
	e.Cfg.Domain.Create = true
	e.Cfg.Domain.Name = "example.com"
	e.Cfg.Admin.Create = true
	e.Cfg.Admin.Account = "Admin@Example.com"
	e.Cfg.Admin.Password = svcTestAdminPass
	e.Cfg.Store.SpamTraining = true
	e.Cfg.Store.SpamAccount = "Spam.x@example.com"
	e.Cfg.Store.HamAccount = "ham.x@example.com"
	e.Cfg.Store.VirusQuarantineAccount = "virus-quarantine.x@example.com"
	e.Facts.ServiceWebapp = true
}

func TestSvcConfigCreateDomain_CreatesDomainAccountsAndAliasesOverLDAP(t *testing.T) {
	e, fakes := svcEnv(t, svcPkgDirectory, PkgAppserver)
	svcDomainCfg(e)
	svcWithDir(t, &svcFakeDir{global: map[string]string{}})
	provTestLDAP().set(provServerDN("mail.example.com"), "zimbraServiceEnabled", "service")

	s := StepConfigCreateDomain()
	done, err := s.Done(e)
	require.NoError(t, err)
	require.False(t, done)
	require.NoError(t, s.Apply(e))

	assert.Empty(t, zmprovCalls(fakes), "no zmprov process")
	assert.NotContains(t, strings.Join(fakes.Exec.Lines(), "\n"), svcTestAdminPass)

	ldap := provTestLDAP()
	assert.Equal(t, []string{"example.com"}, ldap.values("dc=example,dc=com", "zimbraDomainName"), "the domain is created once")
	assert.Equal(t, []string{"example.com"}, ldap.values(provGlobalDN, "zimbraDefaultDomainName"))
	assert.Equal(t, []string{"example.com"}, ldap.values(provGlobalDN, "zimbraMtaMyOrigin"))

	adminDN := "uid=admin,ou=people,dc=example,dc=com"
	assert.Equal(t, []string{"cartBlancheUI"}, ldap.values(adminDN, "zimbraAdminConsoleUIComponents"))
	assert.Equal(t, []string{"TRUE"}, ldap.values(adminDN, "zimbraIsAdminAccount"))
	assert.Equal(t, svcTestAdminPass, ldap.Passwords[adminDN], "the password goes to the server in the password modify operation")
	assert.Empty(t, ldap.values(adminDN, "userPassword"))
	assert.Equal(t, []string{"root@example.com", "postmaster@example.com"}, ldap.values(adminDN, "zimbraMailAlias"))
	assert.True(t, ldap.Exists("uid=root,ou=people,dc=example,dc=com"))
	assert.True(t, ldap.Exists("uid=postmaster,ou=people,dc=example,dc=com"))
	assert.Equal(t, []string{"admin@example.com"}, ldap.values(provGlobalDN, "carbonioNotificationFrom"))
	assert.Equal(t, []string{"admin@example.com"}, ldap.values(provGlobalDN, "carbonioNotificationRecipients"))
	assert.Equal(t, []string{"30d"}, ldap.values("uid=virus-quarantine.x,ou=people,dc=example,dc=com", "zimbraMailMessageLifetime"))
	assert.Equal(t, []string{"spam.x@example.com"}, ldap.values(provGlobalDN, "zimbraSpamIsSpamAccount"))
	assert.Equal(t, []string{"ham.x@example.com"}, ldap.values(provGlobalDN, "zimbraSpamIsNotSpamAccount"))
	assert.Equal(t, []string{"virus-quarantine.x@example.com"}, ldap.values(provGlobalDN, "zimbraAmavisQuarantineAccount"))
}

func TestSvcConfigCreateDomain_DoneWhenEverythingExists(t *testing.T) {
	e, fakes := svcEnv(t, svcPkgDirectory, PkgAppserver)
	svcDomainCfg(e)
	svcWithDir(t, &svcFakeDir{
		existing: []string{
			"zimbraDomainName=example.com", "zimbraMailDeliveryAddress=admin@example.com",
			"zimbraMailAlias=root@example.com", "zimbraMailAlias=postmaster@example.com",
			"=spam.x@example.com", "=ham.x@example.com", "=virus-quarantine.x@example.com",
		},
		global: map[string]string{
			"zimbraDefaultDomainName": "example.com", "zimbraMtaMyOrigin": "example.com",
			"carbonioNotificationFrom": "admin@example.com", "carbonioNotificationRecipients": "admin@example.com",
			"zimbraSpamIsSpamAccount": "spam.x@example.com", "zimbraSpamIsNotSpamAccount": "ham.x@example.com",
			"zimbraAmavisQuarantineAccount": "virus-quarantine.x@example.com",
		},
	})

	done, err := StepConfigCreateDomain().Done(e)
	require.NoError(t, err)
	assert.True(t, done)
	assert.Empty(t, fakes.Exec.Calls())
}

func TestSvcConfigCreateDomain_ExistingAccountsAreNotRecreated(t *testing.T) {
	e, fakes := svcEnv(t, PkgAppserver)
	svcDomainCfg(e)
	e.Facts.ServiceWebapp = false
	svcWithDir(t, &svcFakeDir{
		existing: []string{"zimbraDomainName=example.com", "zimbraMailDeliveryAddress=admin@example.com"},
		global:   map[string]string{},
	})
	provTestLDAP().set("dc=example,dc=com", "objectClass", "zimbraDomain")
	provTestLDAP().set("dc=example,dc=com", "zimbraDomainName", "example.com")
	provTestLDAP().set(provServerDN("mail.example.com"), "zimbraServiceEnabled", "service")

	require.NoError(t, StepConfigCreateDomain().Apply(e))

	assert.Empty(t, zmprovCalls(fakes), "no zmprov process")
	assert.Empty(t, provTestLDAP().values("dc=example,dc=com", "zimbraId"), "the domain exists already: it is not created again")
	assert.False(t, provTestLDAP().Exists("uid=admin,ou=people,dc=example,dc=com"))
	assert.Empty(t, provTestLDAP().values("uid=admin,ou=people,dc=example,dc=com", "zimbraMailAlias"), "no root/postmaster on a web-only node")
	assert.Empty(t, provTestLDAP().values(provGlobalDN, "zimbraDefaultDomainName"), "the domain exists already")
	assert.True(t, provTestLDAP().Exists("uid=ham.x,ou=people,dc=example,dc=com"))
}

func TestSvcConfigCreateDomain_NotApplicableWithoutDirectoryOrStore(t *testing.T) {
	e, _ := svcEnv(t, PkgMTA)
	assert.False(t, StepConfigCreateDomain().When(e))
}

func TestSvcConfigInitSQL(t *testing.T) {
	e, fakes := svcEnv(t, PkgAppserver)
	e.Cfg.Store.MySQLMemoryPercent = 40
	s := StepConfigInitSQL()

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, s.Apply(e))
	assert.Equal(t, []string{"/opt/zextras/libexec/zmmyinit --mysql_memory_percent 40"}, fakes.Exec.Lines())

	fakes.FS.PutDir("/opt/zextras/db/data/zimbra", 0o755)

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestSvcConfigInitSQL_DefaultPercent(t *testing.T) {
	e, fakes := svcEnv(t, PkgAppserver)
	require.NoError(t, StepConfigInitSQL().Apply(e))
	assert.Contains(t, fakes.Exec.Lines()[0], "--mysql_memory_percent 30")
}

func TestSvcStartServers_Systemd(t *testing.T) {
	e, fakes := svcEnv(t, PkgAppserver, PkgMTA)
	e.Facts.Systemd = true
	e.Cfg.StartServices = true

	fakes.Exec.When(func(c *sys.Cmd) bool { return c.Path == "systemctl" && len(c.Args) > 1 && c.Args[0] == "is-enabled" },
		func(c *sys.Cmd) (sys.Result, error) {
			out := "disabled\n"
			if c.Args[1] == "carbonio-appserver.target" || c.Args[1] == "carbonio-mta.target" {
				out = "enabled\n"
			}

			return sys.Result{Stdout: []byte(out), ExitCode: 0}, nil
		})
	// no stray configd: pkill -0 finds nothing
	fakes.Exec.OnPath("pkill", systest.Fail(1, ""))

	require.NoError(t, svcStartServers(e))

	var actions []string

	for _, l := range fakes.Exec.Lines() {
		if strings.HasPrefix(l, "systemctl ") && !strings.Contains(l, "is-enabled") {
			actions = append(actions, l)
		}
	}

	assert.Equal(t, []string{
		"systemctl stop carbonio-appserver.target", "systemctl stop carbonio-mta.target",
		"systemctl stop carbonio-configd.service",
		"systemctl start carbonio-appserver.target", "systemctl start carbonio-mta.target",
	}, actions)
}

func TestSvcStartServers_StrayConfigdTerminated(t *testing.T) {
	e, fakes := svcEnv(t)
	e.Facts.Systemd = true

	probes := 0

	fakes.Exec.When(func(c *sys.Cmd) bool { return c.Path == "pkill" && c.Args[0] == "-0" },
		func(*sys.Cmd) (sys.Result, error) {
			probes++
			if probes == 1 {
				return sys.Result{}, nil // running
			}

			return sys.Result{ExitCode: 1}, nil // gone after TERM
		})

	require.NoError(t, svcStopStrayConfigd(e))

	var sigs []string

	for _, l := range fakes.Exec.Lines() {
		if strings.HasPrefix(l, "pkill ") {
			sigs = append(sigs, l)
		}
	}

	assert.Equal(t, []string{
		"pkill -0 -u zextras -f ^/opt/zextras/bin/configd$",
		"pkill -TERM -u zextras -f ^/opt/zextras/bin/configd$",
		"pkill -0 -u zextras -f ^/opt/zextras/bin/configd$",
	}, sigs)
}

func TestSvcStartServers_ZmcontrolPathAndRedoLog(t *testing.T) {
	e, fakes := svcEnv(t, PkgAppserver)
	e.Facts.Platform = "ubuntu"
	e.Cfg.StartServices = true

	require.NoError(t, svcStartServers(e))

	lines := fakes.Exec.Lines()
	require.Len(t, lines, 4)
	assert.Equal(t, "chown zextras:zextras /opt/zextras/redolog/redo.log", lines[0])

	for i, verb := range []string{"stop", "start", "status"} {
		assert.Contains(t, lines[i+1], "/opt/zextras/bin/zmcontrol "+verb)
		assert.Contains(t, lines[i+1], "runuser -u zextras")
	}

	// new install: no chown
	e2, f2 := svcEnv(t, PkgAppserver)
	e2.Facts.Platform = "ubuntu"
	e2.Facts.NewInstall = true
	require.NoError(t, svcStartServers(e2))
	assert.Len(t, f2.Exec.Lines(), 3)
}

func TestSvcStartServers_StartFailureIsReported(t *testing.T) {
	e, fakes := svcEnv(t)
	fakes.Exec.When(func(c *sys.Cmd) bool { return strings.Contains(c.String(), "zmcontrol start") },
		systest.Fail(1, "boom"))

	require.Error(t, svcStartServers(e))
	assert.False(t, StepStartServers().Optional, "a service that does not start fails the run")
	assert.False(t, StepStartServers().When(e))
}

func TestSvcGALSync_Conditions(t *testing.T) {
	e, _ := svcEnv(t, PkgAppserver)
	e.Cfg.StartServices = true
	e.Facts.NewInstall = true
	e.Facts.ServiceWebapp = true

	assert.True(t, StepConfigCreateDefaultDomainGALSyncAcct().When(e))
	assert.False(t, StepGALSyncSkipNotice().When(e))

	e.Facts.NewInstall = false
	assert.False(t, StepConfigCreateDefaultDomainGALSyncAcct().When(e))
	assert.False(t, StepGALSyncSkipNotice().When(e), "an existing install is not worth a warning")

	e.Facts.NewInstall = true
	e.Cfg.StartServices = false
	assert.False(t, StepConfigCreateDefaultDomainGALSyncAcct().When(e))
	assert.True(t, StepGALSyncSkipNotice().When(e))
}

// svcFakeGAL is the admin API of the GAL sync step.
type svcFakeGAL struct {
	svcGALAdmin // the calls the GAL sync step does not make

	got  []zxadmin.GalSyncAccountRequest
	acct zxadmin.GalSyncAccount
	err  error
}

func (g *svcFakeGAL) CreateGalSyncAccount(
	_ context.Context, r *zxadmin.GalSyncAccountRequest,
) (zxadmin.GalSyncAccount, error) {
	g.got = append(g.got, *r)

	return g.acct, g.err
}

func svcWithGAL(t *testing.T, g *svcFakeGAL) {
	t.Helper()

	old := svcOpenGAL
	svcOpenGAL = func(*Env) (svcGALAdmin, error) { return g, nil }

	t.Cleanup(func() { svcOpenGAL = old })
}

func TestSvcGALSync_CreatesTheAccountThroughTheAdminAPI(t *testing.T) {
	e, fakes := svcEnv(t, PkgAppserver)
	e.Paths.LocalConfig = t.TempDir() + "/localconfig.xml"
	e.Cfg.Domain.Name = "example.com"
	e.Cfg.Host.Hostname = "other.example.com"

	var logFile bytes.Buffer

	e.Log = NewLog(&bytes.Buffer{}, &logFile)

	require.NoError(t, e.SetLC(map[string]string{"zimbra_server_hostname": "store.example.com"}))
	svcWithDir(t, &svcFakeDir{})

	gal := &svcFakeGAL{acct: zxadmin.GalSyncAccount{Name: "galsync.x@example.com", ID: "id-123"}}
	svcWithGAL(t, gal)

	s := StepConfigCreateDefaultDomainGALSyncAcct()
	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, s.Apply(e))

	require.Len(t, gal.got, 1)

	r := gal.got[0]
	assert.Regexp(t, `^galsync\.[a-z0-9]{10}@example\.com$`, r.Account)
	assert.Equal(t, "InternalGAL", r.Name)
	assert.Equal(t, "example.com", r.Domain)
	assert.Equal(t, "zimbra", r.Type)
	assert.Equal(t, "_InternalGAL", r.Folder)
	assert.Equal(t, "store.example.com", r.Server, "the mailbox is the localconfig server, not the configured host")
	assert.Equal(t, "1d", r.PollingInterval)

	assert.Empty(t, fakes.Exec.Lines(), "no zmgsautil, no JVM")
	assert.Contains(t, logFile.String(), "Created GAL sync account galsync.x@example.com (id-123)")

	svcWithDir(t, &svcFakeDir{existing: []string{"galsync.*@example.com"}})

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestSvcGALSync_FaultFailsTheStepWithItsReason(t *testing.T) {
	e, _ := svcEnv(t, PkgAppserver)
	e.Paths.LocalConfig = t.TempDir() + "/localconfig.xml"
	e.Cfg.Domain.Name = "example.com"

	svcWithGAL(t, &svcFakeGAL{err: errors.New("fault account.NO_SUCH_DOMAIN: no such domain")})

	err := StepConfigCreateDefaultDomainGALSyncAcct().Apply(e)
	require.ErrorContains(t, err, "creating the GAL sync account")
	assert.ErrorContains(t, err, "account.NO_SUCH_DOMAIN: no such domain")
}

func TestSvcGALSync_AdminAPIUnavailable(t *testing.T) {
	e, _ := svcEnv(t, PkgAppserver)
	e.Paths.LocalConfig = t.TempDir() + "/localconfig.xml"

	old := svcOpenGAL
	svcOpenGAL = func(*Env) (svcGALAdmin, error) { return nil, errors.New("no localconfig") }

	t.Cleanup(func() { svcOpenGAL = old })

	require.ErrorContains(t, StepConfigCreateDefaultDomainGALSyncAcct().Apply(e), "no localconfig")
}

// The setup.pl command has no bootstrap configuration: domain and server are
// the caller's, and Cfg is never read.
func TestCreateGalSyncAccount_NeedsNoConfiguration(t *testing.T) {
	e, _ := svcEnv(t, PkgAppserver)
	e.Cfg = nil

	gal := &svcFakeGAL{acct: zxadmin.GalSyncAccount{Name: "galsync.q@d.example", ID: "id-1"}}
	svcWithGAL(t, gal)

	acct, err := CreateGalSyncAccount(e, "d.example", "mbox.d.example")
	require.NoError(t, err)
	assert.Equal(t, gal.acct, acct)
	require.Len(t, gal.got, 1)
	assert.Regexp(t, `^galsync\.[a-z0-9]{10}@d\.example$`, gal.got[0].Account)
	assert.Equal(t, "d.example", gal.got[0].Domain)
	assert.Equal(t, "mbox.d.example", gal.got[0].Server)
}

func TestSvcAdminURL(t *testing.T) {
	assert.Equal(t, "https://localhost:7071", svcAdminURL(map[string]string{}))
	assert.Equal(t, "https://mbox.example.com:9071", svcAdminURL(map[string]string{
		"zimbra_zmprov_default_soap_server": "mbox.example.com", "zimbra_admin_service_port": "9071",
	}))
	assert.Equal(t, "https://[::1]:7071", svcAdminURL(map[string]string{"zimbra_zmprov_default_soap_server": "::1"}))
}

const svcTestCronBase = "# ZEXTRAS-START\n0 1 * * * base\n"

func svcCronEnv(t *testing.T, current string, packages ...string) (*Env, *systest.Fakes) {
	t.Helper()

	e, fakes := svcEnv(t, packages...)
	fakes.FS.Put("/opt/zextras/conf/crontabs/crontab", []byte(svcTestCronBase), 0o644)
	fakes.FS.Put("/opt/zextras/conf/crontabs/crontab.ldap", []byte("0 2 * * * ldap\n"), 0o644)
	fakes.FS.Put("/opt/zextras/conf/crontabs/crontab.store", []byte("0 3 * * * store\n"), 0o644)
	fakes.FS.Put("/opt/zextras/conf/crontabs/crontab.mta", []byte("0 4 * * * mta"), 0o644) // no trailing newline
	fakes.Exec.When(func(c *sys.Cmd) bool { return c.Path == "crontab" && c.Args[len(c.Args)-1] == "-l" },
		systest.Reply(current))

	return e, fakes
}

func TestSvcCrontab_ReplacesBlockKeepsUserEntries(t *testing.T) {
	current := "MAILTO=x\n# ZEXTRAS-START\nold line\n# ZEXTRAS-END -- DO NOT EDIT\n5 5 * * * mine\n"
	e, fakes := svcCronEnv(t, current, svcPkgDirectory, PkgMTA)

	s := StepSetupCrontab()
	done, err := s.Done(e)
	require.NoError(t, err)
	require.False(t, done)
	require.NoError(t, s.Apply(e))

	var install *sys.Cmd

	for _, c := range fakes.Exec.Calls() {
		if c.Path == "crontab" && c.Args[len(c.Args)-1] == "-" {
			install = c
		}
	}

	require.NotNil(t, install)
	assert.Equal(t, []string{"-u", "zextras", "-"}, install.Args)
	assert.Equal(t, svcTestCronBase+"0 2 * * * ldap\n0 4 * * * mta\n"+svcCrontabEndLine+"\nMAILTO=x\n5 5 * * * mine\n",
		svcStdin(t, install))
}

func TestSvcCrontab_DoneWhenUnchanged(t *testing.T) {
	want := svcTestCronBase + "0 3 * * * store\n" + svcCrontabEndLine + "\n5 5 * * * mine\n"
	e, _ := svcCronEnv(t, want, PkgAppserver)

	done, err := StepSetupCrontab().Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestSvcCrontab_WithoutMarkersStartsFresh(t *testing.T) {
	e, fakes := svcCronEnv(t, "5 5 * * * mine\n")
	require.NoError(t, StepSetupCrontab().Apply(e))

	for _, c := range fakes.Exec.Calls() {
		if c.Path == "crontab" && c.Args[len(c.Args)-1] == "-" {
			assert.Equal(t, svcTestCronBase+svcCrontabEndLine+"\n", svcStdin(t, c))
		}
	}
}

func TestSvcCrontab_MissingBaseFileFails(t *testing.T) {
	e, _ := svcEnv(t)
	require.Error(t, StepSetupCrontab().Apply(e))
}

func TestSvcRandomPassword(t *testing.T) {
	a, err := svcRandomPassword(16, svcAlnum)
	require.NoError(t, err)

	b, err := svcRandomPassword(16, svcAlnum)
	require.NoError(t, err)

	assert.Len(t, a, 16)
	assert.NotEqual(t, a, b)
}
