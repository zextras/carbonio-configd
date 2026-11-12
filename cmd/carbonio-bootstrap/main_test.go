// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
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
	assert.Contains(t, out, "Preparing system users and directories")
	assert.Regexp(t, `Preparing system users and directories\s+would apply`, out)

	status, out, _ = exec(t, "--dry-run", "--non-interactive")
	assert.Equal(t, 0, status, "no command is run")
	assert.Contains(t, out, "STEP")
}

func TestAbsPath(t *testing.T) {
	assert.Empty(t, absPath(""))
	assert.Equal(t, "/etc/x.yaml", absPath("/etc/x.yaml"))
	assert.True(t, strings.HasPrefix(absPath("rel.yaml"), "/"))
}
