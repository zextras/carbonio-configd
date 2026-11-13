// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ldapBindZimbra = "bind uid=zimbra,cn=admins,cn=zimbra"

func ldapHas(entries []ldapEntry, base, filter string) func(b, f string) ([]ldapEntry, error) {
	return func(b, f string) ([]ldapEntry, error) {
		if b == base && f == filter {
			return entries, nil
		}

		return nil, nil
	}
}

// TestLdapEnableReplica_Master follows libexec/ldapenablereplica.pl for the
// node that is the master: it refuses a node that has the accesslog database
// or syncprov already, else it adds the accesslog database and the overlays.
func TestLdapEnableReplica_Master(t *testing.T) {
	cases := map[string]struct {
		search func(b, f string) ([]ldapEntry, error)
		out    string
		adds   int
		code   int
	}{
		"fresh": {nil, "Enabling sync provider on master...\nsucceeded\n", 4, 0},
		"accesslog exists": {
			ldapHas([]ldapEntry{{DN: "cn=accesslog"}}, "cn=accesslog", "(objectClass=*)"),
			"Accesslog is already enabled. Exiting...\n", 0, 1,
		},
		"syncprov exists": {
			ldapHas([]ldapEntry{{DN: "x"}}, ldapDNMdbDB, "(olcOverlay=syncprov)"),
			"Error: Syncprov already enabled. Exiting...\n", 0, 1,
		},
	}

	for name, c := range cases {
		e, fakes, f := ldapToolEnv(t)
		require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))

		if c.search != nil {
			f.searchFn = c.search
		}

		var out, errb bytes.Buffer

		code := RunLDAPEnableReplica(e, nil, &out, &errb)

		assert.Equal(t, c.code, code, name)
		assert.Equal(t, c.out, out.String(), name)
		assert.Len(t, f.adds, c.adds, name)
		assert.Equal(t, []string{"bind cn=config"}, f.events, name)

		if c.adds > 0 {
			assert.Contains(t, ldapAllLines(fakes), "chown -R zextras:zextras /opt/zextras/data/ldap/accesslog", name)
		}
	}
}

// The ACL of the accesslog database is the literal of ldapenable-mmr.pl and
// ldapenablereplica.pl, blanks included.
func TestLdapEnableReplica_AccesslogACLIsTheLegacyLiteral(t *testing.T) {
	e, _, f := ldapToolEnv(t)
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))

	var out, errb bytes.Buffer

	require.Equal(t, 0, RunLDAPEnableReplica(e, nil, &out, &errb))

	var acl []string

	for _, a := range f.attrs[ldapDNMdbDB] {
		if a.Name == "olcAccess" {
			acl = a.Values
		}
	}

	assert.Equal(t, []string{`{0}to dn.subtree="cn=accesslog"  by dn.exact="uid=zimbra,cn=admins,cn=zimbra" read  ` +
		`by dn.exact="cn=config" read  by dn.exact="uid=zmreplica,cn=admins,cn=zimbra" read`}, acl)
}

func TestLdapEnableReplica_MasterFailures(t *testing.T) {
	e, _, f := ldapToolEnv(t)
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))
	f.addErr = func(string) error { return ldapDiag(goldap.LDAPResultOther, "no space") }

	var out, errb bytes.Buffer

	assert.Equal(t, 255, RunLDAPEnableReplica(e, nil, &out, &errb))
	assert.Equal(t, "Add Failed: no space\n", errb.String())

	e, _, f = ldapToolEnv(t)
	require.NoError(t, e.SetLC(map[string]string{ldapLCMasterURL: "ldap://" + ldapTestHost + ":389"}))
	f.bindOK = func(string, string) bool { return false }
	errb.Reset()

	assert.Equal(t, 255, RunLDAPEnableReplica(e, nil, &out, &errb))
	assert.Equal(t, "Bind: invalid credentials\n", errb.String())
}

// TestLdapEnableReplica_Replica follows the steps of the script for a node
// that is not the master: verify the master, set ldap_url, start slapd, add
// syncrepl and the syncprov overlay, enable the ldap service.
func TestLdapEnableReplica_Replica(t *testing.T) {
	const want = "Verifying ldap on ldap://master.example.com:389...\nsucceeded\n" +
		"Setting ldap_url on ldap1.example.com...done\nStarting LDAP on ldap1.example.com...done\n" +
		"Enabling sync replication on replica...succeeded\nEnabling LDAP service on ldap1.example.com...succeeded\n"

	cases := map[string]struct {
		lc      map[string]string
		url     string
		tls     bool
		syncTLS string
		dial    ldapDial
	}{
		"plain": {
			nil, "ldap://ldap1.example.com:389 " + ldapToolLMR, false, "",
			ldapDial{URLs: []string{ldapToolLMR}},
		},
		"ldaps port": {
			map[string]string{"ldap_port": "636"}, "ldaps://ldap1.example.com:636 " + ldapToolLMR, false, "",
			ldapDial{URLs: []string{ldapToolLMR}},
		},
		"starttls to verify and in the agreement": {
			map[string]string{"ldap_starttls_supported": "1", "zimbra_require_interprocess_security": "1"},
			"ldap://ldap1.example.com:389 " + ldapToolLMR, true, " starttls=critical",
			ldapDial{URLs: []string{ldapToolLMR}, TLS: ldapTLSInsecure},
		},
		"starttls supported but not required": {
			map[string]string{"ldap_starttls_supported": "1"},
			"ldap://ldap1.example.com:389 " + ldapToolLMR, true, "",
			ldapDial{URLs: []string{ldapToolLMR}, TLS: ldapTLSInsecure},
		},
	}

	for name, c := range cases {
		e, fakes, f := ldapToolEnv(t)
		require.NoError(t, e.SetLC(c.lc))

		var out, errb bytes.Buffer

		require.Equal(t, 0, RunLDAPEnableReplica(e, nil, &out, &errb), name)

		assert.Equal(t, want, out.String(), name)
		assert.Equal(t, []ldapDial{c.dial}, f.dials[:1], name)
		assert.Equal(t, []string{ldapBindZimbra, "start", "bind cn=config"}, f.events, name)

		lc, err := e.LC()
		require.NoError(t, err)
		assert.Equal(t, c.url, lc[ldapLCURL], name)

		require.Len(t, f.mods, 2, name)
		assert.Equal(t, ldapDNMdbDB+"|add|olcUpdateRef|"+ldapToolLMR, f.mods[1], name)
		assert.True(t, strings.HasPrefix(f.mods[0], ldapDNMdbDB+"|add|olcSyncrepl|rid=100 provider="+ldapToolLMR+
			" bindmethod=simple timeout=0 network-timeout=0 binddn=uid=zmreplica,cn=admins,cn=zimbra credentials="+
			ldapTestRep+c.syncTLS+` filter="(objectclass=*)"`), name+": "+f.mods[0])
		assert.Equal(t, []string{"olcOverlay={0}syncprov," + ldapDNMdbDB}, f.adds, name)

		assert.Equal(t, []provCall{{
			DN: "cn=ldap1.example.com,cn=servers,cn=zimbra", Add: map[string][]string{"zimbraServiceEnabled": {"ldap"}},
			Del: map[string][]string{}, Replace: map[string][]string{},
		}}, provTestLDAP().Calls, name)
		ldapAssertNoSecret(t, fakes)
	}
}

func TestLdapEnableReplica_ReplicaFailures(t *testing.T) {
	verified := "Verifying ldap on " + ldapToolLMR + "...\nsucceeded\n"
	started := verified + "Setting ldap_url on ldap1.example.com...done\nStarting LDAP on ldap1.example.com...done\n"
	reset := "Resetting ldap_url on " + ldapToolLMR + "...done\n"
	failedSyncrepl := started + "Enabling sync replication on replica..."

	cases := map[string]struct {
		setup  func(f *ldapFake)
		out    string
		errOut string
		code   int
		url    string
	}{
		"master does not accept the bind": {
			func(f *ldapFake) {
				f.bindOK = func(dn, _ string) bool { return dn != "uid=zimbra,cn=admins,cn=zimbra" }
			},
			"Verifying ldap on " + ldapToolLMR + "...\n",
			"ERROR: Unable to verify ldap connection on " + ldapToolLMR + " - exiting\n", 255, ldapToolLMR,
		},
		"slapd does not start": {
			func(f *ldapFake) { f.startErr = errors.New("boom") },
			verified + "Setting ldap_url on ldap1.example.com...done\nStarting LDAP on ldap1.example.com..." +
				"Error: Unable to start ldap, exiting.\n" + reset,
			"", 1, ldapToolLMR,
		},
		"accesslog exists": {
			func(f *ldapFake) {
				f.searchFn = ldapHas([]ldapEntry{{DN: "cn=accesslog"}}, "cn=accesslog", "(objectClass=*)")
			},
			started + "Accesslog is already enabled. Exiting...\n", "", 1,
			"ldap://ldap1.example.com:389 " + ldapToolLMR,
		},
		"syncrepl is refused": {
			func(f *ldapFake) {
				f.modifyErr = func(string) error { return ldapDiag(goldap.LDAPResultOther, "nope") }
			},
			failedSyncrepl + "FAILED\nERROR: Unable to add syncrepl configuration to ldap1.example.com - exiting\n" +
				"ERROR: nope\n" + reset,
			"", 1, ldapToolLMR,
		},
		"syncprov overlay is refused": {
			func(f *ldapFake) { f.addErr = func(string) error { return ldapDiag(goldap.LDAPResultOther, "nope") } },
			failedSyncrepl + "FAILEDERROR: Unable to add syncprov overlay to ldap1.example.com - exiting\n" +
				"ERROR: nope\n" + reset,
			"", 1, ldapToolLMR,
		},
	}

	for name, c := range cases {
		e, _, f := ldapToolEnv(t)
		c.setup(f)

		var out, errb bytes.Buffer

		assert.Equal(t, c.code, RunLDAPEnableReplica(e, nil, &out, &errb), name)
		assert.Equal(t, c.out, out.String(), name)
		assert.Equal(t, c.errOut, errb.String(), name)

		lc, err := e.LC()
		require.NoError(t, err)

		if c.url != "" {
			assert.Equal(t, c.url, lc[ldapLCURL], name)
		}
	}
}

func TestRunLDAPEnableReplica_Options(t *testing.T) { // ldapenablereplica.pl 24-38
	for _, args := range [][]string{{"-h"}, {"--help"}, {"--bogus"}, {"-x"}} {
		e, _, f := ldapToolEnv(t)

		var out, errb bytes.Buffer

		assert.Equal(t, 1, RunLDAPEnableReplica(e, args, &out, &errb), args)
		assert.Contains(t, errb.String(), "Usage:", args)
		assert.Empty(t, f.events, args)
	}

	e, fakes, _ := ldapToolEnv(t)
	require.NoError(t, fakes.FS.RemoveAll(ldapToolRoot+"/schema"))

	var out, errb bytes.Buffer

	assert.Equal(t, 1, RunLDAPEnableReplica(e, nil, &out, &errb))
	assert.Equal(t, "ERROR: openldap does not appear to be installed - exiting\n", out.String())
}

func ldapMMREnv(t *testing.T) (*Env, *ldapFake) {
	t.Helper()

	e, _, f := ldapToolEnv(t)
	require.NoError(t, e.SetLC(map[string]string{ldapLCIsMaster: "true"}))

	return e, f
}

// serverIDs answers the searches of ldapenable-mmr.pl: the server id of cn=config,
// the agreements of the main database and the mirror mode.
func ldapMMRSearch(ids, agreements []string, mirror bool) func(b, f string) ([]ldapEntry, error) {
	return func(b, f string) ([]ldapEntry, error) {
		switch {
		case strings.Contains(f, "olcServerID=*") && len(ids) > 0:
			return []ldapEntry{{DN: b, Attrs: map[string][]string{"olcServerID": ids}}}, nil
		case f == "(olcSyncrepl=*)" && len(agreements) > 0:
			return []ldapEntry{{DN: b, Attrs: map[string][]string{"olcSyncrepl": agreements}}}, nil
		case strings.Contains(f, "olcMirrorMode") && mirror:
			return []ldapEntry{{DN: b}}, nil
		}

		return nil, nil
	}
}

// TestLdapEnableMMR follows libexec/ldapenable-mmr.pl.
func TestLdapEnableMMR(t *testing.T) {
	uri := "ldap://other.example.com:389/"
	cases := map[string]struct {
		o       ldapMMROptions
		search  func(b, f string) ([]ldapEntry, error)
		out     string
		code    int
		mods    []string
		adds    int
		notMast bool
	}{
		"first run": {
			ldapMMROptions{SID: 7, RID: 100, URI: uri}, nil, "", 0,
			[]string{"cn=config|add|olcServerID|7", ldapMMRDB + "|add|olcSyncrepl|rid=100 provider=" + uri,
				ldapMMRDB + "|replace|olcMirrorMode|TRUE"}, 4, false,
		},
		"configured id, mirror mode already on": {
			ldapMMROptions{RID: 101, URI: uri}, ldapMMRSearch([]string{"1"}, nil, true), "", 0,
			[]string{ldapMMRDB + "|add|olcSyncrepl|rid=101 provider=" + uri}, 4, false,
		},
		"no server id": {
			ldapMMROptions{RID: 100, URI: uri}, nil,
			"Error: No Server ID exists on master and none provided.\n" +
				"       Provide valid Server ID value via the -s option.\n", 1, nil, 0, false,
		},
		"server id given twice": {
			ldapMMROptions{SID: 3, RID: 100, URI: uri}, ldapMMRSearch([]string{"1"}, nil, false),
			"Error: Server ID provided when the Server ID is already configured.\n", 1, nil, 0, false,
		},
		"rid in use": {
			ldapMMROptions{RID: 100, URI: uri},
			ldapMMRSearch([]string{"1"}, []string{"rid=099 x=y", "rid=100 provider=z"}, false),
			"Error: A replication agreement for rid 100 already exists.\n", 1, nil, 0, false,
		},
		"rid free": {
			ldapMMROptions{RID: 100, URI: uri},
			ldapMMRSearch([]string{"1"}, []string{"rid=099 x=y", "nonsense"}, false), "", 0,
			[]string{ldapMMRDB + "|add|olcSyncrepl|rid=100 provider=" + uri, ldapMMRDB + "|replace|olcMirrorMode|TRUE"},
			4, false,
		},
		"not a master": {
			ldapMMROptions{SID: 7, RID: 100, URI: uri}, nil,
			"Not a master, please use zmldappromote-replica-mmr\n", 0, nil, 0, true,
		},
	}

	for name, c := range cases {
		e, f := ldapMMREnv(t)
		if c.notMast {
			require.NoError(t, e.SetLC(map[string]string{ldapLCIsMaster: "false"}))
		}

		if c.search != nil {
			f.searchFn = c.search
		}

		var out bytes.Buffer

		err := ldapEnableMMR(e, f.kit(), c.o, &out)

		assert.Equal(t, c.out, out.String(), name)
		assert.Equal(t, c.code, ldapToolStatus(err, &bytes.Buffer{}), name)

		assert.Len(t, f.mods, len(c.mods), name)

		for i, want := range c.mods {
			if i < len(f.mods) {
				assert.True(t, strings.HasPrefix(f.mods[i], want), "%s: %s", name, f.mods[i])
			}
		}

		if c.code == 0 && len(c.mods) > 0 && name != "configured id, mirror mode already on" {
			assert.Len(t, f.adds, c.adds, name)
		}
	}
}

func TestLdapEnableMMR_Failures(t *testing.T) {
	uri := "ldap://other.example.com:389/"
	cases := map[string]struct {
		o    ldapMMROptions
		fail func(f *ldapFake, errw error)
		want string
	}{
		"server id": {
			ldapMMROptions{SID: 7, RID: 100, URI: uri},
			func(f *ldapFake, errw error) {
				f.modifyErr = func(dn string) error { return failOn(dn == "cn=config", errw) }
			},
			"Server ID: nope\n",
		},
		"syncrepl": {
			ldapMMROptions{SID: 7, RID: 100, URI: uri},
			func(f *ldapFake, errw error) {
				f.modifyErr = func(dn string) error { return failOn(dn == ldapMMRDB, errw) }
			},
			"Syncrepl: nope\n",
		},
		"accesslog": {
			ldapMMROptions{SID: 7, RID: 100, URI: uri},
			func(f *ldapFake, errw error) { f.addErr = func(string) error { return errw } },
			"Add Failed: nope\n",
		},
	}

	for name, c := range cases {
		e, f := ldapMMREnv(t)
		c.fail(f, ldapDiag(goldap.LDAPResultOther, "nope"))

		var errb bytes.Buffer

		err := ldapEnableMMR(e, f.kit(), c.o, &bytes.Buffer{})

		assert.Equal(t, 255, ldapToolStatus(err, &errb), name)
		assert.Equal(t, c.want, errb.String(), name)
	}
}

func failOn(cond bool, err error) error {
	if cond {
		return err
	}

	return nil
}

func TestLdapEnableMMR_MirrorModeFailure(t *testing.T) {
	e, f := ldapMMREnv(t)
	f.modifyErr = func(dn string) error { return nil }

	var errb bytes.Buffer

	// the second modify of the main database is the mirror mode
	n := 0
	f.modifyErr = func(dn string) error {
		if dn != ldapMMRDB {
			return nil
		}

		n++

		return failOn(n == 2, ldapDiag(goldap.LDAPResultOther, "nope"))
	}

	err := ldapEnableMMR(e, f.kit(), ldapMMROptions{SID: 1, RID: 100, URI: "ldap://o:389/"}, &bytes.Buffer{})

	assert.Equal(t, 255, ldapToolStatus(err, &errb))
	assert.Equal(t, "Mirror mode: nope\n", errb.String())
}

// ldapenable-mmr.pl 22-61: the options and the usage.
func TestRunLDAPEnableMMR_Options(t *testing.T) {
	cases := map[string]struct {
		args []string
		code int
		mods []string
	}{
		"ok":                {[]string{"-s", "7", "-m", "ldap://o:389/"}, 0, []string{"cn=config|add|olcServerID|7"}},
		"long options":      {[]string{"--sid=7", "--mmr=ldaps://o:636/", "--rid=5"}, 0, []string{"cn=config|add|olcServerID|7"}},
		"abbreviated":       {[]string{"--si", "7", "--m", "ldap://o:389/"}, 0, []string{"cn=config|add|olcServerID|7"}},
		"no uri":            {[]string{"-s", "7"}, 1, nil},
		"uri without slash": {[]string{"-s", "7", "-m", "ldap://o:389"}, 1, nil},
		"not an ldap uri":   {[]string{"-s", "7", "-m", "http://o/"}, 1, nil},
		"help":              {[]string{"-h", "-m", "ldap://o:389/"}, 1, nil},
		"unknown":           {[]string{"-z", "-m", "ldap://o:389/"}, 1, nil},
		"sid not a number":  {[]string{"-s", "x", "-m", "ldap://o:389/"}, 1, nil},
	}

	for name, c := range cases {
		e, f := ldapMMREnv(t)

		var out, errb bytes.Buffer

		assert.Equal(t, c.code, RunLDAPEnableMMR(e, c.args, &out, &errb), name)

		if c.code != 0 {
			assert.Contains(t, errb.String(), "zmldapenable-mmr [-r RID] [-s SID] -m MMRURI", name)
			assert.Empty(t, f.mods, name)

			continue
		}

		require.NotEmpty(t, f.mods, name)
		assert.Equal(t, c.mods[0], f.mods[0], name)
	}

	e, fakes, _ := ldapToolEnv(t)
	require.NoError(t, fakes.FS.RemoveAll(ldapToolRoot+"/schema"))

	var out, errb bytes.Buffer

	assert.Equal(t, 1, RunLDAPEnableMMR(e, []string{"-m", "ldap://o:389/"}, &out, &errb))
	assert.Contains(t, out.String(), "openldap does not appear to be installed")
}
