// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/alecthomas/kong"
	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cli/ldapcmd"
	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// directory is an in-memory LDAP server for the "ldap" verbs.
type directory struct {
	global, server map[string]string
	entries        []*goldap.Entry

	gotServer, gotBase, gotFilter string
	gotAttrs                      []string
	gotScope                      int
	gotAdd, gotDel, gotReplace    map[string][]string
}

func (d *directory) GetGlobalConfig() (map[string]string, error) { return d.global, nil }

func (d *directory) GetServerConfig(host string) (map[string]string, error) {
	d.gotServer = host

	return d.server, nil
}

func (d *directory) Search(base, filter string, attrs []string, scope int) (*goldap.SearchResult, error) {
	d.gotBase, d.gotFilter, d.gotAttrs, d.gotScope = base, filter, attrs, scope

	return &goldap.SearchResult{Entries: d.entries}, nil
}

func (d *directory) ModifyValues(_ string, add, del, replace map[string][]string) error {
	d.gotAdd, d.gotDel, d.gotReplace = add, del, replace

	return nil
}

func (d *directory) Close() error { return nil }

// runLDAPCLI parses "configd ldap <argv>" against a fake directory and runs
// it, returning what the verb printed and the parameters the connection got.
func runLDAPCLI(t *testing.T, dir *directory, argv ...string) (string, *carboldap.ClientConfig, error) {
	t.Helper()

	return runLDAPCLIOn(t, systest.New().System(), dir, argv...)
}

func runLDAPCLIOn(t *testing.T, env sys.System, dir *directory, argv ...string) (string, *carboldap.ClientConfig, error) {
	t.Helper()

	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	for _, key := range []string{"ldap_url", "ldap_master_url", "zimbra_ldap_userdn", "zimbra_ldap_password", "zimbra_server_hostname", "ldap_starttls_supported"} {
		t.Setenv("CONFIGD_LC_"+key, "")
		require.NoError(t, os.Unsetenv("CONFIGD_LC_"+key))
	}

	cli := &CLI{}

	parser, err := kong.New(cli, kong.Vars{"version": "test"})
	require.NoError(t, err)

	kctx, err := parser.Parse(append([]string{"ldap"}, argv...))
	require.NoError(t, err)

	var (
		out    bytes.Buffer
		dialed *carboldap.ClientConfig
	)

	cli.LDAP.dial = func(cfg *carboldap.ClientConfig) (ldapcmd.Conn, error) {
		dialed = cfg

		return dir, nil
	}

	err = kctx.Run(env, &stdio{Out: &out, Err: io.Discard})

	return out.String(), dialed, err
}

func TestLDAPCLI_GacfUsesConnectionFlags(t *testing.T) {
	const pw = "/etc/configd/ldap.pw"

	dir := &directory{global: map[string]string{"zimbraMailMode": "https", "cn": "config"}}

	f := systest.New()
	f.FS.Put(pw, []byte("s3cret\n"), 0o600)

	out, dialed, err := runLDAPCLIOn(t, f.System(), dir, "--url", "ldap://a:389", "--url", "ldap://b:389",
		"--bind-dn", "uid=admin", "--password-file", pw, "gacf")

	require.NoError(t, err)
	assert.Equal(t, "cn: config\nzimbraMailMode: https\n", out)
	assert.Equal(t, []string{"ldap://a:389", "ldap://b:389"}, dialed.URLs, "repeated --url builds the failover list")
	assert.Equal(t, "uid=admin", dialed.BindDN)
	assert.Equal(t, "s3cret", dialed.Password)
}

func TestLDAPCLI_GcfPrintsOnlyTheNamedAttributesAsJSON(t *testing.T) {
	dir := &directory{global: map[string]string{"zimbraMailMode": "https", "cn": "config"}}

	out, _, err := runLDAPCLI(t, dir, "--url", "ldap://a:389", "--json", "gcf", "cn")

	require.NoError(t, err)
	assert.JSONEq(t, `{"cn":["config"]}`, out)
}

func TestLDAPCLI_GsNamesTheServer(t *testing.T) {
	dir := &directory{server: map[string]string{"zimbraServiceEnabled": "mailbox"}}

	out, _, err := runLDAPCLI(t, dir, "--url", "ldap://a:389", "gs", "mbox.example")

	require.NoError(t, err)
	assert.Equal(t, "mbox.example", dir.gotServer)
	assert.Equal(t, "zimbraServiceEnabled: mailbox\n", out)
}

func TestLDAPCLI_McfDropsTheHabitualDoubleDash(t *testing.T) {
	dir := &directory{}

	_, _, err := runLDAPCLI(t, dir, "--url", "ldap://a:389", "mcf", "--", "+zimbraMtaRestriction", "x", "-zimbraGone", "y", "zimbraMailMode", "https")

	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"zimbraMtaRestriction": {"x"}}, dir.gotAdd)
	assert.Equal(t, map[string][]string{"zimbraGone": {"y"}}, dir.gotDel)
	assert.Equal(t, map[string][]string{"zimbraMailMode": {"https"}}, dir.gotReplace)
}

func TestLDAPCLI_SearchPassesBaseFilterScopeAndAttrs(t *testing.T) {
	dir := &directory{entries: []*goldap.Entry{goldap.NewEntry("cn=a,cn=zimbra", map[string][]string{"cn": {"a"}})}}

	out, _, err := runLDAPCLI(t, dir, "--url", "ldap://a:389", "search",
		"--base", "cn=servers,cn=zimbra", "--filter", "(cn=a)", "--scope", "one", "--attr", "cn", "--attr", "mail")

	require.NoError(t, err)
	assert.Equal(t, "cn=servers,cn=zimbra", dir.gotBase)
	assert.Equal(t, "(cn=a)", dir.gotFilter)
	assert.Equal(t, goldap.ScopeSingleLevel, dir.gotScope)
	assert.Equal(t, []string{"cn", "mail"}, dir.gotAttrs)
	assert.Equal(t, "dn: cn=a,cn=zimbra\ncn: a\n", out)
}

func TestLDAPCLI_NoURLExitsWithStatusOne(t *testing.T) {
	_, dialed, err := runLDAPCLI(t, &directory{}, "gacf")

	assert.ErrorIs(t, err, exitStatus(1))
	assert.Nil(t, dialed, "no connection is attempted without a URL")
}
