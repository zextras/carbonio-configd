// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeXML(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "localconfig.xml")
	require.NoError(t, os.WriteFile(path, []byte("<localconfig>"+body+"</localconfig>"), 0o600))

	return path
}

// Legacy ShellConfigWriter/ExportConfigWriter: never hide passwords, always expand.
func TestShellExportNeverMask(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"ldap_root_password": "pw", "ref": "${zimbra_home}/x"})

	stdout, _, err := runLC(&Options{ConfigPath: path, Mode: "shell", Keys: []string{"ldap_root_password", "ref"}})
	require.NoError(t, err)
	assert.Equal(t, "ldap_root_password='pw';\nref='/opt/zextras/x';\n", stdout)

	stdout, _, err = runLC(&Options{ConfigPath: path, Mode: "export", Keys: []string{"ldap_root_password"}})
	require.NoError(t, err)
	assert.Equal(t, "export ldap_root_password='pw';\n", stdout)
}

// Legacy mask rule: plain/xml/nokey, case-sensitive "password" only, value "*".
func TestMaskRule(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{
		"my_password_x": "p", "My_PASSWORD_Upper": "p", "api_secret": "p", "ldap_pass": "p", "some_passwd": "p",
	})
	keys := []string{"my_password_x", "My_PASSWORD_Upper", "api_secret", "ldap_pass", "some_passwd"}

	stdout, _, err := runLC(&Options{ConfigPath: path, Keys: keys})
	require.NoError(t, err)
	assert.Equal(t, "my_password_x = *\nMy_PASSWORD_Upper = p\napi_secret = p\nldap_pass = p\nsome_passwd = p\n", stdout)

	stdout, _, err = runLC(&Options{ConfigPath: path, Mode: "nokey", Keys: keys[:1]})
	require.NoError(t, err)
	assert.Equal(t, "*\n", stdout)

	stdout, _, err = runLC(&Options{ConfigPath: path, ShowPasswords: true, Keys: keys[:1]})
	require.NoError(t, err)
	assert.Equal(t, "my_password_x = p\n", stdout)
}

// Without -x values are raw; -x expands; -d -x expands against configured values.
func TestExpandOnlyWithDashX(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"mine": "${zimbra_home}/m"})

	stdout, _, err := runLC(&Options{ConfigPath: path, Mode: "nokey", Keys: []string{"mine", "zimbra_log_directory"}})
	require.NoError(t, err)
	assert.Equal(t, "${zimbra_home}/m\n${zimbra_home}/log\n", stdout)

	stdout, _, err = runLC(&Options{ConfigPath: path, Mode: "nokey", Expand: true, Keys: []string{"mine", "zimbra_log_directory"}})
	require.NoError(t, err)
	assert.Equal(t, "/opt/zextras/m\n/opt/zextras/log\n", stdout)

	stdout, _, err = runLC(&Options{ConfigPath: path, ShowDefaults: true, Mode: "nokey", Keys: []string{"zimbra_log_directory"}})
	require.NoError(t, err)
	assert.Equal(t, "${zimbra_home}/log\n", stdout)

	stdout, _, err = runLC(&Options{ConfigPath: path, ShowDefaults: true, Expand: true, Mode: "nokey", Keys: []string{"zimbra_log_directory"}})
	require.NoError(t, err)
	assert.Equal(t, "/opt/zextras/log\n", stdout)
}

// Read-modify-write must not freeze references (M3).
func TestReadModifyWriteKeepsReferences(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"opts": "-Dttl=${zimbra_home}"})

	stdout, _, err := runLC(&Options{ConfigPath: path, Mode: "nokey", Keys: []string{"opts"}})
	require.NoError(t, err)

	_, _, err = runLC(&Options{ConfigPath: path, Edit: true, Keys: []string{"opts=" + stdout[:len(stdout)-1] + " -Dx=1"}})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "-Dttl=${zimbra_home} -Dx=1")
}

// Null defaults and keys without <value> have no value; an empty <value/> does.
func TestNullKeys(t *testing.T) {
	path := writeXML(t, `<key name="novalue"/><key name="empty"><value/></key>`)

	stdout, stderr, err := runLC(&Options{ConfigPath: path, Keys: []string{"mysql_bind_address", "novalue", "empty"}})
	require.NoError(t, err)
	assert.Equal(t, "empty = \n", stdout)
	assert.Equal(t, "Warning: null valued key 'mysql_bind_address'\nWarning: null valued key 'novalue'\n", stderr)

	stdout, stderr, err = runLC(&Options{ConfigPath: path})
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "mysql_bind_address")
	assert.NotContains(t, stdout, "novalue")
	assert.Contains(t, stdout, "empty = \n")

	// -d: a known null default is skipped silently, an unknown key warns.
	stdout, stderr, err = runLC(&Options{ConfigPath: path, ShowDefaults: true, Keys: []string{"mysql_bind_address", "nope"}})
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Equal(t, "Warning: not a known key 'nope'\n", stderr)
}

// Explicit keys keep their argument order in every mode (L3).
func TestArgumentOrder(t *testing.T) {
	path := newTestLocalconfig(t, map[string]string{"b": "2", "a": "1"})
	keys := []string{"b", "a", "b"}

	for mode, want := range map[string]string{
		"plain":  "b = 2\na = 1\nb = 2\n",
		"shell":  "b='2';\na='1';\nb='2';\n",
		"export": "export b='2';\nexport a='1';\nexport b='2';\n",
		"nokey":  "2\n1\n2\n",
	} {
		stdout, _, err := runLC(&Options{ConfigPath: path, Mode: mode, Keys: keys})
		require.NoError(t, err, mode)
		assert.Equal(t, want, stdout, mode)
	}
}

// A bad reference makes every invocation fail (L7).
func TestInvalidReferencesAreFatal(t *testing.T) {
	cycle := writeXML(t, `<key name="a1"><value>${b1}</value></key><key name="b1"><value>${a1}</value></key>`)
	undefined := writeXML(t, `<key name="a1"><value>${nope}</value></key>`)

	for _, tc := range []struct{ path, detail string }{
		{cycle, "recursive expansion of key 'a1': a1 b1"},
		{undefined, "null valued key 'nope' referenced"},
	} {
		for _, opts := range []*Options{
			{ConfigPath: tc.path, Keys: []string{"a1"}},
			{ConfigPath: tc.path, Edit: true, Keys: []string{"x=1"}},
		} {
			_, _, err := runLC(opts)
			require.Error(t, err)

			var out, errOut bytes.Buffer
			code := Run(t.Context(), &out, &errOut, opts)
			assert.Equal(t, 1, code)
			assert.Contains(t, errOut.String(), "Error: failed with error in config file\n")
			assert.Contains(t, errOut.String(), tc.detail)
		}
	}
}

// -n lists a key without <value> and warns about it, like legacy printChanged.
func TestChangedWarnsAboutValuelessKey(t *testing.T) {
	path := writeXML(t, `<key name="novalue"/><key name="custom"><value>1</value></key>`)

	stdout, stderr, err := runLC(&Options{ConfigPath: path, ShowChanged: true})
	require.NoError(t, err)
	assert.Equal(t, "custom = 1\n", stdout)
	assert.Equal(t, "Warning: null valued key 'novalue'\n", stderr)
}
