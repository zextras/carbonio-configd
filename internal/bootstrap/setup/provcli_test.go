// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prov runs the command line and returns the status, stdout and stderr.
func provCLI(t *testing.T, e *Env, args ...string) (status int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	status = RunProvCLI(e, args, &out, &errOut)

	return status, out.String(), errOut.String()
}

func TestRunProvCLI_AcceptsAndIgnoresTheConnectionOptions(t *testing.T) {
	e, fakes, _ := testEnv(t)

	status, out, errOut := provCLI(t, e, "-r", "-m", "-l", "gs", "mail.example.com", "zimbraServiceHostname")

	assert.Equal(t, 0, status, errOut)
	assert.Equal(t, "# name mail.example.com\nzimbraServiceHostname: mail.example.com\n\n", out)
	assert.Empty(t, fakes.Exec.Calls(), "no zmprov process")
}

func TestRunProvCLI_WriteThenRead(t *testing.T) {
	e, _, _ := testEnv(t)

	status, out, errOut := provCLI(t, e, "-r", "-m", "-l", "cs", "new.example.com")
	require.Equal(t, 0, status, errOut)
	assert.Regexp(t, `^[0-9a-f-]{36}\n$`, out)

	status, out, _ = provCLI(t, e, "gas")
	assert.Equal(t, 0, status)
	assert.Equal(t, "ldap1.example.com\nmail.example.com\nmta.example.com\nnew.example.com\n", out)
}

func TestRunProvCLI_UnknownVerbExitsOneAndNamesIt(t *testing.T) {
	e, fakes, _ := testEnv(t)

	for _, args := range [][]string{{"-r", "-m", "-l", "gdpak", "x.org"}, {"-l"}} {
		status, out, errOut := provCLI(t, e, args...)

		assert.Equal(t, 1, status)
		assert.Empty(t, out)
		assert.Contains(t, errOut, "unknown command")
	}

	_, _, errOut := provCLI(t, e, "gdpak", "x.org")
	assert.Contains(t, errOut, `"gdpak"`)
	assert.Zero(t, provTestLDAP().opened, "no LDAP connection for an unknown verb")
	assert.Empty(t, fakes.Exec.Calls(), "no fallback to the Java zmprov")
}

func TestRunProvCLI_ServiceErrorsExitTwoLikeZmprov(t *testing.T) {
	e, _, _ := testEnv(t)

	status, out, errOut := provCLI(t, e, "gs", "nosuch.example.com")

	assert.Equal(t, 2, status)
	assert.Empty(t, out)
	assert.Equal(t, "ERROR: account.NO_SUCH_SERVER (no such server: nosuch.example.com)\n", errOut)
}

func TestRunProvCLI_WrongArgumentCountExitsOne(t *testing.T) {
	e, _, _ := testEnv(t)

	status, _, errOut := provCLI(t, e, "gcf")

	assert.Equal(t, 1, status)
	assert.Contains(t, errOut, "gcf is expecting 1 arguments")
}

func TestRunProvCLI_ADirectoryFailureExitsTwoWithoutLeakingAPassword(t *testing.T) {
	e, _, _ := testEnv(t)
	provTestLDAP().openErr = errors.New("connection refused")

	status, _, errOut := provCLI(t, e, "ca", "admin@example.com", "S3cret-pass-word")

	assert.Equal(t, 2, status)
	assert.Contains(t, errOut, "ERROR: service.FAILURE (opening LDAP: connection refused)")
	assert.NotContains(t, errOut, "S3cret-pass-word")
}

func TestRunProvCLI_AFailedCreateDoesNotEchoThePassword(t *testing.T) {
	e, _, _ := testEnv(t)

	status, _, errOut := provCLI(t, e, "ca", "admin@nowhere.org", "S3cret-pass-word")

	assert.Equal(t, 2, status)
	assert.NotContains(t, errOut, "S3cret-pass-word")
	assert.Contains(t, errOut, "no such domain")
}
