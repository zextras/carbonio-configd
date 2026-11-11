// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/sys"
)

type host struct{}

func (host) FQDN() string { return "mail.example.com" }
func (host) InstalledRoles() []string {
	return []string{bootstrap.RoleDirectoryServer, bootstrap.RoleAppserver}
}
func (host) IPMode() string     { return "ipv4" }
func (host) Timezone() string   { return "Europe/Rome" }
func (host) Networks() []string { return nil }
func (host) MemoryMB() int      { return 4096 }

func runCmd(t *testing.T, opts *Options, deps Deps, stdin string) (int, string, string) {
	t.Helper()

	deps.Host = host{}

	var out, errb strings.Builder

	status := run(context.Background(), sys.Real(), strings.NewReader(stdin), &out, &errb, opts, deps)

	return status, out.String(), errb.String()
}

func TestRun_NoTerminalFailsOnMissingKeys(t *testing.T) {
	status, out, errb := runCmd(t, &Options{Wizard: WizardMissing}, Deps{}, "")

	assert.Equal(t, 1, status)
	assert.Empty(t, out, "no wizard without a terminal")
	assert.Contains(t, errb, "admin.password is required")
	assert.Contains(t, errb, "CARBONIO_BOOTSTRAP_ADMIN_PASSWORD[_FILE]")
}

func TestRun_WizardGate(t *testing.T) {
	complete := []string{"CARBONIO_BOOTSTRAP_ADMIN_PASSWORD=secret1", "CARBONIO_BOOTSTRAP_STORE_SMTP_HOST=smtp.example.com"}

	t.Run("complete input skips the wizard", func(t *testing.T) {
		status, out, errb := runCmd(t, &Options{Wizard: WizardMissing, Environ: complete}, Deps{Terminal: true}, "")
		require.Equal(t, 0, status, errb)
		assert.NotContains(t, out, "Hostname of this node")
		assert.Contains(t, out, "password: '"+bootstrap.Mask+"' # env")
	})

	t.Run("--non-interactive never asks", func(t *testing.T) {
		status, _, errb := runCmd(t, &Options{Wizard: WizardAll, NonInteractive: true}, Deps{Terminal: true}, "")
		assert.Equal(t, 1, status)
		assert.Contains(t, errb, "admin.password is required")
	})

	t.Run("--wizard=all needs a terminal", func(t *testing.T) {
		status, _, errb := runCmd(t, &Options{Wizard: WizardAll, Environ: complete}, Deps{}, "")
		assert.Equal(t, 1, status)
		assert.Contains(t, errb, "--wizard=all needs a terminal")
	})

	t.Run("a missing key starts the wizard, which can save", func(t *testing.T) {
		saved := filepath.Join(t.TempDir(), "bootstrap.yaml")
		// 11 pre-filled questions, the admin password twice, the SMTP host (no
		// MTA on the node: no default), start services, accept, save.
		answers := strings.Repeat("\n", 11) + "secret1\nsecret1\nsmtp.example.com\n\ny\n" + saved + "\n"

		status, out, errb := runCmd(t, &Options{Wizard: WizardMissing}, Deps{Terminal: true}, answers)
		require.Equal(t, 0, status, errb+out)
		assert.Contains(t, out, "Configuration saved to "+saved)

		data, err := os.ReadFile(saved)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "secret1")

		info, err := os.Stat(saved)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
}

func TestRun_ConfigAndLegacyAreExclusive(t *testing.T) {
	status, _, errb := runCmd(t, &Options{ConfigPath: "a.yaml", LegacyPath: "b.conf"}, Deps{}, "")

	assert.Equal(t, 1, status)
	assert.Contains(t, errb, "mutually exclusive")
}

func TestRun_LegacyMigration(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "config.1234")
	output := filepath.Join(dir, "bootstrap.yaml")

	require.NoError(t, os.WriteFile(legacy, []byte("HOSTNAME=\"mx.example.org\"\nSMTPHOST=\"smtp.example.org\"\nCREATEADMINPASS=\"secret1\"\nEXPANDMENU=\"no\"\n"), 0o600))

	status, out, errb := runCmd(t, &Options{LegacyPath: legacy, Output: output}, Deps{}, "")
	require.Equal(t, 0, status, errb)
	assert.Contains(t, out, "Configuration saved to "+output)
	assert.Contains(t, errb, "legacy key EXPANDMENU not imported: obsolete")

	data, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hostname: mx.example.org")
	assert.NotContains(t, string(data), "secret1", "a legacy password is used but never saved")
}
