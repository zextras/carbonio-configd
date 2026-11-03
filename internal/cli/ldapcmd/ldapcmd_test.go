// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapcmd

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// fakeConn records what the verbs ask of the directory.
type fakeConn struct {
	global, server map[string]string
	result         *goldap.SearchResult
	err            error

	gotServer                  string
	gotBase, gotFilter         string
	gotAttrs                   []string
	gotScope                   int
	gotDN                      string
	gotAdd, gotDel, gotReplace map[string][]string
	closed                     bool
}

func (f *fakeConn) GetGlobalConfig() (map[string]string, error) { return f.global, f.err }

func (f *fakeConn) GetServerConfig(host string) (map[string]string, error) {
	f.gotServer = host

	return f.server, f.err
}

func (f *fakeConn) Search(base, filter string, attrs []string, scope int) (*goldap.SearchResult, error) {
	f.gotBase, f.gotFilter, f.gotAttrs, f.gotScope = base, filter, attrs, scope

	return f.result, f.err
}

func (f *fakeConn) ModifyValues(dn string, add, del, replace map[string][]string) error {
	f.gotDN, f.gotAdd, f.gotDel, f.gotReplace = dn, add, del, replace

	return f.err
}

func (f *fakeConn) Close() error {
	f.closed = true

	return nil
}

// run executes opts against conn with an empty localconfig and returns the exit
// status, stdout and stderr, plus the connection parameters that were dialed.
func run(t *testing.T, conn *fakeConn, opts *Options) (int, string, string, *carboldap.ClientConfig) {
	t.Helper()

	for _, key := range []string{"ldap_url", "ldap_master_url", "zimbra_ldap_userdn", "zimbra_ldap_password", "zimbra_server_hostname", "ldap_starttls_supported"} {
		t.Setenv("CONFIGD_LC_"+key, "")
	}

	var dialed *carboldap.ClientConfig

	opts.ConfigPath = filepath.Join(t.TempDir(), "absent.xml")
	opts.Dial = func(cfg *carboldap.ClientConfig) (Conn, error) {
		dialed = cfg

		return conn, nil
	}

	if opts.FS == nil {
		opts.FS = systest.New().FS
	}

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, opts)

	return code, stdout.String(), stderr.String(), dialed
}

func TestRun_GacfPrintsSortedZmprovLines(t *testing.T) {
	conn := &fakeConn{global: map[string]string{
		"zimbraMtaRestriction": "reject_a\nreject_b",
		"zimbraMailMode":       "https",
		"carbonioFlag":         "  padded  ",
	}}

	code, stdout, stderr, _ := run(t, conn, &Options{Verb: VerbGacf, URLs: []string{"ldap://x:389"}})

	require.Zero(t, code, stderr)
	assert.Equal(t, "carbonioFlag: padded\nzimbraMailMode: https\nzimbraMtaRestriction: reject_a\nzimbraMtaRestriction: reject_b\n", stdout)
	assert.True(t, conn.closed)
}

func TestRun_GacfJSON(t *testing.T) {
	conn := &fakeConn{global: map[string]string{"zimbraMtaRestriction": "a\nb", "zimbraMailMode": "https"}}

	code, stdout, _, _ := run(t, conn, &Options{Verb: VerbGacf, JSON: true, URLs: []string{"ldap://x:389"}})

	require.Zero(t, code)
	assert.JSONEq(t, `{"zimbraMtaRestriction":["a","b"],"zimbraMailMode":["https"]}`, stdout)
}

func TestRun_GcfFiltersCaseInsensitively(t *testing.T) {
	conn := &fakeConn{global: map[string]string{"zimbraMailMode": "https", "zimbraMtaRestriction": "a"}}

	code, stdout, _, _ := run(t, conn, &Options{Verb: VerbGcf, Args: []string{"zimbramailmode", "absent"}, URLs: []string{"ldap://x:389"}})

	require.Zero(t, code)
	assert.Equal(t, "zimbraMailMode: https\n", stdout)
}

func TestRun_GsDefaultsToLocalHostname(t *testing.T) {
	conn := &fakeConn{server: map[string]string{"zimbraServiceEnabled": "mailbox"}}

	opts := &Options{Verb: VerbGs, URLs: []string{"ldap://x:389"}}

	code, stdout, stderr, _ := runWithHostname(t, conn, opts, "mail.test.example")

	require.Zero(t, code, stderr)
	assert.Equal(t, "mail.test.example", conn.gotServer)
	assert.Equal(t, "zimbraServiceEnabled: mailbox\n", stdout)
}

func runWithHostname(t *testing.T, conn *fakeConn, opts *Options, host string) (int, string, string, *carboldap.ClientConfig) {
	t.Helper()

	t.Setenv("CONFIGD_LC_zimbra_server_hostname", host)

	var dialed *carboldap.ClientConfig

	opts.ConfigPath = filepath.Join(t.TempDir(), "absent.xml")
	opts.FS = systest.New().FS
	opts.Dial = func(cfg *carboldap.ClientConfig) (Conn, error) {
		dialed = cfg

		return conn, nil
	}

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, opts)

	return code, stdout.String(), stderr.String(), dialed
}

func TestRun_GsNamedServerAndMissingServer(t *testing.T) {
	conn := &fakeConn{server: map[string]string{"zimbraServiceHostname": "other"}}

	code, stdout, _, _ := run(t, conn, &Options{Verb: VerbGs, Args: []string{"other.test.example"}, URLs: []string{"ldap://x:389"}})
	require.Zero(t, code)
	assert.Equal(t, "other.test.example", conn.gotServer)
	assert.Equal(t, "zimbraServiceHostname: other\n", stdout)

	code, _, stderr, _ := run(t, &fakeConn{}, &Options{Verb: VerbGs, URLs: []string{"ldap://x:389"}})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "gs needs a server")
}

func TestRun_McfRoutesPrefixes(t *testing.T) {
	conn := &fakeConn{}

	code, stdout, stderr, _ := run(t, conn, &Options{
		Verb: VerbMcf,
		Args: []string{"+zimbraMtaRestriction", "a", "+zimbraMtaRestriction", "b", "-zimbraSSLExcludeCipherSuites", "c", "zimbraMailMode", "https", "-zimbraGone", ""},
		URLs: []string{"ldap://x:389"},
	})

	require.Zero(t, code, stderr)
	assert.Empty(t, stdout)
	assert.Equal(t, "cn=config,cn=zimbra", conn.gotDN)
	assert.Equal(t, map[string][]string{"zimbraMtaRestriction": {"a", "b"}}, conn.gotAdd)
	assert.Equal(t, map[string][]string{"zimbraSSLExcludeCipherSuites": {"c"}, "zimbraGone": nil}, conn.gotDel)
	assert.Equal(t, map[string][]string{"zimbraMailMode": {"https"}}, conn.gotReplace)
}

func TestParseMcf_Errors(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"no args":         nil,
		"odd count":       {"zimbraMailMode"},
		"empty add":       {"+attr", ""},
		"bare plus":       {"+", "v"},
		"bare minus":      {"-", "v"},
		"empty attribute": {"", "v"},
	} {
		_, err := parseMcf(args)
		assert.Error(t, err, name)
	}
}

func TestParseMcf_BareEmptyValueClearsAttribute(t *testing.T) {
	t.Parallel()

	c, err := parseMcf([]string{"zimbraFoo", ""})
	require.NoError(t, err)
	assert.Contains(t, c.replace, "zimbraFoo")
	assert.Empty(t, c.replace["zimbraFoo"])
}

func TestParseMcf_WholeAttributeDeleteWins(t *testing.T) {
	t.Parallel()

	c, err := parseMcf([]string{"-a", "1", "-a", "", "-a", "2"})
	require.NoError(t, err)
	assert.Nil(t, c.del["a"])
	assert.Contains(t, c.del, "a")
}

func TestRun_SearchDefaultsAndOutput(t *testing.T) {
	conn := &fakeConn{result: &goldap.SearchResult{Entries: []*goldap.Entry{
		goldap.NewEntry("cn=a,cn=zimbra", map[string][]string{"cn": {"a"}, "zimbraServiceEnabled": {"mailbox", "mta"}}),
		goldap.NewEntry("cn=b,cn=zimbra", map[string][]string{"cn": {"b"}}),
	}}}

	code, stdout, stderr, _ := run(t, conn, &Options{Verb: VerbSearch, URLs: []string{"ldap://x:389"}})

	require.Zero(t, code, stderr)
	assert.Equal(t, "cn=zimbra", conn.gotBase)
	assert.Equal(t, "(objectClass=*)", conn.gotFilter)
	assert.Equal(t, goldap.ScopeWholeSubtree, conn.gotScope)
	assert.Equal(t, "dn: cn=a,cn=zimbra\ncn: a\nzimbraServiceEnabled: mailbox\nzimbraServiceEnabled: mta\n\ndn: cn=b,cn=zimbra\ncn: b\n", stdout)
}

func TestRun_SearchScopeAndJSON(t *testing.T) {
	conn := &fakeConn{result: &goldap.SearchResult{Entries: []*goldap.Entry{
		goldap.NewEntry("cn=a,cn=zimbra", map[string][]string{"cn": {"a"}}),
	}}}

	code, stdout, _, _ := run(t, conn, &Options{
		Verb: VerbSearch, Base: "cn=servers,cn=zimbra", Filter: "(cn=a)", Scope: ScopeOne, Attrs: []string{"cn"}, JSON: true,
		URLs: []string{"ldap://x:389"},
	})

	require.Zero(t, code)
	assert.Equal(t, goldap.ScopeSingleLevel, conn.gotScope)
	assert.Equal(t, "cn=servers,cn=zimbra", conn.gotBase)
	assert.Equal(t, []string{"cn"}, conn.gotAttrs)
	assert.JSONEq(t, `[{"dn":"cn=a,cn=zimbra","attributes":{"cn":["a"]}}]`, stdout)

	code, _, stderr, dialed := run(t, conn, &Options{Verb: VerbSearch, Scope: "bogus", URLs: []string{"ldap://x:389"}})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, `unknown scope "bogus"`)
	assert.Nil(t, dialed, "argument errors are reported before connecting")
}

func TestRun_FlagsOverrideLocalconfig(t *testing.T) {
	dir := t.TempDir()
	fakes := systest.New()
	fakes.FS.Put(dir+"/pw", []byte("from-file\n"), 0o600)

	code, _, stderr, dialed := run(t, &fakeConn{global: map[string]string{}}, &Options{
		Verb:         VerbGacf,
		URLs:         []string{"ldap://a:389", "ldap://b:389"},
		BindDN:       "uid=flag,cn=admins,cn=zimbra",
		PasswordFile: dir + "/pw",
		FS:           fakes.FS,
	})

	require.Zero(t, code, stderr)
	require.NotNil(t, dialed)
	assert.Equal(t, []string{"ldap://a:389", "ldap://b:389"}, dialed.URLs)
	assert.Equal(t, "uid=flag,cn=admins,cn=zimbra", dialed.BindDN)
	assert.Equal(t, "from-file", dialed.Password, "the trailing newline is not part of the password")
}

func TestRun_CredentialsFromEnvAndStartTLS(t *testing.T) {
	t.Setenv("CONFIGD_LC_ldap_url", "ldap://env:389")
	t.Setenv("CONFIGD_LC_zimbra_ldap_userdn", "uid=env")
	t.Setenv("CONFIGD_LC_zimbra_ldap_password", "env-secret")
	t.Setenv("CONFIGD_LC_ldap_starttls_supported", "0")

	var dialed *carboldap.ClientConfig

	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), &stdout, &stderr, &Options{
		Verb:       VerbGacf,
		ConfigPath: filepath.Join(t.TempDir(), "absent.xml"),
		FS:         systest.New().FS,
		Dial: func(cfg *carboldap.ClientConfig) (Conn, error) {
			dialed = cfg

			return &fakeConn{global: map[string]string{}}, nil
		},
	})

	require.Zero(t, code, stderr.String())
	assert.Equal(t, []string{"ldap://env:389"}, dialed.URLs)
	assert.Equal(t, "uid=env", dialed.BindDN)
	assert.Equal(t, "env-secret", dialed.Password)
	assert.False(t, dialed.StartTLS, "ldap_starttls_supported=0 means plaintext")
}

func TestRun_Errors(t *testing.T) {
	url := []string{"ldap://x:389"}

	t.Run("no url", func(t *testing.T) {
		code, _, stderr, dialed := run(t, &fakeConn{}, &Options{Verb: VerbGacf})
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "no LDAP URL")
		assert.Nil(t, dialed)
	})

	t.Run("missing password file", func(t *testing.T) {
		code, _, stderr, _ := run(t, &fakeConn{}, &Options{Verb: VerbGacf, URLs: url, PasswordFile: "/nope"})
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "read password file")
	})

	t.Run("dial failure", func(t *testing.T) {
		opts := &Options{Verb: VerbGacf, URLs: url, ConfigPath: filepath.Join(t.TempDir(), "absent.xml"), FS: systest.New().FS}
		opts.Dial = func(*carboldap.ClientConfig) (Conn, error) { return nil, errors.New("refused") }

		var stderr bytes.Buffer

		assert.Equal(t, 1, Run(context.Background(), &bytes.Buffer{}, &stderr, opts))
		assert.Contains(t, stderr.String(), "connecting to LDAP: refused")
	})

	t.Run("query failure", func(t *testing.T) {
		code, _, stderr, _ := run(t, &fakeConn{err: errors.New("denied")}, &Options{Verb: VerbGacf, URLs: url})
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "Error: denied")
	})

	t.Run("bad arguments", func(t *testing.T) {
		for _, opts := range []*Options{
			{Verb: VerbGacf, Args: []string{"x"}},
			{Verb: VerbGcf},
			{Verb: VerbGs, Args: []string{"a", "b"}},
			{Verb: VerbMcf, Args: []string{"odd"}},
			{Verb: "bogus"},
		} {
			opts.URLs = url
			code, _, _, dialed := run(t, &fakeConn{}, opts)
			assert.Equal(t, 1, code, opts.Verb)
			assert.Nil(t, dialed)
		}
	})
}
