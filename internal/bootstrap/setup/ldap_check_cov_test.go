// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/services"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	lwMasterHost  = "master.example.com"
	lwAdminDN     = "uid=zimbra,cn=admins,cn=zimbra"
	lwReplicaDN   = "uid=zmreplica,cn=admins,cn=zimbra"
	lwAmavisDN    = "uid=zmamavis,cn=appaccts,cn=zimbra"
	lwPostfixDN   = "uid=zmpostfix,cn=appaccts,cn=zimbra"
	lwMasterURL   = "ldap://master.example.com:389"
	lwAccesslogDN = "cn=accesslog"
)

// lwLogged gives e a log whose lines the test can read.
func lwLogged(e *Env) *bytes.Buffer {
	buf := &bytes.Buffer{}
	e.Log = NewLog(nil, buf)

	return buf
}

// lwReplicaEnv is a directory-server replica of lwMasterHost.
func lwReplicaEnv(t *testing.T) (*Env, *systest.Fakes, *ldapFake) {
	t.Helper()

	e, fakes, f := ldapTestEnv(t)
	e.Cfg.LDAP.Host = lwMasterHost
	require.NoError(t, e.SetLC(map[string]string{"zimbra_ldap_userdn": lwAdminDN}))

	return e, fakes, f
}

// lwKitNoStartTLS is the fake kit whose StartTLS dials fail.
func lwKitNoStartTLS(f *ldapFake) *ldapKit {
	k := f.kit()
	dial := k.dial

	k.dial = func(e *Env, d ldapDial) (ldapConn, error) {
		if d.TLS == ldapTLSInsecure {
			f.dials = append(f.dials, d)

			return nil, errors.New("StartTLS refused")
		}

		return dial(e, d)
	}

	return k
}

func TestLdapIsAvailable_IncompleteConfigurationIsNotChecked(t *testing.T) {
	for name, mut := range map[string]func(*Env){
		"no admin password": func(e *Env) { e.Cfg.LDAP.AdminPassword = "" },
		"no port":           func(e *Env) { e.Cfg.LDAP.Port = 0 },
		"no host":           func(e *Env) { e.Cfg.LDAP.Host = "" },
	} {
		t.Run(name, func(t *testing.T) {
			e, _, f := lwReplicaEnv(t)
			logs := lwLogged(e)
			mut(e)

			assert.False(t, ldapIsAvailable(e, f.kit()))
			assert.Empty(t, f.dials, "no connection is opened for a configuration that cannot work")
			assert.Contains(t, logs.String(), "ldap configuration not complete")
		})
	}
}

func TestLdapIsAvailable_UnreadableLocalConfigIsNotAvailable(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	logs := lwLogged(e)
	require.NoError(t, os.WriteFile(e.Paths.LocalConfig, []byte("<localconfig><key"), 0o600))
	e.lc = nil

	assert.False(t, ldapIsAvailable(e, f.kit()))
	assert.Empty(t, f.dials)
	assert.NotEmpty(t, logs.String())
}

func TestLdapIsAvailable_ConfiguredMasterChecksItselfWithoutReplication(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	require.NoError(t, e.SetLC(map[string]string{"zimbra_ldap_userdn": lwAdminDN}))
	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)

	assert.True(t, ldapIsAvailable(e, f.kit()))
	assert.Equal(t, []string{"bind " + lwAdminDN}, f.events,
		"the master has no replication account and no accesslog to query")
}

func TestLdapIsAvailable_AFailedAdminBindIsNotRememberedAndTheRestIsStillChecked(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	e.Facts.Installed[PkgMTA] = true
	f.bindOK = func(dn, _ string) bool { return dn != lwAdminDN }

	assert.False(t, ldapIsAvailable(e, f.kit()))

	lc, err := e.LC()
	require.NoError(t, err)
	assert.NotEqual(t, ldapTestAdmin, lc[ldapLCAdmin], "a password that does not bind must not be stored")
	assert.Equal(t, []string{
		"bind " + lwAdminDN,
		"bind " + lwPostfixDN, "bind " + lwAmavisDN,
		"bind " + lwReplicaDN,
		"bind " + lwAdminDN,
	}, f.events, "every account is tried, then the replication check binds as admin")
}

func TestLdapIsAvailable_AnAccountWithoutPasswordFailsWithoutBinding(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	logs := lwLogged(e)
	e.Facts.Installed[PkgMTA] = true
	e.Cfg.LDAP.AmavisPassword = ""

	assert.False(t, ldapIsAvailable(e, f.kit()))
	assert.Contains(t, logs.String(), "amavis configuration not complete")
	assert.NotContains(t, f.events, "bind "+lwAmavisDN, "an empty password would be an anonymous bind")
	assert.Contains(t, f.events, "bind "+lwPostfixDN)
}

func TestLdapIsAvailable_ReplicaChecksItsReplicationAccountAndTheAccesslog(t *testing.T) {
	e, _, f := lwReplicaEnv(t)

	assert.True(t, ldapIsAvailable(e, f.kit()))
	assert.Equal(t, []string{
		"bind " + lwAdminDN,
		"bind " + lwReplicaDN,
		"bind " + lwAdminDN,
	}, f.events)
}

func TestLdapAvailabilityChecks_DNsFollowTheInstalledPackagesAndTheBase(t *testing.T) {
	e, _, _ := ldapTestEnv(t)
	assert.Empty(t, ldapAvailabilityChecks(e, "cn=zimbra"), "a directory master checks nothing else")

	e.Facts.Installed[ldapPkgProxy] = true
	e.Facts.Installed[PkgMTA] = true
	e.Cfg.LDAP.Host = lwMasterHost

	got := ldapAvailabilityChecks(e, "dc=corp")
	require.Len(t, got, 4)
	assert.Equal(t, ldapAvailabilityCheck{"nginx", "uid=zmnginx,cn=appaccts,dc=corp", ldapTestNginx}, got[0])
	assert.Equal(t, ldapAvailabilityCheck{"postfix", "uid=zmpostfix,cn=appaccts,dc=corp", ldapTestPost}, got[1])
	assert.Equal(t, ldapAvailabilityCheck{"amavis", "uid=zmamavis,cn=appaccts,dc=corp", ldapTestAmavis}, got[2])
	assert.Equal(t, ldapAvailabilityCheck{"replication", "uid=zmreplica,cn=admins,dc=corp", ldapTestRep}, got[3])
}

func TestLdapCountFailedChecks_UsesTheConfiguredBaseDN(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	e.Facts.Installed[ldapPkgProxy] = true
	e.Cfg.LDAP.BaseDN = "dc=corp"
	f.bindOK = func(dn, _ string) bool { return dn != "uid=zmnginx,cn=appaccts,dc=corp" }

	assert.Equal(t, 1, ldapCountFailedChecks(e, f.kit()))
	assert.Equal(t, []string{"bind uid=zmnginx,cn=appaccts,dc=corp", "bind uid=zmreplica,cn=admins,dc=corp"}, f.events)
}

func TestLdapProbe_Ldaps636NeverAsksForStartTLS(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	e.Cfg.LDAP.Port = 636
	e.Cfg.Host.RequireInterprocessSecurity = true

	conn, startTLS, url, err := ldapProbe(e, f.kit())
	require.NoError(t, err)
	assert.NotNil(t, conn)
	assert.False(t, startTLS)
	assert.Equal(t, "ldaps://master.example.com:636", url)
	assert.Equal(t, []ldapDial{{URLs: []string{url}}}, f.dials)
	assert.True(t, e.Cfg.Host.RequireInterprocessSecurity, "ldaps keeps the requirement")
}

func TestLdapProbe_WithoutTheRequirementTheConnectionIsPlain(t *testing.T) {
	e, _, f := lwReplicaEnv(t)

	_, startTLS, url, err := ldapProbe(e, f.kit())
	require.NoError(t, err)
	assert.False(t, startTLS)
	assert.Equal(t, lwMasterURL, url)
	assert.Equal(t, []ldapDial{{URLs: []string{lwMasterURL}}}, f.dials)
}

func TestLdapProbe_RequirementUpgradesWithStartTLS(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	e.Cfg.Host.RequireInterprocessSecurity = true

	_, startTLS, _, err := ldapProbe(e, f.kit())
	require.NoError(t, err)
	assert.True(t, startTLS)
	assert.Equal(t, []ldapDial{{URLs: []string{lwMasterURL}, TLS: ldapTLSInsecure}}, f.dials)
	assert.True(t, e.Cfg.Host.RequireInterprocessSecurity)
}

func TestLdapProbe_MasterWithoutStartTLSDropsTheRequirement(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	logs := lwLogged(e)
	e.Cfg.Host.RequireInterprocessSecurity = true

	conn, startTLS, _, err := ldapProbe(e, lwKitNoStartTLS(f))
	require.NoError(t, err)
	assert.NotNil(t, conn)
	assert.False(t, startTLS)
	assert.False(t, e.Cfg.Host.RequireInterprocessSecurity, "setup.pl turns the requirement off")
	assert.Equal(t, []ldapDial{
		{URLs: []string{lwMasterURL}, TLS: ldapTLSInsecure},
		{URLs: []string{lwMasterURL}},
	}, f.dials)
	assert.Contains(t, logs.String(), "Unable to startTLS: StartTLS refused")
	assert.Contains(t, logs.String(), "Disabling the requirement for interprocess security.")
}

func TestLdapProbe_UnreachableMasterKeepsTheRequirement(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	e.Cfg.Host.RequireInterprocessSecurity = true

	k := f.kit()
	k.dial = func(_ *Env, d ldapDial) (ldapConn, error) {
		f.dials = append(f.dials, d)

		return nil, errors.New("connection refused")
	}

	conn, startTLS, url, err := ldapProbe(e, k)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.False(t, startTLS)
	assert.Equal(t, lwMasterURL, url)
	assert.Len(t, f.dials, 2, "StartTLS first, then clear text")
	assert.True(t, e.Cfg.Host.RequireInterprocessSecurity, "the requirement is only dropped once the plain connection works")
}

func TestLdapCheckBind_NewInstallRecordsHowTheMasterIsReached(t *testing.T) {
	cases := map[string]struct {
		require  bool
		noTLS    bool
		wantTLS  string
		wantSec  string
		wantDial int
	}{
		"starttls":                 {require: true, wantTLS: "1", wantSec: "1", wantDial: 1},
		"no requirement":           {wantTLS: "0", wantSec: "0", wantDial: 1},
		"starttls refused by peer": {require: true, noTLS: true, wantTLS: "0", wantSec: "0", wantDial: 2},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e, _, f := lwReplicaEnv(t)
			e.Cfg.Host.RequireInterprocessSecurity = c.require

			k := f.kit()
			if c.noTLS {
				k = lwKitNoStartTLS(f)
			}

			require.True(t, ldapCheckBind(e, k, lwAdminDN, ldapTestAdmin))
			assert.Len(t, f.dials, c.wantDial)

			lc, err := e.LC()
			require.NoError(t, err)
			assert.Equal(t, lwMasterURL, lc[ldapLCURL])
			assert.Equal(t, c.wantTLS, lc["ldap_starttls_supported"])
			assert.Equal(t, c.wantSec, lc["zimbra_require_interprocess_security"])
			assert.Equal(t, "true", lc["ssl_allow_untrusted_certs"])
		})
	}
}

func TestLdapCheckBind_ExistingInstallLeavesLocalConfigAlone(t *testing.T) {
	e, _, f := lwReplicaEnv(t)
	e.Facts.NewInstall = false

	require.True(t, ldapCheckBind(e, f.kit(), lwAdminDN, ldapTestAdmin))

	lc, err := e.LC()
	require.NoError(t, err)
	assert.NotEqual(t, lwMasterURL, lc[ldapLCURL])
}

func TestLdapCheckBind_Failures(t *testing.T) {
	t.Run("unreachable", func(t *testing.T) {
		e, _, f := lwReplicaEnv(t)
		logs := lwLogged(e)

		k := f.kit()
		k.dial = func(*Env, ldapDial) (ldapConn, error) { return nil, errors.New("connection refused") }

		assert.False(t, ldapCheckBind(e, k, lwAdminDN, ldapTestAdmin))
		assert.Contains(t, logs.String(), "Unable to contact ldap at "+lwMasterURL+": connection refused")
	})

	t.Run("bad credentials", func(t *testing.T) {
		e, _, f := lwReplicaEnv(t)
		logs := lwLogged(e)
		f.bindOK = func(string, string) bool { return false }

		assert.False(t, ldapCheckBind(e, f.kit(), lwAdminDN, "wrong"))
		assert.Contains(t, logs.String(), "Unable to bind to "+lwMasterURL+" with user "+lwAdminDN)
		assert.NotContains(t, logs.String(), "wrong", "the password is never logged")

		lc, err := e.LC()
		require.NoError(t, err)
		assert.NotEqual(t, lwMasterURL, lc[ldapLCURL], "nothing is recorded for a master that did not authenticate")
	})
}

// lwSearchConn is an ldapConn whose search fails.
type lwSearchConn struct {
	ldapFakeConn
	err error
}

func (c lwSearchConn) Search(string, string, bool, []string) ([]ldapEntry, error) { return nil, c.err }

func TestLdapCheckReplication(t *testing.T) {
	cases := map[string]struct {
		replType string
		hint     string
	}{
		"replica": {replType: "Replica", hint: "Please run zmldapenablereplica on the master."},
		"mmr":     {replType: "MMR", hint: "Please run zmldapenable-mmr on the master."},
		"other":   {replType: "master"},
	}

	for name, c := range cases {
		t.Run("accesslog missing, "+name, func(t *testing.T) {
			e, _, f := lwReplicaEnv(t)
			logs := lwLogged(e)
			e.Cfg.LDAP.ReplicationType = c.replType

			k := f.kit()
			k.dial = func(_ *Env, d ldapDial) (ldapConn, error) {
				f.dials = append(f.dials, d)

				return lwSearchConn{ldapFakeConn{f}, errors.New("no such object")}, nil
			}

			assert.False(t, ldapCheckReplication(e, k, lwAdminDN, ldapTestAdmin))
			assert.Contains(t, logs.String(), "Unable to find accesslog database on master.")

			if c.hint == "" {
				assert.NotContains(t, logs.String(), "Please run")
			} else {
				assert.Contains(t, logs.String(), c.hint)
			}
		})
	}

	t.Run("accesslog answers", func(t *testing.T) {
		e, _, f := lwReplicaEnv(t)
		logs := lwLogged(e)

		assert.True(t, ldapCheckReplication(e, f.kit(), lwAdminDN, ldapTestAdmin))
		assert.Contains(t, logs.String(), "Verified ability to query accesslog on master.")
	})

	t.Run("unreachable", func(t *testing.T) {
		e, _, f := lwReplicaEnv(t)
		logs := lwLogged(e)

		k := f.kit()
		k.dial = func(*Env, ldapDial) (ldapConn, error) { return nil, errors.New("connection refused") }

		assert.False(t, ldapCheckReplication(e, k, lwAdminDN, ldapTestAdmin))
		assert.Contains(t, logs.String(), "Unable to contact ldap at "+lwMasterURL)
	})

	t.Run("bad credentials", func(t *testing.T) {
		e, _, f := lwReplicaEnv(t)
		logs := lwLogged(e)
		f.bindOK = func(string, string) bool { return false }

		assert.False(t, ldapCheckReplication(e, f.kit(), lwAdminDN, "wrong"))
		assert.Contains(t, logs.String(), "Unable to bind to "+lwMasterURL)
	})
}

// lwNativeReplicaEnv is a replica pointed at the fake server.
func lwNativeReplicaEnv(t *testing.T, s *lwServer) *Env {
	t.Helper()

	e, _, _ := lwReplicaEnv(t)
	e.Cfg.LDAP.Host = "127.0.0.1"
	e.Cfg.LDAP.Port = s.Port()
	e.Cfg.Host.RequireInterprocessSecurity = true

	return e
}

func TestLDAPIsAvailable_ReplicaAgainstARealMaster(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{startTLS: true})
	s.BindCode = func(dn, pw string) uint8 {
		want := map[string]string{lwAdminDN: ldapTestAdmin, lwReplicaDN: ldapTestRep}[dn]
		if want == "" || want != pw {
			return goldap.LDAPResultInvalidCredentials
		}

		return goldap.LDAPResultSuccess
	}
	s.Search = func(base string, _ int64, _ string) ([]lwEntry, uint8) {
		if base != lwAccesslogDN {
			return nil, goldap.LDAPResultNoSuchObject
		}

		return []lwEntry{{DN: lwAccesslogDN}}, goldap.LDAPResultSuccess
	}

	e := lwNativeReplicaEnv(t, s)

	require.True(t, e.LDAPIsAvailable())
	assert.Equal(t, []string{
		"starttls", "bind " + lwAdminDN,
		"starttls", "bind " + lwReplicaDN,
		"starttls", "bind " + lwAdminDN,
		"search cn=accesslog|0|(cn=accesslog)|cn",
	}, s.Seen(), "credentials only travel over the upgraded connection, and the accesslog is queried as a base search")
	assert.Equal(t, 3, s.TLSConns())

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestAdmin, lc[ldapLCAdmin], "the admin password that bound is stored")
	assert.Equal(t, "ldap://127.0.0.1:"+strconv.Itoa(s.Port()), lc[ldapLCURL])
	assert.Equal(t, "1", lc["ldap_starttls_supported"])
}

func TestLDAPIsAvailable_ReplicaWithoutAccesslogOnTheMasterIsNotAvailable(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.Search = func(string, int64, string) ([]lwEntry, uint8) { return nil, goldap.LDAPResultNoSuchObject }

	e := lwNativeReplicaEnv(t, s)
	e.Cfg.LDAP.ReplicationType = "replica"
	logs := lwLogged(e)

	assert.False(t, e.LDAPIsAvailable())
	assert.Contains(t, logs.String(), "Unable to find accesslog database on master.")
	assert.Contains(t, logs.String(), "zmldapenablereplica")
	assert.False(t, e.Cfg.Host.RequireInterprocessSecurity, "this master does not offer StartTLS: the requirement is dropped")

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "0", lc["ldap_starttls_supported"])
}

func TestLDAPIsAvailable_WrongAdminPasswordIsNotAvailable(t *testing.T) {
	t.Parallel()

	s := lwStart(t, lwOpts{})
	s.BindCode = func(string, string) uint8 { return goldap.LDAPResultInvalidCredentials }

	e := lwNativeReplicaEnv(t, s)
	logs := lwLogged(e)

	assert.False(t, e.LDAPIsAvailable())
	assert.Contains(t, logs.String(), "Unable to bind to ldap://127.0.0.1:"+strconv.Itoa(s.Port())+" with user "+lwAdminDN)
}

// lwSlapd is the systemd state of the fake slapd unit.
type lwSlapd struct{ up atomic.Bool }

// lwCtl is a Controller whose only service is ldap, driven by systemd.
func lwCtl(fakes *systest.Fakes) (*services.Controller, *lwSlapd) {
	slapd := &lwSlapd{}

	fakes.FS.PutDir("/run/systemd/system", 0o755)
	fakes.Exec.OnCmdline("/usr/bin/systemctl is-active", func(*sys.Cmd) (sys.Result, error) {
		if slapd.up.Load() {
			return sys.Result{}, nil
		}

		return sys.Result{ExitCode: 3}, &sys.ExitError{Code: 3}
	})
	fakes.Exec.OnCmdline("/usr/bin/systemctl start", func(*sys.Cmd) (sys.Result, error) {
		slapd.up.Store(true)

		return sys.Result{}, nil
	})

	ctl := services.NewController(fakes.System(),
		services.WithConfigLoader(func() (map[string]string, error) { return map[string]string{}, nil }),
		services.WithRegistry(map[string]*services.ServiceDef{
			"ldap": {Name: "ldap", DisplayName: "LDAP", SystemdUnits: []string{"carbonio-openldap.service"}},
		}))

	return ctl, slapd
}

func lwSystemctl(fakes *systest.Fakes, verb string) []string {
	var out []string

	for _, l := range fakes.Exec.Lines() {
		if strings.HasPrefix(l, "/usr/bin/systemctl "+verb) {
			out = append(out, l)
		}
	}

	return out
}

func TestNativeKit_RunningFollowsTheUnit(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)

	var slapd *lwSlapd

	e.Ctl, slapd = lwCtl(fakes)
	k := ldapNativeKit()

	up, err := k.running(e)
	require.NoError(t, err)
	assert.False(t, up)

	slapd.up.Store(true)

	up, err = k.running(e)
	require.NoError(t, err)
	assert.True(t, up)
}

func TestNativeKit_StartLeavesARunningSlapdAlone(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)

	var slapd *lwSlapd

	e.Ctl, slapd = lwCtl(fakes)
	slapd.up.Store(true)

	require.NoError(t, ldapNativeKit().start(e))
	assert.Empty(t, lwSystemctl(fakes, "start"))
	assert.False(t, e.Flags.LDAPStarted, "only a slapd this run started is remembered as started")
}

func TestNativeKit_StartWithoutSystemdDoesNotWaitOnTheSocket(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Ctl, _ = lwCtl(fakes)
	e.Facts.Systemd = false

	k := ldapNativeKit()
	k.dial = f.kit().dial

	require.NoError(t, k.start(e))
	assert.Empty(t, lwSystemctl(fakes, "start"))
	assert.Equal(t, []string{"/opt/zextras/bin/ldap start"}, lwZextrasCalls(fakes),
		"without systemd slapd is started as zextras, like setup.pl, so zmcontrol can stop it")
	assert.True(t, e.Flags.LDAPStarted)
	assert.Empty(t, f.dials, "no readiness poll: setup.pl only waits under systemd")
}

// lwZextrasCalls returns the commands run as zextras (through runuser).
func lwZextrasCalls(fakes *systest.Fakes) []string {
	var out []string

	for _, c := range fakes.Exec.Calls() {
		if c.Path == "runuser" && len(c.Args) > 6 {
			out = append(out, strings.Join(c.Args[6:], " "))
		}
	}

	return out
}

func TestNativeKit_StartUnderSystemdWaitsUntilSlapdAnswersOnTheSocket(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Ctl, _ = lwCtl(fakes)
	e.Facts.Systemd = true
	require.NoError(t, e.SetLC(map[string]string{"ldap_root_password": ldapTestRoot}))

	tries := 0
	f.bindOK = func(dn, pw string) bool {
		tries++

		return tries >= 3 && dn == ldapConfigDN && pw == ldapTestRoot
	}

	k := ldapNativeKit()
	k.dial = f.kit().dial

	stop := ldapTickClock(fakes)
	defer stop()

	require.NoError(t, k.start(e))
	assert.Equal(t, 3, tries, "polls until the cn=config root binds")
	assert.True(t, e.Flags.LDAPStarted)
	assert.Equal(t, ldapDial{URLs: []string{ldapSocketURL}}, f.dials[0])
}

func TestNativeKit_StartUnderSystemdGivesUpWhenSlapdNeverAnswers(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Ctl, _ = lwCtl(fakes)
	e.Facts.Systemd = true
	f.bindOK = func(string, string) bool { return false }

	k := ldapNativeKit()
	k.dial = f.kit().dial

	stop := ldapTickClock(fakes)
	defer stop()

	err := k.start(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not answer within 30s")
	assert.Len(t, f.dials, ldapWaitSeconds)
}

func TestNativeKit_StartFailureIsReportedAndNotRemembered(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)
	e.Ctl, _ = lwCtl(fakes)
	e.Facts.Systemd = true
	fakes.Exec.OnCmdline("/usr/bin/systemctl start", systest.Fail(1, "Job failed"))

	err := ldapNativeKit().start(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting ldap:")
	assert.Contains(t, err.Error(), "carbonio-openldap.service")
	assert.False(t, e.Flags.LDAPStarted)
}

func TestNativeKit_Stop(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)

	var slapd *lwSlapd

	e.Ctl, slapd = lwCtl(fakes)
	e.Facts.Systemd = true

	k := ldapNativeKit()
	require.NoError(t, k.stop(e))
	assert.Empty(t, lwSystemctl(fakes, "stop"), "a stopped slapd is left alone, like setup.pl stopLdap")

	slapd.up.Store(true)
	require.NoError(t, k.stop(e))
	assert.Equal(t, []string{"/usr/bin/systemctl stop carbonio-openldap.service"}, lwSystemctl(fakes, "stop"))

	fakes.Exec.OnCmdline("/usr/bin/systemctl stop", systest.Fail(5, "not loaded"))

	err := k.stop(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping ldap:")
}

func TestLdapStop_FailureIsLoggedNotReturned(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	logs := lwLogged(e)

	k := f.kit()
	k.stop = func(*Env) error { return errors.New("stopping ldap: boom") }

	k.ldapStop(e)
	assert.Contains(t, logs.String(), "stopping ldap: boom")

	quiet := lwLogged(e)

	f.kit().ldapStop(e)
	assert.Empty(t, quiet.String())
}
