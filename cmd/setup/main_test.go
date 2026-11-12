// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestLegacyArgs(t *testing.T) {
	assert.Equal(t, []string{"--legacy-config", "/x/config.1", "--non-interactive"}, legacyArgs([]string{"-c", "/x/config.1"}))
	assert.Equal(t, []string{"run"}, legacyArgs([]string{"-d", "run"}), "-d is accepted and ignored")
	assert.Equal(t, []string{"--set", "a=b", "--legacy-config", "f", "--non-interactive"},
		legacyArgs([]string{"--set", "a=b", "-c", "f", "-d"}))
	assert.Equal(t, []string{"-c"}, legacyArgs([]string{"-c"}), "a -c without a file is left to the parser")
}

func exec(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var out, errb strings.Builder

	status := run(args, systest.New().System(), strings.NewReader(""), &out, &errb)

	return status, out.String(), errb.String()
}

func TestHelpListsTheCommands(t *testing.T) {
	status, out, _ := exec(t, "-h")

	require.Equal(t, 0, status)
	assert.Contains(t, out, "config")
	assert.Contains(t, out, "run")
	assert.Contains(t, out, "--plain")
	assert.Contains(t, out, "--legacy-config")
	assert.Contains(t, out, "--dry-run")
	assert.NotContains(t, out, "-d,", "the legacy -d is not advertised")

	status, out, _ = exec(t, "config", "--help")
	require.Equal(t, 0, status)
	assert.Contains(t, out, "--output")
	assert.NotContains(t, out, "--dry-run", "config changes nothing: no dry run")
}

func TestUsageErrors(t *testing.T) {
	status, _, errb := exec(t, "config", "--wizard", "sometimes")
	assert.Equal(t, exitUsage, status)
	assert.Contains(t, errb, "--wizard must be")

	status, _, errb = exec(t, "run", "--wizard=nope")
	assert.Equal(t, exitUsage, status)
	assert.Contains(t, errb, "--wizard must be")

	status, _, errb = exec(t, "--bogus")
	assert.Equal(t, exitUsage, status)
	assert.Contains(t, errb, "unknown flag")

	status, _, _ = exec(t, "nonsense")
	assert.Equal(t, exitUsage, status)
}

func TestSetupPlCommandsAreListedAndValidated(t *testing.T) {
	status, out, _ := exec(t, "-h")
	require.Equal(t, 0, status)

	for _, c := range []string{"gal-sync-account", "ssh-keygen", "update-authkeys", "syslog", "dhparam", "proxyconfig", "ldap-passwd",
		"ldap-schema", "ldap-update-ldif", "ldap-enable-replica", "ldap-enable-mmr", "iptool", "iniutil"} {
		assert.Contains(t, out, c)
	}

	status, out, _ = exec(t, "gal-sync-account", "--help")
	require.Equal(t, 0, status)
	assert.Contains(t, out, "--domain")
	assert.Contains(t, out, "--server")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"gal-sync-account"}, `required flag(s) "domain", "server" not set`},
		{[]string{"gal-sync-account", "--domain", "example.com"}, `required flag(s) "server" not set`},
		{[]string{"gal-sync-account", "--domain", "example.com", "--server", " "}, "must not be empty"},
		{[]string{"gal-sync-account", "--domain", "", "--server", "s"}, "must not be empty"},
		{[]string{"gal-sync-account", "--domain", "d", "--server", "s", "extra"}, "unknown command"},
	} {
		status, _, errb := exec(t, tc.args...)
		assert.Equal(t, exitUsage, status, tc.args)
		assert.Contains(t, errb, tc.want, tc.args)
	}

	// The commands that replace scripts take the scripts' arguments.
	for _, tc := range []struct {
		args   []string
		status int
		want   string
	}{
		{[]string{"ssh-keygen", "dsa"}, 1, "Bad keytype: dsa"},
		{[]string{"update-authkeys", "-x"}, exitUsage, "Usage: update-authkeys [-a]"},
	} {
		status, _, errb := exec(t, tc.args...)
		assert.Equal(t, tc.status, status, tc.args)
		assert.Contains(t, errb, tc.want, tc.args)
	}
}

func TestSetupPlCommandsRefuseNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the refusal is for non-root users")
	}

	for _, args := range [][]string{
		{"gal-sync-account", "--domain", "example.com", "--server", "store.example.com"},
		{"ssh-keygen"},
		{"update-authkeys"},
		{"syslog"},
		{"update-authkeys", "-a"},
		{"dhparam"},
		{"dhparam", "get"},
		{"ldap-passwd", "-p", "pw"},
		{"proxyconfig", "-m", "-e", "-H", "h"},
		{"ldap-schema"},
		{"ldap-enable-mmr", "-m", "ldap://h:389/"},
		{"iptool", "-d"},
	} {
		status, out, errb := exec(t, args...)

		assert.Equal(t, 1, status, args)
		assert.Empty(t, out, args)
		assert.Contains(t, errb, "must run as root", args)
	}
}

func TestConfigNonInteractiveFailsOnMissingKeys(t *testing.T) {
	status, _, errb := exec(t, "config", "--non-interactive")

	assert.Equal(t, 1, status)
	assert.Contains(t, errb, "ldap.admin_password is required")
}

func TestRunDryRunPrintsTheNotice(t *testing.T) {
	for k, v := range map[string]string{
		"ROLES": "directory-server", "HOST_HOSTNAME": "mail.example.com", "HOST_PUBLIC_HOSTNAME": "mail.example.com",
		"LDAP_HOST": "mail.example.com", "LDAP_ADMIN_PASSWORD": "Sup3rSecret!", "ADMIN_PASSWORD": "Sup3rSecret!",
	} {
		t.Setenv("CARBONIO_BOOTSTRAP_"+k, v)
	}

	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())

	status, out, errb := exec(t, "run", "--dry-run", "--non-interactive")

	require.Equal(t, 0, status, errb)
	assert.Contains(t, out, "Starting Carbonio Bootstrap...")
	assert.Contains(t, out, "Prepare system users and directories")
	assert.Regexp(t, `Prepare system users and directories\s+would apply`, out)

	status, out, _ = exec(t, "--dry-run", "--non-interactive")
	assert.Equal(t, 0, status, "no command is run")
	assert.Contains(t, out, "STEP")
}

func TestAbsPath(t *testing.T) {
	assert.Empty(t, absPath(""))
	assert.Equal(t, "/etc/x.yaml", absPath("/etc/x.yaml"))
	assert.True(t, strings.HasPrefix(absPath("rel.yaml"), "/"))
}

func TestSyslogCommandTakesNoArguments(t *testing.T) {
	status, out, errb := exec(t, "syslog", "extra")

	assert.Equal(t, 2, status)
	assert.Empty(t, out)
	assert.Contains(t, errb, "Usage: syslog")
}

// The options are the script's: cobra must not read -h or -H as its own flags.
func TestProxyConfigCommandPassesTheScriptOptionsThrough(t *testing.T) {
	status, _, errb := exec(t, "proxyconfig", "-z")

	if os.Geteuid() == 0 || os.Getenv("USER") == "zextras" {
		assert.Equal(t, 255, status, "getopts fails: Unable to set options")
		assert.Contains(t, errb, "Unable to set options")

		return
	}

	assert.Equal(t, 1, status)
	assert.Contains(t, errb, "must run as root")
}

// -d and -c are options of zmproxyconfig; the bootstrap's legacy mapping must
// not eat them.
func TestLegacyArgs_ToolCommandsKeepTheirOptions(t *testing.T) {
	assert.Equal(t, []string{"proxyconfig", "-w", "-d", "-o", "-c", "-H", "h"},
		legacyArgs([]string{"proxyconfig", "-w", "-d", "-o", "-c", "-H", "h"}))
	assert.Equal(t, []string{"ldap-passwd", "-c", "-l", "-d"}, legacyArgs([]string{"ldap-passwd", "-c", "-l", "-d"}),
		"-c is the replica option of zmldappasswd")
	assert.Equal(t, []string{"iptool", "-d", "-m", "ipv6"}, legacyArgs([]string{"iptool", "-d", "-m", "ipv6"}),
		"-d is the dry run of zmiptool")
	assert.Equal(t, []string{"ldap-enable-mmr", "-s", "2", "-m", "ldap://h:389/"},
		legacyArgs([]string{"ldap-enable-mmr", "-s", "2", "-m", "ldap://h:389/"}))
	assert.Equal(t, []string{"dhparam", "set", "-d", "-new"}, legacyArgs([]string{"dhparam", "set", "-d", "-new"}))
	assert.Equal(t, []string{"--legacy-config", "f", "--non-interactive"}, legacyArgs([]string{"-d", "-c", "f"}),
		"the bootstrap's own mapping stays")
}
