// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
)

// brokenWriter fails every write, like a closed stdout pipe.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// failAfter accepts the first n writes, then fails.
type failAfter struct{ n int }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("broken pipe")
	}

	f.n--

	return len(p), nil
}

func TestSearchScope(t *testing.T) {
	t.Parallel()

	for scope, want := range map[string]int{
		ScopeBase: goldap.ScopeBaseObject,
		ScopeOne:  goldap.ScopeSingleLevel,
		ScopeSub:  goldap.ScopeWholeSubtree,
		"":        goldap.ScopeWholeSubtree,
	} {
		got, err := searchScope(scope)
		require.NoError(t, err, scope)
		assert.Equal(t, want, got, scope)
	}

	_, err := searchScope("galaxy")
	assert.ErrorContains(t, err, `unknown scope "galaxy"`)
}

func TestSearch_UnknownScopeSkipsTheQuery(t *testing.T) {
	t.Parallel()

	conn := &fakeConn{}

	err := search(&bytes.Buffer{}, conn, &Options{Verb: VerbSearch, Scope: "galaxy"})

	assert.ErrorContains(t, err, "unknown scope")
	assert.Empty(t, conn.gotBase, "no search was sent")
}

func TestSearch_QueryFailureIsReturned(t *testing.T) {
	t.Parallel()

	err := search(&bytes.Buffer{}, &fakeConn{err: errors.New("no such object")}, &Options{Verb: VerbSearch})

	assert.EqualError(t, err, "no such object")
}

func TestModifyConfig_RejectsMalformedChangesWithoutModifying(t *testing.T) {
	t.Parallel()

	conn := &fakeConn{}

	err := modifyConfig(conn, []string{"zimbraMailMode"})

	assert.Error(t, err)
	assert.Empty(t, conn.gotDN, "nothing was sent to the directory")
}

func TestServerConfig_QueryFailureIsReturned(t *testing.T) {
	t.Parallel()

	err := serverConfig(&bytes.Buffer{}, &fakeConn{err: errors.New("denied")}, nil,
		&Options{Verb: VerbGs, Args: []string{"mbox.example"}})

	assert.EqualError(t, err, "denied")
}

func TestPrintAttrs_WriteFailureIsReturned(t *testing.T) {
	t.Parallel()

	err := printAttrs(brokenWriter{}, map[string]string{"cn": "a"}, false)

	assert.EqualError(t, err, "broken pipe")
}

func TestPrintEntries_WriteFailuresAreReturned(t *testing.T) {
	t.Parallel()

	entries := []*goldap.Entry{
		goldap.NewEntry("cn=a", map[string][]string{"cn": {"a"}}),
		goldap.NewEntry("cn=b", map[string][]string{"cn": {"b"}}),
	}

	// Writes: "dn: cn=a", "cn: a", the separating blank line, "dn: cn=b", "cn: b".
	for ok := range 5 {
		err := printEntries(&failAfter{n: ok}, entries, false)
		assert.EqualError(t, err, "broken pipe", "failing after %d writes", ok)
	}

	assert.NoError(t, printEntries(&failAfter{n: 5}, entries, false))
}

func TestRun_UnreadableLocalconfigFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(path, []byte("<localconfig><key"), 0o600))

	var stderr bytes.Buffer

	code := Run(context.Background(), &bytes.Buffer{}, &stderr, &Options{
		Verb: VerbGacf, URLs: []string{"ldap://x:389"}, ConfigPath: path,
		Dial: func(*carboldap.ClientConfig) (Conn, error) {
			t.Error("must not dial")

			return nil, errors.New("unexpected dial")
		},
	})

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "loading localconfig")
}

func TestRun_DefaultsToInstalledLocalconfigAndRealFS(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CARBONIO_BASE_DIR", base)

	// An empty value would still override the file, so remove the variables.
	for _, key := range []string{"ldap_url", "ldap_master_url"} {
		t.Setenv("CONFIGD_LC_"+key, "")
		require.NoError(t, os.Unsetenv("CONFIGD_LC_"+key))
	}

	conf := filepath.Join(base, "conf")
	require.NoError(t, os.MkdirAll(conf, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(conf, "localconfig.xml"),
		[]byte(`<localconfig><key name="ldap_master_url"><value>ldap://installed:389</value></key></localconfig>`), 0o600))

	var dialed *carboldap.ClientConfig

	var stderr bytes.Buffer

	code := Run(context.Background(), &bytes.Buffer{}, &stderr, &Options{
		Verb: VerbGacf,
		Dial: func(cfg *carboldap.ClientConfig) (Conn, error) {
			dialed = cfg

			return &fakeConn{}, nil
		},
	})

	require.Zero(t, code, stderr.String())
	require.NotNil(t, dialed)
	assert.Equal(t, []string{"ldap://installed:389"}, dialed.URLs)
	assert.NotNil(t, dialed.FS, "the connection gets a filesystem even when Options.FS is unset")
}

func TestDialClient(t *testing.T) {
	t.Parallel()

	conn, err := dialClient(&carboldap.ClientConfig{URLs: []string{"ldap://127.0.0.1:1"}})
	require.NoError(t, err, "connections are opened lazily, on the first query")
	require.NotNil(t, conn)
	assert.NoError(t, conn.Close())

	_, err = dialClient(&carboldap.ClientConfig{})
	assert.ErrorContains(t, err, "no URL configured")
}
