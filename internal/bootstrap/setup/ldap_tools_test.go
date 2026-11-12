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

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	ldapToolRoot = "/opt/zextras/common/etc/openldap"
	ldapToolLMR  = "ldap://master.example.com:389"
	ldapToolDir  = "/opt/zextras/data/ldap/config/cn=config/cn=schema"
)

// ldapSchemaSource is a schema file of the package for the schema called name.
func ldapSchemaSource(name string) string {
	return "dn: cn=" + name + ",cn=schema,cn=config\nobjectClass: olcSchemaConfig\ncn: " + name +
		"\nolcAttributeTypes: ( 1.2.3 NAME 'cn: " + name + "' )\n"
}

// ldapPutSchemas puts the openldap schemas of the package on the fake file system.
func ldapPutSchemas(fakes *systest.Fakes) {
	fakes.FS.PutDir(ldapToolRoot+"/schema", 0o755)

	for _, s := range ldapSchemaFiles {
		fakes.FS.Put(ldapToolRoot+"/"+s.source, []byte(ldapSchemaSource(s.schema)), 0o644)
	}
}

// ldapToolEnv is a node with openldap installed whose LDAP connections go to
// the fake: the host is ldap1.example.com, the master is master.example.com.
func ldapToolEnv(t *testing.T) (*Env, *systest.Fakes, *ldapFake) {
	t.Helper()

	e, fakes, f := ldapTestEnv(t)
	e.kit = f.kit()
	ldapPutSchemas(fakes)

	require.NoError(t, e.SetLC(map[string]string{
		ldapLCMasterURL: ldapToolLMR, "zimbra_server_hostname": ldapTestHost, ldapLCRoot: ldapTestRoot,
		ldapLCAdmin: ldapTestAdmin, "zimbra_ldap_userdn": "uid=zimbra,cn=admins,cn=zimbra",
		"ldap_replication_password": ldapTestRep, "ldap_port": "389", ldapLCURL: ldapToolLMR,
	}))

	return e, fakes, f
}

func ldapDiag(code uint16, msg string) error { return goldap.NewError(code, errors.New(msg)) }

// TestLdapSchema_InstallsRenamedSchemasInTheOrderOfTheScript follows
// libexec/ldapschema.sh: the messages (lines 18-97), the "{N}" that the two
// sed lines (49-97) put in the dn and cn lines, the copy of every schema to
// cn=schema as zextras with mode 600 and the cleanup of the sed backups (99).
func TestLdapSchema_InstallsRenamedSchemasInTheOrderOfTheScript(t *testing.T) {
	e, fakes, _ := ldapToolEnv(t)
	dir := ldapToolDir
	fakes.FS.Put(dir+"/core.ldif.orig", []byte("old"), 0o600)

	var out bytes.Buffer

	require.NoError(t, ldapInstallSchema(e, &out))

	assert.Equal(t, "Looking for LDAP installation...succeeded\n"+
		"Installing core schema...\nInstalling cosine schema...\nInstalling inetOrgPerson schema...\n"+
		"Installing zimbra schema...\nInstalling amavis schema...\nInstalling dyngroup schema...\n"+
		"Installing OpenDKIM schema...\n", out.String())

	for _, c := range []struct{ file, name, schema string }{
		{"cn={0}core.ldif", "core", "{0}core"}, {"cn={1}cosine.ldif", "cosine", "{1}cosine"},
		{"cn={2}inetorgperson.ldif", "inetorgperson", "{2}inetorgperson"},
		{"cn={3}dyngroup.ldif", "dyngroup", "{3}dyngroup"},
		{"cn={4}carbonio.ldif", "zimbra", "{4}zimbra"}, {"cn={5}amavisd.ldif", "amavisd", "{5}amavisd"},
		{"cn={6}opendkim.ldif", "opendkim", "{6}opendkim"},
	} {
		data, ok := fakes.FS.Data(dir + "/" + c.file)
		require.True(t, ok, c.file)

		want := "dn: cn=" + c.schema + "\nobjectClass: olcSchemaConfig\ncn: " + c.schema +
			"\nolcAttributeTypes: ( 1.2.3 NAME 'cn: " + c.name + "' )\n"
		assert.Equal(t, want, string(data), "dn and cn lines are renamed, the others are not: %s", c.file)

		mode, _ := fakes.FS.Mode(dir + "/" + c.file)
		assert.Equal(t, "-rw-------", mode.String(), c.file)
		assert.Contains(t, ldapAllLines(fakes), "chown zextras:zextras "+dir+"/"+c.file)
	}

	assert.False(t, fakes.FS.Exists(dir+"/core.ldif.orig"), "ldapschema.sh removes the sed backups")
	assert.Contains(t, ldapAllLines(fakes), "chown zextras:zextras "+dir+"\n")
	mode, _ := fakes.FS.Mode(dir)
	assert.Equal(t, "drwxr-x---", mode.String(), "the directory is 750")
}

func TestLdapSchema_RenamingTouchesOnlyTheLinesOfTheSchema(t *testing.T) {
	f := ldapSchemaFile{index: 4, schema: "zimbra"}

	cases := map[string]struct{ in, want string }{
		"dn line":                   {"dn: cn=zimbra,cn=schema,cn=config\n", "dn: cn={4}zimbra\n"},
		"dn of another schema":      {"dn: cn=core,cn=schema,cn=config\n", "dn: cn=core,cn=schema,cn=config\n"},
		"dn with other suffix":      {"dn: cn=zimbra,ou=x\n", "dn: cn=zimbra,ou=x\n"},
		"cn line":                   {"cn: zimbra\n", "cn: {4}zimbra\n"},
		"cn line is a prefix match": {"cn: zimbra2\n", "cn: {4}zimbra2\n"},
		"indented cn is not":        {" cn: zimbra\n", " cn: zimbra\n"},
		"only the first match":      {"cn: zimbra cn: zimbra\n", "cn: {4}zimbra cn: zimbra\n"},
		"no final newline":          {"cn: zimbra", "cn: {4}zimbra"},
	}

	for name, c := range cases {
		assert.Equal(t, c.want, string(ldapRenameSchema([]byte(c.in), f)), name)
	}
}

func TestLdapSchema_OpenLDAPMissing(t *testing.T) {
	e, fakes, _ := ldapToolEnv(t)
	require.NoError(t, fakes.FS.RemoveAll(ldapToolRoot+"/schema"))

	var out, errb bytes.Buffer

	// ldapschema.sh lines 15-20
	assert.Equal(t, 1, RunLDAPSchema(e, []string{"ignored"}, &out, &errb))
	assert.Equal(t, "Looking for LDAP installation...FAILED\nERROR: openldap does not appear to be installed - exiting\n",
		out.String())
	assert.Empty(t, errb.String())
}

func TestLdapSchema_MissingSourceFails(t *testing.T) {
	e, fakes, _ := ldapToolEnv(t)
	require.NoError(t, fakes.FS.Remove(ldapToolRoot+"/schema/cosine.ldif"))

	var out, errb bytes.Buffer

	assert.Equal(t, 1, RunLDAPSchema(e, nil, &out, &errb))
	assert.Contains(t, errb.String(), "cosine.ldif")
	assert.Contains(t, out.String(), "Installing cosine schema...\n")
	assert.NotContains(t, out.String(), "inetOrgPerson", "the run ends at the failure")
}

func TestLdapSchema_RunSucceeds(t *testing.T) {
	e, _, _ := ldapToolEnv(t)

	var out, errb bytes.Buffer

	assert.Equal(t, 0, RunLDAPSchema(e, nil, &out, &errb))
	assert.Empty(t, errb.String())
}

// ldapUpdateEnv is a master with the LDIF files of libexec/ldapupdateldif.pl.
func ldapUpdateEnv(t *testing.T) (*Env, *systest.Fakes, *ldapFake) {
	t.Helper()

	e, fakes, f := ldapToolEnv(t)
	require.NoError(t, e.SetLC(map[string]string{
		ldapLCIsMaster: "TRUE", "zimbra_tmp_directory": "/opt/zextras/data/tmp",
	}))

	return e, fakes, f
}

var ldapTemplateDNs = []string{
	"uid=zimbra,cn=admins,cn=zimbra", "uid=zmreplica,cn=admins,cn=zimbra", "cn=zimbra_globalconfig,cn=zimbra",
	"cn=zimbra_defaultcos,cn=zimbra", "cn=zimbra_defaultexternalcos,cn=zimbra", "cn=mimehandlers,cn=zimbra",
}

// TestLdapUpdateLDIF_AddsTheEntriesTheDirectoryLacks follows
// libexec/ldapupdateldif.pl: nothing unless ldap_is_master is true (40-42),
// the tmp directory (44-47), the connection and bind (49-76) and the adds of
// the five LDIF files (78-130); entries that exist are left alone.
func TestLdapUpdateLDIF_AddsTheEntriesTheDirectoryLacks(t *testing.T) {
	e, fakes, f := ldapUpdateEnv(t)
	require.NoError(t, e.SetLC(map[string]string{"ldap_starttls_supported": "1"}))

	f.bindOK = func(dn, pw string) bool { return dn == ldapConfigDN && pw == ldapTestRoot }

	var out bytes.Buffer

	require.NoError(t, ldapUpdateLDIF(e, f.kit(), &out))

	assert.Equal(t, ldapTemplateDNs, f.adds)
	assert.Empty(t, out.String(), "the script is silent")
	assert.Equal(t, []string{"start", "bind cn=config"}, f.events)
	assert.Equal(t, []ldapDial{{URLs: []string{ldapToolLMR}, TLS: ldapTLSInsecure}}, f.dials,
		"StartTLS without verification as ldap_starttls_supported is set")
	assert.True(t, fakes.FS.Exists("/opt/zextras/data/tmp"))
	assert.Contains(t, ldapAllLines(fakes), "chown -R zextras:zextras /opt/zextras/data/tmp")
}

func TestLdapUpdateLDIF_NotAMasterDoesNothing(t *testing.T) {
	for _, v := range []string{"false", "", "yes"} {
		e, fakes, f := ldapUpdateEnv(t)
		require.NoError(t, e.SetLC(map[string]string{ldapLCIsMaster: v}))

		var out, errb bytes.Buffer

		assert.Equal(t, 0, RunLDAPUpdateLDIF(e, nil, &out, &errb), v)
		assert.Empty(t, out.String()+errb.String(), v)
		assert.Empty(t, f.events, v)
		assert.Empty(t, f.dials, v)
		assert.Empty(t, fakes.Exec.Lines(), v)
	}
}

func TestLdapUpdateLDIF_ConnectionDetails(t *testing.T) {
	cases := map[string]struct {
		lc   map[string]string
		want ldapDial
	}{
		// ldapupdateldif.pl 55-71: the masters of ldap_master_url, start_tls
		// unless ldaps.
		"several masters": {
			map[string]string{ldapLCMasterURL: "ldap://a:389 ldap://b:389"},
			ldapDial{URLs: []string{"ldap://a:389", "ldap://b:389"}},
		},
		"starttls": {
			map[string]string{"ldap_starttls_supported": "true"},
			ldapDial{URLs: []string{ldapToolLMR}, TLS: ldapTLSInsecure},
		},
		"ldaps needs no starttls": {
			map[string]string{ldapLCMasterURL: "ldaps://a:636", "ldap_starttls_supported": "true"},
			ldapDial{URLs: []string{"ldaps://a:636"}},
		},
	}

	for name, c := range cases {
		e, _, f := ldapUpdateEnv(t)
		require.NoError(t, e.SetLC(c.lc))

		require.NoError(t, ldapUpdateLDIF(e, f.kit(), &bytes.Buffer{}), name)
		assert.Equal(t, []ldapDial{c.want}, f.dials, name)
	}
}

func TestLdapUpdateLDIF_MimeHandlersOfTheConfigurationOverrideTheTemplate(t *testing.T) {
	e, fakes, f := ldapUpdateEnv(t)
	fakes.FS.Put("/opt/zextras/conf/ldap/mimehandlers.ldif", []byte("dn: cn=custom,cn=zimbra\ncn: x\n"), 0o644)

	require.NoError(t, ldapUpdateLDIF(e, f.kit(), &bytes.Buffer{}))

	assert.Equal(t, "cn=custom,cn=zimbra", f.adds[len(f.adds)-1])
	assert.NotContains(t, f.adds, "cn=mimehandlers,cn=zimbra")
}

func TestLdapUpdateLDIF_ExistingEntriesAndFailedAddsAreNotReported(t *testing.T) {
	e, _, f := ldapUpdateEnv(t)
	f.addErr = func(dn string) error {
		if strings.Contains(dn, "cos") {
			return ldapDiag(goldap.LDAPResultEntryAlreadyExists, "exists")
		}

		return ldapDiag(goldap.LDAPResultInsufficientAccessRights, "no")
	}

	var out bytes.Buffer

	require.NoError(t, ldapUpdateLDIF(e, f.kit(), &out))
	assert.Len(t, f.adds, 6, "a failed add does not stop the next ones")
	assert.Empty(t, out.String())
}

// ldapupdateldif.pl 132-187: the *.ldif files of lib/conf replace the
// attributes of their entries.
func TestLdapUpdateLDIF_LibConfReplacesAttributes(t *testing.T) {
	e, fakes, f := ldapUpdateEnv(t)
	dir := "/opt/zextras/lib/conf"
	fakes.FS.PutDir(dir, 0o755)
	fakes.FS.Put(dir+"/b.ldif", []byte("dn: cn=b,cn=zimbra\nzimbraFoo: 1\nzimbraFoo: 2\nzimbraBar: x\n"), 0o644)
	fakes.FS.Put(dir+"/a.ldif", []byte("dn: cn=a,cn=zimbra\nzimbraBaz: y\n"), 0o644)
	fakes.FS.Put(dir+"/notes.txt", []byte("dn: cn=n,cn=zimbra\nx: y\n"), 0o644)
	fakes.FS.PutDir(dir+"/sub.ldif", 0o755)

	var out bytes.Buffer

	require.NoError(t, ldapUpdateLDIF(e, f.kit(), &out))

	assert.Equal(t, []string{
		"cn=a,cn=zimbra|replace|zimbraBaz|y",
		"cn=b,cn=zimbra|replace|zimbraFoo|1,2",
		"cn=b,cn=zimbra|replace|zimbraBar|x",
	}, f.mods, "in the order of the file names, one replace per attribute")
	assert.Empty(t, out.String())
}

func TestLdapUpdateLDIF_LibConfFailuresArePrinted(t *testing.T) {
	e, fakes, f := ldapUpdateEnv(t)
	dir := "/opt/zextras/lib/conf"
	fakes.FS.PutDir(dir, 0o755)
	fakes.FS.Put(dir+"/a.ldif", []byte("dn: cn=a,cn=zimbra\nzimbraBaz: y\n"), 0o644)
	fakes.FS.Put(dir+"/b.ldif", []byte("not an ldif entry\n"), 0o644)
	f.modifyErr = func(string) error { return ldapDiag(goldap.LDAPResultNoSuchObject, "No such object") }

	var out bytes.Buffer

	require.NoError(t, ldapUpdateLDIF(e, f.kit(), &out))

	assert.Contains(t, out.String(), "Error msg: cn=a,cn=zimbra No such object\n")
	assert.Contains(t, out.String(), "Error lines:", "an LDIF that does not parse is reported too")
}

func TestLdapUpdateLDIF_Failures(t *testing.T) {
	cases := map[string]struct {
		setup func(f *ldapFake, e *Env)
		want  string
	}{
		"connection": {
			func(_ *ldapFake, e *Env) {
				e.kit.dial = func(*Env, ldapDial) (ldapConn, error) { return nil, errors.New("Can't connect") }
			},
			"Can't connect",
		},
		"bind": {
			func(f *ldapFake, _ *Env) { f.bindOK = func(string, string) bool { return false } },
			"Bind: invalid credentials",
		},
	}

	for name, c := range cases {
		e, _, f := ldapUpdateEnv(t)
		c.setup(f, e)

		var out, errb bytes.Buffer

		assert.Equal(t, 255, RunLDAPUpdateLDIF(e, nil, &out, &errb), name)
		assert.Equal(t, c.want+"\n", errb.String(), name)
	}
}

func TestLdapUpdateLDIF_StartFailureIsOnlyLogged(t *testing.T) {
	e, _, f := ldapUpdateEnv(t)
	f.startErr = errors.New("cannot start")

	require.NoError(t, ldapUpdateLDIF(e, f.kit(), &bytes.Buffer{}))
	assert.Len(t, f.adds, 6, "ldapupdateldif.pl ignored the result of the start")
}

func TestLdapUpdateLDIF_MissingFileFails(t *testing.T) {
	e, fakes, _ := ldapUpdateEnv(t)
	require.NoError(t, fakes.FS.Remove(ldapToolRoot+"/zimbra/zimbra_defaultcos.ldif"))

	var out, errb bytes.Buffer

	assert.Equal(t, 1, RunLDAPUpdateLDIF(e, nil, &out, &errb))
	assert.Contains(t, errb.String(), "zimbra_defaultcos.ldif")
}
