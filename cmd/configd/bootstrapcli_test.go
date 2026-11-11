// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func bootstrapCLIEnv(t *testing.T) *systest.Fakes {
	t.Helper()

	t.Setenv("CARBONIO_BASE_DIR", t.TempDir())
	t.Setenv("CARBONIO_BOOTSTRAP_ADMIN_PASSWORD", "Sup3rSecret!")
	t.Setenv("CARBONIO_BOOTSTRAP_STORE_SMTP_HOST", "smtp.example.com")

	f := systest.New()
	f.FS.Put("/etc/os-release", []byte("ID=rocky\n"), 0o644)
	f.Exec.OnPath("rpm", systest.Reply(
		"carbonio-core install ok installed\ncarbonio-directory-server install ok installed\n"))
	f.Exec.OnPath("hostname", systest.Reply("mail.example.com\n"))

	return f
}

func TestBootstrapConfigCmd_OptionsReachTheCommand(t *testing.T) {
	f := bootstrapCLIEnv(t)

	var out, errb bytes.Buffer

	c := &BootstrapConfigCmd{
		NonInteractive: true,
		Wizard:         "missing",
		Sets:           []string{"ldap.host=mail.example.com", "ldap.admin_password=ldapSecret1"},
	}

	err := c.Run(f.System(), &stdio{In: strings.NewReader(""), Out: &out, Err: &errb})

	require.NoError(t, err, errb.String())
	assert.Contains(t, out.String(), "hostname: mail.example.com", "--set and the environment are applied")
	assert.NotContains(t, out.String(), "Sup3rSecret!")
	assert.NotContains(t, out.String(), "ldapSecret1")
}

func TestBootstrapConfigCmd_InvalidConfigurationExitsWithStatus1(t *testing.T) {
	f := bootstrapCLIEnv(t)

	var out, errb bytes.Buffer

	c := &BootstrapConfigCmd{NonInteractive: true, Wizard: "missing", Sets: []string{"ldap.port=notanumber"}}

	err := c.Run(f.System(), &stdio{In: strings.NewReader(""), Out: &out, Err: &errb})

	require.Error(t, err)
	assert.Equal(t, exitStatus(1), err)
	assert.Contains(t, errb.String(), "ldap.port")
}

func TestBootstrapRunCmd_DryRunListsStepsWithoutRoot(t *testing.T) {
	f := bootstrapCLIEnv(t)

	var out, errb bytes.Buffer

	c := &BootstrapRunCmd{DryRun: true}
	c.NonInteractive, c.Wizard = true, "missing"
	c.Sets = []string{"ldap.host=mail.example.com", "ldap.admin_password=ldapSecret1"}

	err := c.Run(f.System(), &stdio{In: strings.NewReader(""), Out: &out, Err: &errb})

	require.NoError(t, err, errb.String())
	assert.Contains(t, out.String(), "STEP")
	assert.Contains(t, out.String(), "Installing LDAP schema")
	assert.NotContains(t, out.String()+errb.String(), "Sup3rSecret!")
}
