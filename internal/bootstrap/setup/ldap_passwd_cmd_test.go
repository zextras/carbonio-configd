// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const ldapPasswdTestLDIF = ldapTestCarbonioLDIF + `
dn: uid=zmpostfix,cn=appaccts,cn=zimbra
objectClass: organizationalPerson
uid: zmpostfix
userPassword: {SSHA}oldpost

dn: uid=zmbes-searcher,cn=appaccts,cn=zimbra
objectClass: organizationalPerson
uid: zmbes-searcher
userPassword: {SSHA}oldbes
`

func TestParseLDAPPasswdArgs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		account  ldapAccount
		accounts int
		help     bool
		confirm  bool
		password string
		hasPw    bool
		extra    []string
		stderr   string
		ok       bool
	}{
		{name: "the admin account", args: []string{"pw"}, account: ldapAcctAdmin, password: "pw", hasPw: true, ok: true},
		{name: "no password", args: []string{"-p"}, account: ldapAcctPostfix, accounts: 1, ok: true},
		{name: "bundled", args: []string{"-cl", "pw"}, account: ldapAcctReplica, accounts: 1, confirm: true,
			password: "pw", hasPw: true, ok: true},
		{name: "separate", args: []string{"-c", "-l", "pw"}, account: ldapAcctReplica, accounts: 1, confirm: true,
			password: "pw", hasPw: true, ok: true},
		{name: "every account", args: []string{"-r"}, account: ldapAcctRoot, accounts: 1, ok: true},
		{name: "amavis", args: []string{"-a", "pw"}, account: ldapAcctAmavis, accounts: 1, password: "pw",
			hasPw: true, ok: true},
		{name: "nginx", args: []string{"-n"}, account: ldapAcctNginx, accounts: 1, ok: true},
		{name: "bes searcher", args: []string{"-b"}, account: ldapAcctBES, accounts: 1, ok: true},
		{name: "two accounts", args: []string{"-p", "-n"}, account: ldapAcctNginx, accounts: 2, ok: true},
		{name: "help", args: []string{"-h"}, account: ldapAcctAdmin, help: true, ok: true},
		{name: "-- ends the options", args: []string{"--", "-pw"}, account: ldapAcctAdmin, password: "-pw",
			hasPw: true, ok: true},
		{name: "the first argument ends the options", args: []string{"pw", "-p"}, account: ldapAcctAdmin,
			password: "pw", hasPw: true, extra: []string{"-p"}, ok: true},
		{name: "a lone dash is a password", args: []string{"-"}, account: ldapAcctAdmin, password: "-",
			hasPw: true, ok: true},
		{name: "unknown option", args: []string{"-x", "-p"}, account: ldapAcctPostfix, accounts: 1,
			stderr: "Unknown option: x\n"},
		{name: "options are case sensitive", args: []string{"-P"}, account: ldapAcctAdmin, stderr: "Unknown option: P\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer

			o, ok := parseLDAPPasswdArgs(tc.args, &stderr)

			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.stderr, stderr.String())
			assert.Equal(t, tc.account.Name, o.account.Name)
			assert.Equal(t, tc.accounts, o.accounts)
			assert.Equal(t, tc.help, o.help)
			assert.Equal(t, tc.confirm, o.confirm)
			assert.Equal(t, tc.password, o.password)
			assert.Equal(t, tc.hasPw, o.hasPassword)
			assert.ElementsMatch(t, tc.extra, o.extra)
		})
	}
}

// ldapPasswdTestEnv is a node whose directory is the fake f.
func ldapPasswdTestEnv(t *testing.T, isMaster string) (*Env, *systest.Fakes, *ldapFake) {
	t.Helper()

	e, fakes, f := ldapTestEnv(t)
	fakes.FS.Put("/opt/zextras/conf/carbonio.ldif", []byte(ldapPasswdTestLDIF), 0o640)
	require.NoError(t, e.SetLC(map[string]string{
		ldapLCMasterURL:           "ldap://" + ldapTestHost + ":389",
		"ldap_is_master":          isMaster,
		"ldap_root_password":      "oldroot",
		"zimbra_ldap_userdn":      "uid=zimbra,cn=admins,cn=zimbra",
		"ldap_starttls_supported": "0",
	}))

	return e, fakes, f
}

type ldapPasswdRun struct {
	code   int
	stdout string
	stderr string
}

func ldapPasswdExec(t *testing.T, e *Env, f *ldapFake, stdin string, args ...string) ldapPasswdRun {
	t.Helper()

	var out, errb bytes.Buffer

	code := runLDAPPasswd(e, f.kit(), args, strings.NewReader(stdin), &out, &errb)

	return ldapPasswdRun{code, out.String(), errb.String()}
}

const ldapPasswdUsageHead = "Usage: /opt/zextras/bin/zmldappasswd [-h] [-r] [-p] [[-c]-l] [newpassword]\n"

func TestRunLDAPPasswd_Accounts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		lcKey   string
		dn      string
		hashDN  string
		ldifOld string
	}{
		{"admin", nil, "zimbra_ldap_password", "uid=zimbra,cn=admins,cn=zimbra", "uid=zimbra,cn=admins,cn=zimbra",
			"userPassword: {SSHA}old\n"},
		{"postfix", []string{"-p"}, "ldap_postfix_password", ldapDNPostfix, ldapDNPostfix, "userPassword: {SSHA}oldpost\n"},
		{"bes-searcher", []string{"-b"}, "ldap_bes_searcher_password", ldapDNBES, ldapDNBES, "userPassword: {SSHA}oldbes\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, fakes, f := ldapPasswdTestEnv(t, "true")
			f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN && pw == "oldroot" }

			r := ldapPasswdExec(t, e, f, "", append(tc.args, "New-Secret")...)

			assert.Equal(t, ldapPasswdRun{0, "Updating local config and LDAP\n", ""}, r)
			assert.Equal(t, []string{"bind " + tc.dn, "bind cn=config"}, f.events,
				"the account is tried first, then root authorises the change")
			assert.Equal(t, []string{tc.dn}, f.setPw)

			lc, err := e.LC()
			require.NoError(t, err)
			assert.Equal(t, "New-Secret", lc[tc.lcKey])

			ldif, _ := fakes.FS.Data("/opt/zextras/conf/carbonio.ldif")
			assert.NotContains(t, string(ldif), tc.ldifOld, "the hash is recorded")
			assert.NotContains(t, string(ldif), "New-Secret")
			assert.NotContains(t, ldapAllLines(fakes), "New-Secret", "never on a command line")
		})
	}
}

func TestRunLDAPPasswd_RootGoesOverTheSocket(t *testing.T) {
	e, _, f := ldapPasswdTestEnv(t, "true")
	f.bindOK = func(_, pw string) bool { return pw == "oldroot" }

	r := ldapPasswdExec(t, e, f, "", "-r", "NewRoot")

	assert.Equal(t, ldapPasswdRun{0, "Updating local config and LDAP\n", ""}, r)
	assert.Equal(t, []ldapDial{{URLs: []string{ldapSocketURL}}}, f.dials)
	require.Len(t, f.replace, 1)
	assert.Contains(t, f.replace[0], "olcRootPW|{SSHA512}")

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "NewRoot", lc["ldap_root_password"])
}

// localconfig keeps the root password the directory still has when the
// directory cannot be reached or refuses the change.
func TestRunLDAPPasswd_RootFailureLeavesLocalConfigAlone(t *testing.T) {
	t.Run("no connection", func(t *testing.T) {
		e, _, f := ldapPasswdTestEnv(t, "true")
		k := f.kit()
		k.dial = func(*Env, ldapDial) (ldapConn, error) { return nil, errors.New("refused") }

		var out, errb bytes.Buffer

		assert.Equal(t, 1, runLDAPPasswd(e, k, []string{"-r", "NewRoot"}, strings.NewReader(""), &out, &errb))

		lc, err := e.LC()
		require.NoError(t, err)
		assert.Equal(t, "oldroot", lc["ldap_root_password"])
	})

	t.Run("olcRootPW refused", func(t *testing.T) {
		e, _, f := ldapPasswdTestEnv(t, "true")
		f.bindOK = func(_, pw string) bool { return pw == "oldroot" }
		f.replaceErr = errors.New("insufficient access")

		r := ldapPasswdExec(t, e, f, "", "-r", "NewRoot")

		assert.Equal(t, 1, r.code)

		lc, err := e.LC()
		require.NoError(t, err)
		assert.Equal(t, "oldroot", lc["ldap_root_password"])
	})
}

// The password of a command line without one is the first line of stdin.
func TestRunLDAPPasswd_PasswordFromStdin(t *testing.T) {
	for _, tc := range []struct{ name, stdin, want string }{
		{"line", "Std-In-Pw\n", "Std-In-Pw"},
		{"CRLF", "Std-In-Pw\r\n", "Std-In-Pw"},
		{"no line end", "Std-In-Pw", "Std-In-Pw"},
		{"only the first line", "Std-In-Pw\nsecond\n", "Std-In-Pw"},
		{"spaces inside are kept", "a  b\n", "a  b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, fakes, f := ldapPasswdTestEnv(t, "true")
			f.bindOK = func(dn, _ string) bool { return dn == ldapConfigDN }

			r := ldapPasswdExec(t, e, f, tc.stdin, "-p")

			assert.Equal(t, 0, r.code, r.stderr)

			lc, err := e.LC()
			require.NoError(t, err)
			assert.Equal(t, tc.want, lc["ldap_postfix_password"])
			assert.NotContains(t, ldapAllLines(fakes), tc.want)
		})
	}

	t.Run("the argument wins and stdin is left alone", func(t *testing.T) {
		e, _, f := ldapPasswdTestEnv(t, "true")
		stdin := iotest.ErrReader(errors.New("must not be read"))

		var out, errb bytes.Buffer

		code := runLDAPPasswd(e, f.kit(), []string{"-n", "Arg-Pw"}, stdin, &out, &errb)

		assert.Equal(t, 0, code, errb.String())
	})
}

func TestRunLDAPPasswd_RefusedCommandLines(t *testing.T) {
	for _, tc := range []struct {
		name     string
		isMaster string
		stdin    string
		args     []string
	}{
		{"-h", "true", "", []string{"-h"}},
		{"-h with a password", "true", "", []string{"-h", "pw"}},
		{"two accounts", "true", "", []string{"-p", "-n", "pw"}},
		{"root and replication", "true", "", []string{"-r", "-l", "pw"}},
		{"-c without -l", "false", "", []string{"-c", "pw"}},
		{"-c with another account", "false", "", []string{"-c", "-p", "pw"}},
		{"-c -l on the master", "true", "", []string{"-c", "-l", "pw"}},
		{"-c -l on the master, any case", "TRUE", "", []string{"-c", "-l", "pw"}},
		{"no password at all", "true", "", nil},
		{"an empty line", "true", "\n", []string{"-p"}},
		{"a password with spaces split by the shell", "true", "", []string{"-p", "a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, f := ldapPasswdTestEnv(t, tc.isMaster)

			before, err := e.LC()
			require.NoError(t, err)

			unchanged := before["ldap_postfix_password"]

			r := ldapPasswdExec(t, e, f, tc.stdin, tc.args...)

			assert.Equal(t, 1, r.code)
			assert.True(t, strings.HasPrefix(r.stdout, ldapPasswdUsageHead), r.stdout)
			assert.Contains(t, r.stdout, "\t-b: change ldap_bes_searcher_password\n")
			assert.Empty(t, r.stderr)
			assert.Empty(t, f.dials, "nothing is changed")

			lc, err := e.LC()
			require.NoError(t, err)
			assert.Equal(t, unchanged, lc["ldap_postfix_password"])
		})
	}
}

func TestRunLDAPPasswd_UnknownOptionIs255(t *testing.T) {
	e, _, f := ldapPasswdTestEnv(t, "true")

	r := ldapPasswdExec(t, e, f, "", "-x", "pw")

	assert.Equal(t, ldapPasswdRun{255, "", "Unknown option: x\nUnable to set options\n"}, r)
	assert.Empty(t, f.dials)
}

// On a replica, after the master changed it, -c -l brings the replication
// password in the replica's localconfig and syncrepl line up to date.
func TestRunLDAPPasswd_ReplicaFollowsTheMaster(t *testing.T) {
	e, fakes, f := ldapPasswdTestEnv(t, "false")
	f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN && pw == "oldroot" }
	f.search = []ldapEntry{{DN: ldapDNMdbDB, Attrs: map[string][]string{
		"olcSyncrepl": {"{0}rid=100 provider=ldap://m credentials=OLDPW filter=(objectclass=*)"},
	}}}

	ldifBefore, _ := fakes.FS.Data("/opt/zextras/conf/carbonio.ldif")

	r := ldapPasswdExec(t, e, f, "", "-c", "-l", "Repl-New")

	assert.Equal(t, ldapPasswdRun{0, "Updating local config and LDAP\n", ""}, r)
	assert.Equal(t, []ldapDial{{URLs: []string{ldapSocketURL}}}, f.dials, "the replica's own directory, not the master")
	assert.Equal(t, []string{"bind " + ldapDNReplica, "bind cn=config"}, f.events)
	assert.Empty(t, f.setPw, "the directory has the account from the master already")
	assert.Equal(t, []string{ldapDNMdbDB + "|olcSyncrepl|{0}rid=100 provider=ldap://m credentials=Repl-New " +
		"filter=(objectclass=*)"}, f.replace)

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "Repl-New", lc["ldap_replication_password"])

	ldifAfter, _ := fakes.FS.Data("/opt/zextras/conf/carbonio.ldif")
	assert.Equal(t, string(ldifBefore), string(ldifAfter), "carbonio.ldif is the master's business")
}

// Only the credentials of the syncrepl line change: starttls=critical and the
// other parameters stay.
func TestRunLDAPPasswd_SyncreplKeepsTheOtherParameters(t *testing.T) {
	e, _, f := ldapPasswdTestEnv(t, "false")
	f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN && pw == "oldroot" }
	f.search = []ldapEntry{{DN: ldapDNMdbDB, Attrs: map[string][]string{
		"olcSyncrepl": {"{0}rid=100 provider=ldap://m bindmethod=simple binddn=\"uid=zmreplica,cn=admins,cn=zimbra\" " +
			"credentials=OLDPW starttls=critical filter=(objectclass=*) type=refreshAndPersist"},
	}}}

	r := ldapPasswdExec(t, e, f, "", "-c", "-l", "Repl-New")

	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, []string{ldapDNMdbDB + "|olcSyncrepl|{0}rid=100 provider=ldap://m bindmethod=simple " +
		"binddn=\"uid=zmreplica,cn=admins,cn=zimbra\" credentials=Repl-New starttls=critical " +
		"filter=(objectclass=*) type=refreshAndPersist"}, f.replace)
}

// Without -c, -l is the master's change: it sets the password in the
// directory and records the hash in carbonio.ldif as well.
func TestRunLDAPPasswd_ReplicationOnTheMaster(t *testing.T) {
	e, fakes, f := ldapPasswdTestEnv(t, "true")
	f.bindOK = func(dn, _ string) bool { return dn == ldapConfigDN }

	r := ldapPasswdExec(t, e, f, "", "-l", "Repl-New")

	assert.Equal(t, 0, r.code, r.stderr)
	assert.Equal(t, []string{ldapDNReplica}, f.setPw)

	ldif, _ := fakes.FS.Data("/opt/zextras/conf/carbonio.ldif")
	assert.NotContains(t, string(ldif), "{SSHA}oldrep")
}

func TestRunLDAPPasswd_Failures(t *testing.T) {
	t.Run("the directory refuses root", func(t *testing.T) {
		e, fakes, f := ldapPasswdTestEnv(t, "true")
		f.bindOK = func(string, string) bool { return false }

		r := ldapPasswdExec(t, e, f, "", "-p", "Secret-Never-Shown")

		assert.Equal(t, 1, r.code)
		assert.Equal(t, "Updating local config and LDAP\n", r.stdout)
		assert.Contains(t, r.stderr, "Error: binding as cn=config to set the postfix password")
		assert.NotContains(t, r.stdout+r.stderr+ldapAllLines(fakes), "Secret-Never-Shown")
	})

	t.Run("no directory to reach", func(t *testing.T) {
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"-p", "pw"}, "Error: connecting to the LDAP master: refused\n"},
			{[]string{"-c", "-l", "pw"}, "Error: connecting to the LDAP replica: refused\n"},
		} {
			e, _, f := ldapPasswdTestEnv(t, "false")
			k := f.kit()
			k.dial = func(*Env, ldapDial) (ldapConn, error) { return nil, errors.New("refused") }

			var out, errb bytes.Buffer

			assert.Equal(t, 1, runLDAPPasswd(e, k, tc.args, strings.NewReader(""), &out, &errb))
			assert.Equal(t, tc.want, errb.String())
		}
	})

	t.Run("stdin cannot be read", func(t *testing.T) {
		e, _, f := ldapPasswdTestEnv(t, "true")

		r := ldapPasswdExec(t, e, f, "", "-p")
		assert.Equal(t, 1, r.code, "empty stdin is a missing password")

		var out, errb bytes.Buffer

		code := runLDAPPasswd(e, f.kit(), []string{"-p"}, iotest.ErrReader(errors.New("broken pipe")), &out, &errb)

		assert.Equal(t, 1, code)
		assert.Equal(t, "Error: reading the password from stdin: broken pipe\n", errb.String())
	})

	t.Run("no stdin", func(t *testing.T) {
		e, _, f := ldapPasswdTestEnv(t, "true")

		var out, errb bytes.Buffer

		assert.Equal(t, 1, runLDAPPasswd(e, f.kit(), []string{"-p"}, nil, &out, &errb))
		assert.True(t, strings.HasPrefix(out.String(), ldapPasswdUsageHead))
	})

	t.Run("unreadable localconfig", func(t *testing.T) {
		e, _, f := ldapPasswdTestEnv(t, "true")
		e.Paths.LocalConfig = t.TempDir() // a directory: unreadable for root too
		e.lc = nil

		r := ldapPasswdExec(t, e, f, "", "-p", "pw")

		assert.Equal(t, 1, r.code)
		assert.True(t, strings.HasPrefix(r.stderr, "Error: "), r.stderr)
	})
}
