// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	ldapTestHost   = "ldap1.example.com"
	ldapTestRoot   = "RootSecret-1"
	ldapTestAdmin  = "AdminSecret-2"
	ldapTestRep    = "ReplSecret-3"
	ldapTestPost   = "PostSecret-4"
	ldapTestAmavis = "AmavisSecret-5"
	ldapTestNginx  = "NginxSecret-6"
)

// ldapFake records what the steps do through the kit.
type ldapFake struct {
	events []string
	// bindOK decides every bind (nil: all succeed).
	bindOK  func(dn, pw string) bool
	addErr  func(dn string) error
	adds    []string
	replace []string
	setPw   []string
	// search is the answer to every search.
	search  []ldapEntry
	running bool
	// startErr fails the start.
	startErr error
	dials    []ldapDial
}

type ldapFakeConn struct{ f *ldapFake }

func (c ldapFakeConn) Bind(dn, pw string) error {
	c.f.events = append(c.f.events, "bind "+dn)

	if c.f.bindOK != nil && !c.f.bindOK(dn, pw) {
		return goldap.NewError(goldap.LDAPResultInvalidCredentials, errors.New("invalid credentials"))
	}

	return nil
}

func (c ldapFakeConn) Add(dn string, _ []ldapLDIFAttr) error {
	c.f.adds = append(c.f.adds, dn)

	if c.f.addErr != nil {
		return c.f.addErr(dn)
	}

	return nil
}

func (c ldapFakeConn) Replace(dn, attr string, values []string) error {
	c.f.replace = append(c.f.replace, dn+"|"+attr+"|"+strings.Join(values, ","))

	return nil
}

func (c ldapFakeConn) SetPassword(userDN, _ string) error {
	c.f.setPw = append(c.f.setPw, userDN)

	return nil
}

func (c ldapFakeConn) Search(string, string, bool, []string) ([]ldapEntry, error) {
	return c.f.search, nil
}

func (c ldapFakeConn) Close() {}

func (f *ldapFake) kit() *ldapKit {
	return &ldapKit{
		dial: func(_ *Env, d ldapDial) (ldapConn, error) {
			f.dials = append(f.dials, d)

			return ldapFakeConn{f}, nil
		},
		running: func(*Env) (bool, error) { return f.running, nil },
		start: func(*Env) error {
			f.events = append(f.events, "start")

			if f.startErr == nil {
				f.running = true
			}

			return f.startErr
		},
		stop: func(*Env) error {
			f.events = append(f.events, "stop")
			f.running = false

			return nil
		},
	}
}

const ldapTestCarbonioLDIF = `dn: uid=zimbra,cn=admins,cn=zimbra
objectClass: organizationalPerson
cn: zimbra
uid: zimbra
userPassword: {SSHA}old

dn: uid=zmreplica,cn=admins,cn=zimbra
objectClass: organizationalPerson
uid: zmreplica
userPassword: {SSHA}oldrep
`

// ldapTestEnv is a directory-server master on a new install.
func ldapTestEnv(t *testing.T) (*Env, *systest.Fakes, *ldapFake) {
	t.Helper()

	e, fakes, _ := testEnv(t)
	e.Paths.LocalConfig = filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(e.Paths.LocalConfig,
		[]byte("<?xml version=\"1.0\"?><localconfig></localconfig>"), 0o600))

	e.Cfg = &bootstrap.Config{}
	e.Cfg.Host.Hostname = ldapTestHost
	e.Cfg.LDAP = bootstrap.LDAPConfig{
		Host: ldapTestHost, Port: 389,
		RootPassword: ldapTestRoot, AdminPassword: ldapTestAdmin, ReplicationPassword: ldapTestRep,
		PostfixPassword: ldapTestPost, AmavisPassword: ldapTestAmavis, NginxPassword: ldapTestNginx,
	}
	e.Facts = &Facts{
		NewInstall: true,
		Configured: map[string]bool{},
		Installed:  map[string]bool{ldapPkgDirectory: true, PkgCore: true},
		Enabled:    map[string]bool{ldapPkgDirectory: true, PkgCore: true},
	}

	tmpl := "/opt/zextras/common/etc/openldap/zimbra"
	fakes.FS.Put(tmpl+"/carbonio.ldif", []byte(ldapTestCarbonioLDIF), 0o644)
	fakes.FS.Put("/opt/zextras/data/ldap/config/cn=config/olcDatabase={0}config.ldif",
		[]byte("dn: olcDatabase={0}config\nolcRootPW:: b2xk\nolcRootDN: cn=config\n"), 0o600)

	for _, f := range []string{"zimbra_globalconfig.ldif", "zimbra_defaultcos.ldif",
		"zimbra_defaultexternalcos.ldif", "mimehandlers.ldif"} {
		fakes.FS.Put(tmpl+"/"+f, []byte("dn: cn="+strings.TrimSuffix(f, ".ldif")+",cn=zimbra\ncn: x\n"), 0o644)
	}

	return e, fakes, &ldapFake{}
}

func ldapAllLines(fakes *systest.Fakes) string { return strings.Join(fakes.Exec.Lines(), "\n") }

func ldapAssertNoSecret(t *testing.T, fakes *systest.Fakes) {
	t.Helper()

	lines := ldapAllLines(fakes)
	for _, s := range []string{ldapTestRoot, ldapTestAdmin, ldapTestRep, ldapTestPost, ldapTestAmavis, ldapTestNginx} {
		assert.NotContains(t, lines, s)
	}
}

func TestLdapHashPassword_IsSSHA512(t *testing.T) {
	h, err := ldapHashPassword("secret")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "{SSHA512}"))

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "{SSHA512}"))
	require.NoError(t, err)
	require.Len(t, raw, sha512.Size+ldapSaltLen)

	sum := sha512.Sum512(append([]byte("secret"), raw[sha512.Size:]...))
	assert.Equal(t, sum[:], raw[:sha512.Size])

	other, err := ldapHashPassword("secret")
	require.NoError(t, err)
	assert.NotEqual(t, h, other, "the salt is random")
}

func TestLdapParseLDIF(t *testing.T) {
	in := "version: 1\n# comment\n#  folded\n comment\n\ndn: cn=a,cn=zimbra\ncn: a\nsn:: " +
		base64.StdEncoding.EncodeToString([]byte("é")) + "\nmember: x\nmember: y\ndescription: long\n  value\n\n\ndn: cn=b\ncn: b\n"

	got, err := ldapParseLDIF([]byte(in))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "cn=a,cn=zimbra", got[0].DN)
	assert.Equal(t, []ldapLDIFAttr{
		{"cn", []string{"a"}}, {"sn", []string{"é"}}, {"member", []string{"x", "y"}}, {"description", []string{"long value"}},
	}, got[0].Attrs)
	assert.Equal(t, "cn=b", got[1].DN)
}

func TestLdapParseLDIF_Errors(t *testing.T) {
	for _, in := range []string{"cn: a\n", "dn: cn=a\nchangetype: modify\n", "dn: cn=a\nnocolon\n", "dn: cn=a\nx:: !!\n"} {
		_, err := ldapParseLDIF([]byte(in))
		assert.ErrorIs(t, err, errLDAPLDIF, in)
	}
}

func TestLdapLDIFSetAttr(t *testing.T) {
	in := ldapTestCarbonioLDIF + "\ndn: uid=x,cn=zimbra\nuid: x\n"

	out, found, err := ldapLDIFSetPassword([]byte(in), "UID=zmreplica, cn=admins,cn=zimbra", "{SSHA512}new")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, strings.Replace(in, "userPassword: {SSHA}oldrep", "userPassword: {SSHA512}new", 1), string(out))

	// an attribute the entry lacks is added at the end of the entry
	out, found, err = ldapLDIFSetPassword([]byte(in), "uid=x,cn=zimbra", "p")
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, strings.HasSuffix(string(out), "uid: x\nuserPassword: p\n"))

	_, found, err = ldapLDIFSetPassword([]byte(in), "uid=nobody,cn=zimbra", "p")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestLdapLDIFSetAttr_DropsFoldedOldValue(t *testing.T) {
	in := "dn: uid=a\nuserPassword: {SSHA}aaaa\n bbbb\ncn: a\n"
	out, found, err := ldapLDIFSetPassword([]byte(in), "uid=a", "n")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "dn: uid=a\nuserPassword: n\ncn: a\n", string(out))
}

func TestLdapReplaceLine(t *testing.T) {
	out, n := ldapReplaceLine([]byte("a: 1\nolcRootPW:: xx\n yy\nb: 2\n"), "olcRootPW", "olcRootPW:: NEW")
	assert.Equal(t, 1, n)
	assert.Equal(t, "a: 1\nolcRootPW:: NEW\nb: 2\n", string(out))

	_, n = ldapReplaceLine([]byte("a: 1\n"), "olcRootPW", "x")
	assert.Zero(t, n)
}

func TestLdapStepInstallConfig(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)
	src := "/opt/zextras/common/etc/openldap/zimbra/config/"

	for _, f := range ldapConfigFiles() {
		fakes.FS.Put(src+f, []byte(f), 0o644)
	}

	s := StepInstallLdapConfig()
	assert.False(t, s.When(e), "data/ldap/config does not exist")

	fakes.FS.PutDir("/opt/zextras/data/ldap/config", 0o750)
	require.True(t, s.When(e))

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, s.Apply(e))

	for _, f := range ldapConfigFiles() {
		data, ok := fakes.FS.Data("/opt/zextras/data/ldap/config/" + f)
		require.True(t, ok, f)
		assert.Equal(t, f, string(data))
	}

	mode, _ := fakes.FS.Mode("/opt/zextras/data/ldap/config/cn=config.ldif")
	assert.Equal(t, os.FileMode(0o600), mode.Perm())
	mode, _ = fakes.FS.Mode("/opt/zextras/data/ldap/config/cn=config/cn=schema.ldif")
	assert.Equal(t, os.FileMode(0o600), mode.Perm())
	assert.Contains(t, ldapAllLines(fakes), "chown -R zextras:zextras /opt/zextras/data/ldap/config")

	// once the database exists, a second run leaves cn=config alone
	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestLdapStepInstallConfig_ExistingInstallOnlyWhenTreeMissing(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)
	e.Facts.NewInstall = false
	fakes.FS.PutDir("/opt/zextras/data/ldap/config", 0o750)

	s := StepInstallLdapConfig()
	assert.True(t, s.When(e))

	fakes.FS.Put("/opt/zextras/data/ldap/config/cn=config.ldif", []byte("x"), 0o600)
	assert.False(t, s.When(e))

	e.Facts.Installed[ldapPkgDirectory] = false
	e.Facts.NewInstall = true
	assert.False(t, s.When(e))
}

func TestLdapStepSchemaAndUpdateLdif(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)

	schema := StepInstallLdapSchema()
	require.True(t, schema.When(e))
	assert.True(t, schema.Optional)
	require.NoError(t, schema.Apply(e))
	assert.Contains(t, ldapAllLines(fakes), "/opt/zextras/libexec/zmldapschema")

	upd := StepZmldapupdateldif()
	assert.False(t, upd.When(e), "new install")

	e.Facts.NewInstall = false
	require.True(t, upd.When(e))

	done, err := upd.Done(e)
	require.NoError(t, err)
	assert.True(t, done, "ldap_is_master is not true")

	require.NoError(t, e.SetLC(map[string]string{ldapLCIsMaster: "true"}))

	done, err = upd.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, upd.Apply(e))
	assert.Equal(t, "/opt/zextras/libexec/zmldapupdateldif", fakes.Exec.Lines()[len(fakes.Exec.Lines())-1],
		"as root: no runuser wrapper")
}

func TestLdapPreStartWritesHashesAndLocalConfig(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)

	require.NoError(t, ldapPreStart(e, ldapTestRoot, ldapTestAdmin))

	data, ok := fakes.FS.Data("/opt/zextras/data/ldap/config/cn=config/olcDatabase={0}config.ldif")
	require.True(t, ok)

	var enc string

	for line := range strings.SplitSeq(string(data), "\n") {
		if v, found := strings.CutPrefix(line, "olcRootPW:: "); found {
			enc = v
		}
	}

	hash, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)
	ldapAssertHash(t, string(hash), ldapTestRoot)
	assert.Contains(t, string(data), "olcRootDN: cn=config")

	ldif, ok := fakes.FS.Data("/opt/zextras/conf/carbonio.ldif")
	require.True(t, ok)

	_, rest, _ := strings.Cut(string(ldif), "dn: uid=zimbra,cn=admins,cn=zimbra\n")
	entry, _, _ := strings.Cut(rest, "\n\n")

	var adminHash string

	for line := range strings.SplitSeq(entry, "\n") {
		if v, found := strings.CutPrefix(line, "userPassword: "); found {
			adminHash = v
		}
	}

	ldapAssertHash(t, adminHash, ldapTestAdmin)
	assert.Contains(t, string(ldif), "userPassword: {SSHA}oldrep", "other entries are untouched")

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestRoot, lc[ldapLCRoot])
	assert.Equal(t, ldapTestAdmin, lc[ldapLCAdmin])
	assert.Equal(t, "true", lc[ldapLCIsMaster])

	mode, _ := fakes.FS.Mode("/opt/zextras/conf/carbonio.ldif")
	assert.Equal(t, os.FileMode(0o640), mode.Perm()) // as setup.pl left it
	assert.Contains(t, ldapAllLines(fakes), "chown zextras:zextras /opt/zextras/conf/carbonio.ldif")
	ldapAssertNoSecret(t, fakes)
}

func ldapAssertHash(t *testing.T, hash, password string) {
	t.Helper()

	require.True(t, strings.HasPrefix(hash, "{SSHA512}"), hash)

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(hash, "{SSHA512}"))
	require.NoError(t, err)

	sum := sha512.Sum512(append([]byte(password), raw[sha512.Size:]...))
	assert.Equal(t, sum[:], raw[:sha512.Size])
}

func TestLdapPreStart_NoRootPWLineFails(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)
	fakes.FS.Put("/opt/zextras/data/ldap/config/cn=config/olcDatabase={0}config.ldif", []byte("dn: x\n"), 0o600)

	err := ldapPreStart(e, ldapTestRoot, ldapTestAdmin)
	assert.ErrorIs(t, err, errLDAPNoRootPW)
}

func TestLdapPostStart(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	require.NoError(t, ldapPreStart(e, ldapTestRoot, ldapTestAdmin))
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389", "ldap_starttls_supported": "1"}))

	f.addErr = func(dn string) error {
		if strings.Contains(dn, "defaultcos") {
			return goldap.NewError(goldap.LDAPResultEntryAlreadyExists, errors.New("exists"))
		}

		return nil
	}

	rc, err := ldapPostStart(e, f.kit())
	require.NoError(t, err)
	assert.Zero(t, rc)
	assert.Equal(t, []string{
		"uid=zimbra,cn=admins,cn=zimbra", "uid=zmreplica,cn=admins,cn=zimbra",
		"cn=zimbra_globalconfig,cn=zimbra", "cn=zimbra_defaultcos,cn=zimbra",
		"cn=zimbra_defaultexternalcos,cn=zimbra", "cn=mimehandlers,cn=zimbra",
	}, f.adds)
	assert.Equal(t, []string{"bind cn=config"}, f.events)
	assert.Equal(t, ldapDial{URLs: []string{"ldap://" + ldapTestHost + ":389"}, TLS: ldapTLSInsecure}, f.dials[0])

	_ = fakes
}

func TestLdapPostStart_UsesLocalMimehandlersAndReportsFailures(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	require.NoError(t, ldapPreStart(e, ldapTestRoot, ldapTestAdmin))
	fakes.FS.Put("/opt/zextras/conf/ldap/mimehandlers.ldif", []byte("dn: cn=local,cn=zimbra\ncn: l\n"), 0o644)

	_, err := ldapPostStart(e, f.kit())
	require.NoError(t, err)
	assert.Equal(t, "cn=local,cn=zimbra", f.adds[len(f.adds)-1])

	f.bindOK = func(string, string) bool { return false }
	rc, err := ldapPostStart(e, f.kit())
	require.Error(t, err)
	assert.Equal(t, 2, rc)

	f.bindOK = nil
	f.addErr = func(string) error {
		return goldap.NewError(goldap.LDAPResultInsufficientAccessRights, errors.New("no"))
	}
	rc, err = ldapPostStart(e, f.kit())
	require.Error(t, err)
	assert.Equal(t, 2, rc)

	fakes.FS.Put("/opt/zextras/common/etc/openldap/zimbra/zimbra_defaultcos.ldif", []byte("garbage\n"), 0o644)
	f.addErr = nil
	rc, err = ldapPostStart(e, f.kit())
	require.Error(t, err)
	assert.Equal(t, 1, rc)
}

func TestLdapSetPassword_AccountFallsBackToRootAndRecordsHash(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	require.NoError(t, ldapPreStart(e, ldapTestRoot, ldapTestAdmin))
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))

	f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN && pw == ldapTestRoot }

	require.NoError(t, ldapSetPassword(e, f.kit(), ldapAcctPostfix, ldapTestPost))

	assert.Equal(t, []string{"bind " + ldapDNPostfix, "bind cn=config"}, f.events)
	assert.Equal(t, []string{ldapDNPostfix}, f.setPw)

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestPost, lc["ldap_postfix_password"])
	ldapAssertNoSecret(t, fakes)
}

func TestLdapSetPassword_AcceptedPasswordIsNotChanged(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))

	require.NoError(t, ldapSetPassword(e, f.kit(), ldapAcctAdmin, ldapTestAdmin))

	assert.Empty(t, f.setPw)
	assert.Equal(t, []string{"bind uid=zimbra,cn=admins,cn=zimbra"}, f.events)
}

func TestLdapSetPassword_RootUsesTheSocketAndReplacesOlcRootPW(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	require.NoError(t, e.SetLC(map[string]string{ldapLCRoot: "oldroot"}))

	f.bindOK = func(_, pw string) bool { return pw == "oldroot" }

	require.NoError(t, ldapSetPassword(e, f.kit(), ldapAcctRoot, ldapTestRoot))

	assert.Equal(t, []ldapDial{{URLs: []string{ldapSocketURL}}}, f.dials)
	require.Len(t, f.replace, 1)

	dn, rest, _ := strings.Cut(f.replace[0], "|")
	attr, hash, _ := strings.Cut(rest, "|")
	assert.Equal(t, ldapDNRootDB, dn)
	assert.Equal(t, "olcRootPW", attr)
	ldapAssertHash(t, hash, ldapTestRoot)

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestRoot, lc[ldapLCRoot])
}

func TestLdapSetPassword_ReplicationUpdatesSyncreplAndLDIF(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	require.NoError(t, ldapPreStart(e, ldapTestRoot, ldapTestAdmin))
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))

	f.bindOK = func(dn, _ string) bool { return dn == ldapConfigDN }
	f.search = []ldapEntry{{DN: ldapDNMdbDB, Attrs: map[string][]string{
		"olcSyncrepl": {"{0}rid=100 provider=ldap://m credentials=OLDPW filter=(objectclass=*)"},
	}}}

	require.NoError(t, ldapSetPassword(e, f.kit(), ldapAcctReplica, ldapTestRep))

	assert.Equal(t, []string{ldapDNReplica}, f.setPw)
	assert.Equal(t, []string{ldapDNMdbDB + "|olcSyncrepl|{0}rid=100 provider=ldap://m credentials=" +
		ldapTestRep + " filter=(objectclass=*)"}, f.replace)

	ldif, _ := fakes.FS.Data("/opt/zextras/conf/carbonio.ldif")
	assert.NotContains(t, string(ldif), "{SSHA}oldrep")
	assert.NotContains(t, string(ldif), ldapTestRep)
	ldapAssertNoSecret(t, fakes)
}

func TestLdapSetPassword_Errors(t *testing.T) {
	e, _, f := ldapTestEnv(t)

	require.Error(t, ldapSetPassword(e, f.kit(), ldapAcctAdmin, ""))

	f.bindOK = func(string, string) bool { return false }
	require.Error(t, ldapSetPassword(e, f.kit(), ldapAcctPostfix, ldapTestPost))
}

func TestLdapStepConfigSetupLdap_NewMasterOrder(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Cfg.LDAP.ForceReplication = true
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))

	f.running = true
	f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN && pw == ldapTestRoot }

	s := ldapStepConfigSetupLdap(f.kit())
	assert.Equal(t, "configSetupLdap", s.Name)

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, s.Apply(e))

	// stop, then start, then postLdapStart binds as cn=config, then the four
	// account passwords are set
	assert.Equal(t, []string{"stop", "start", "bind cn=config",
		"bind " + ldapDNReplica, "bind cn=config",
		"bind " + ldapDNPostfix, "bind cn=config",
		"bind " + ldapDNAmavis, "bind cn=config",
		"bind " + ldapDNNginx, "bind cn=config"}, f.events)
	assert.Len(t, f.adds, 6)
	assert.Equal(t, []string{ldapDNReplica, ldapDNPostfix, ldapDNAmavis, ldapDNNginx}, f.setPw)

	assert.Contains(t, ldapAllLines(fakes), "/opt/zextras/libexec/zmldapenablereplica")
	assert.True(t, fakes.FS.Exists("/opt/zextras/.enable_replica"))
	ldapAssertNoSecret(t, fakes)

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestRep, lc["ldap_replication_password"])
	assert.Equal(t, ldapTestNginx, lc["ldap_nginx_password"])
}

func TestLdapStepConfigSetupLdap_NewMasterFailsWhenLoadFails(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	f.bindOK = func(string, string) bool { return false }

	err := ldapStepConfigSetupLdap(f.kit()).Apply(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initializing ldap failed (2)")
}

func TestLdapStepConfigSetupLdap_DoneWhenStageRecorded(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	e.Facts.Configured["configSetupLdap"] = true

	done, err := ldapStepConfigSetupLdap(f.kit()).Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestLdapStepConfigSetupLdap_NotADirectoryServerOnlySetsLocalConfig(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Facts.Enabled[ldapPkgDirectory] = false
	e.Cfg.LDAP.Host = "other.example.com"
	e.Cfg.LDAP.RootPassword = ""
	e.Cfg.LDAP.NginxPassword = ""

	require.NoError(t, ldapStepConfigSetupLdap(f.kit()).Apply(e))

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestAdmin, lc[ldapLCAdmin])
	assert.Equal(t, ldapTestPost, lc["ldap_postfix_password"])
	assert.Equal(t, ldapTestAmavis, lc["ldap_amavis_password"])
	assert.Equal(t, "zimbra", lc[ldapLCRoot], "an empty configured password does not overwrite the default")
	assert.Empty(t, f.events)
	assert.Empty(t, fakes.Exec.Lines())
}

func TestLdapStepConfigSetupLdap_ReplicaEnabled(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Cfg.LDAP.Host = "master.example.com"
	e.Cfg.LDAP.ReplicationType = bootstrap.ReplicationReplica
	e.Cfg.Admin.Create = true
	e.Cfg.Domain.Create = true
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://master.example.com:389"}))

	require.NoError(t, ldapStepConfigSetupLdap(f.kit()).Apply(e))

	assert.Contains(t, ldapAllLines(fakes), "/opt/zextras/libexec/zmldapenablereplica")
	assert.NotContains(t, ldapAllLines(fakes), "zmldapenable-mmr")
	assert.True(t, fakes.FS.Exists("/opt/zextras/.enable_replica"))
	assert.False(t, e.Cfg.Domain.Create)
	assert.False(t, e.Cfg.Admin.Create, "no appserver: no admin either")
	assert.Equal(t, []string{"stop", "start"}, f.events)

	// the root password reached localconfig before the LDIF was applied
	ldif, _ := fakes.FS.Data("/opt/zextras/data/ldap/config/cn=config/olcDatabase={0}config.ldif")
	assert.NotContains(t, string(ldif), "b2xk")

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestRoot, lc[ldapLCRoot])
	assert.Equal(t, ldapTestRep, lc["ldap_replication_password"])
}

func TestLdapStepConfigSetupLdap_ReplicaKeepsAdminWithAppserver(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	e.Cfg.LDAP.Host = "master.example.com"
	e.Facts.Enabled[PkgAppserver] = true
	e.Cfg.Admin.Create = true

	require.NoError(t, ldapStepConfigSetupLdap(f.kit()).Apply(e))
	assert.True(t, e.Cfg.Admin.Create)
}

func TestLdapStepConfigSetupLdap_MMR(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Cfg.LDAP.Host = "master.example.com"
	e.Cfg.LDAP.ReplicationType = bootstrap.ReplicationMMR
	e.Cfg.LDAP.ServerID = 7
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://master.example.com:389"}))

	f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN }

	require.NoError(t, ldapStepConfigSetupLdap(f.kit()).Apply(e))

	lines := ldapAllLines(fakes)
	assert.Contains(t, lines, "libexec/zmldapenable-mmr -s 7 -m ldap://master.example.com:389/")
	assert.Contains(t, lines, "runuser -u zextras")
	assert.Equal(t, []string{ldapDNPostfix, ldapDNAmavis, ldapDNNginx}, f.setPw)

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "true", lc[ldapLCIsMaster])
	assert.Equal(t, "ldap://"+ldapTestHost+":389 ldap://master.example.com:389", lc[ldapLCURL])
	ldapAssertNoSecret(t, fakes)
}

func TestLdapStepConfigSetupLdap_ReplicaFailureDisablesDirectoryServer(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Cfg.LDAP.Host = "master.example.com"
	e.Cfg.Admin.Create = true
	e.Cfg.Domain.Create = true
	fakes.Exec.OnPath("/opt/zextras/libexec/zmldapenablereplica", systest.Fail(1, "boom"))

	require.NoError(t, ldapStepConfigSetupLdap(f.kit()).Apply(e), "setup.pl carried on")

	assert.True(t, e.Cfg.Domain.Create)
	assert.Equal(t, []string{"stop"}, f.events)
	assert.True(t, fakes.FS.Exists("/opt/zextras/.enable_replica"))

	var batch string

	for _, c := range fakes.Exec.Calls() {
		if c.Stdin != nil {
			var b bytes.Buffer

			_, _ = b.ReadFrom(c.Stdin)
			batch += b.String()
		}
	}

	assert.Contains(t, batch, `"ms" "ldap1.example.com" "-zimbraServiceEnabled" "directory-server"`)
}

func TestLdapStepConfigSetupLdap_ExistingMasterRunsUpdateLdifAsRoot(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Facts.NewInstall = false

	require.NoError(t, ldapStepConfigSetupLdap(f.kit()).Apply(e))

	assert.Equal(t, []string{"/opt/zextras/libexec/zmldapupdateldif"}, fakes.Exec.Lines())
	assert.Empty(t, f.events)
}

func TestLdapStepUpdatePasswords_NonDirectoryNode(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Facts.Enabled[ldapPkgDirectory] = false
	e.Facts.Enabled[PkgMTA] = true
	e.Cfg.LDAP.Host = "other.example.com"

	s := ldapStepUpdatePasswords(f.kit())
	require.True(t, s.When(e))

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, s.Apply(e))

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, ldapTestPost, lc["ldap_postfix_password"])
	assert.Equal(t, ldapTestAmavis, lc["ldap_amavis_password"])
	assert.NotEqual(t, ldapTestNginx, lc["ldap_nginx_password"], "no proxy here")
	assert.Empty(t, f.events, "a remote directory is not touched")

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
	ldapAssertNoSecret(t, fakes)
}

func TestLdapStepUpdatePasswords_DirectoryWhenConfiguredOnExistingInstall(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	s := ldapStepUpdatePasswords(f.kit())

	assert.False(t, s.When(e), "new install: configSetupLdap sets them")

	e.Facts.NewInstall = false
	assert.False(t, s.When(e), "no database yet")

	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)
	require.True(t, s.When(e))

	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))
	f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN && pw == ldapTestRoot }

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, s.Apply(e))
	assert.Equal(t, "start", f.events[0])
	assert.Equal(t, []string{ldapDNAdminForTest, ldapDNReplica, ldapDNPostfix, ldapDNAmavis, ldapDNNginx}, f.setPw,
		"the root password binds as it is; every other account needs the root bind")

	ldapAssertNoSecret(t, fakes)
}

func TestLdapStepUpdatePasswords_DoneNeedsTheDirectoryToAgreeToo(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	e.Facts.NewInstall = false
	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)

	lcv := map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}
	for _, a := range ldapAllAccounts() {
		lcv[a.LCKey] = a.get(&e.Cfg.LDAP)
	}

	require.NoError(t, e.SetLC(lcv))

	s := ldapStepUpdatePasswords(f.kit())

	// slapd is down: localconfig agrees, nothing to do
	done, err := s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)

	// slapd runs and refuses the postfix password
	f.running = true
	f.bindOK = func(dn, _ string) bool { return dn != ldapDNPostfix }

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)
}

func TestLdapStepStartLdap(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	s := ldapStepStartLdap(f.kit())

	assert.False(t, s.When(e), "new install")

	e.Facts.NewInstall = false
	assert.False(t, s.When(e), "no database")

	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)
	require.True(t, s.When(e))
	assert.True(t, s.Optional)

	done, err := s.Done(e)
	require.NoError(t, err)
	assert.False(t, done)

	require.NoError(t, s.Apply(e))
	assert.Equal(t, []string{"start"}, f.events)

	done, err = s.Done(e)
	require.NoError(t, err)
	assert.True(t, done)
}

func TestLdapStepConfigLDAPSchemaVersion(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)
	fakes.FS.Put("/opt/zextras/conf/attrs-schema", []byte("42\n"), 0o644)

	s := StepConfigLDAPSchemaVersion()
	require.True(t, s.When(e))
	assert.True(t, s.Optional)

	e.Facts.Enabled[ldapPkgDirectory] = false
	assert.False(t, s.When(e))

	require.NoError(t, s.Apply(e))

	var batch string

	for _, c := range fakes.Exec.Calls() {
		if c.Stdin != nil {
			var b bytes.Buffer

			_, _ = b.ReadFrom(c.Stdin)
			batch += b.String()
		}
	}

	assert.Contains(t, batch, `"mcf" "zimbraLDAPSchemaVersion" "42"`)
}

func TestLdapStepConfigLDAPSchemaVersion_NoFile(t *testing.T) {
	e, _, _ := ldapTestEnv(t)
	assert.Error(t, StepConfigLDAPSchemaVersion().Apply(e))
}

// ldapTickClock advances the fake clock whenever the code under test sleeps.
func ldapTickClock(fakes *systest.Fakes) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		for {
			select {
			case <-done:
				return
			default:
			}

			if fakes.Clock.Waiters() > 0 {
				fakes.Clock.Advance(time.Second)
			}

			runtime.Gosched()
		}
	}()

	return func() {
		close(done)
		<-finished
	}
}

func TestLdapWaitForPollsTheSocket(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	k := f.kit()

	tries := 0
	f.bindOK = func(string, string) bool {
		tries++

		return tries >= 3
	}

	stop := ldapTickClock(fakes)
	defer stop()

	require.NoError(t, k.waitFor(e, 30))
	assert.Equal(t, 3, tries)
	assert.Equal(t, ldapDial{URLs: []string{ldapSocketURL}}, f.dials[0])
}

func TestLdapWaitForGivesUp(t *testing.T) {
	e, fakes, f := ldapTestEnv(t)
	f.bindOK = func(string, string) bool { return false }

	stop := ldapTickClock(fakes)
	defer stop()

	err := f.kit().waitFor(e, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not answer within 3s")
	assert.Len(t, f.dials, 3)
}

func TestLdapConfigured(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)
	assert.False(t, e.LDAPConfigured())

	fakes.FS.Put("/opt/zextras/data/ldap/mdb/db/data.mdb", []byte("x"), 0o600)
	assert.True(t, e.LDAPConfigured())
}

func TestLdapIsAvailable(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	e.Cfg.LDAP.Host = "master.example.com"
	e.Cfg.Host.RequireInterprocessSecurity = true
	e.Facts.Installed[PkgMTA] = true
	require.NoError(t, e.SetLC(map[string]string{"zimbra_ldap_userdn": ldapDNAdminForTest}))

	assert.True(t, ldapIsAvailable(e, f.kit()))
	assert.Equal(t, ldapDial{URLs: []string{"ldap://master.example.com:389"}, TLS: ldapTLSInsecure}, f.dials[0])

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "ldap://master.example.com:389", lc[ldapLCURL])
	assert.Equal(t, "1", lc["ldap_starttls_supported"])
	assert.Equal(t, "true", lc["ssl_allow_untrusted_certs"])
	assert.Equal(t, ldapTestAdmin, lc[ldapLCAdmin])

	f.bindOK = func(dn, _ string) bool { return dn != ldapDNPostfix }
	assert.False(t, ldapIsAvailable(e, f.kit()))
}

const ldapDNAdminForTest = "uid=zimbra,cn=admins,cn=zimbra"

func TestLdapIsAvailable_MasterNotConfiguredYet(t *testing.T) {
	e, _, f := ldapTestEnv(t)
	assert.False(t, ldapIsAvailable(e, f.kit()))
	assert.Empty(t, f.dials)
}
