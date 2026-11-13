// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const provTestHost = "new.example.com"

func TestRunProv_RunsNoProcessAndWritesTheEntries(t *testing.T) {
	e, fakes, _ := testEnv(t)

	p := &Prov{}
	p.Add("cs", provTestHost)
	p.Add("ms", provTestHost, "+zimbraServiceEnabled", "mailbox", "zimbraMtaMyNetworks", "127.0.0.0/8")
	p.Add("mcf", "zimbraDefaultDomainName", "example.com")
	p.Add("mc", "default", "zimbraPrefTimeZoneId", "Europe/Rome")

	require.NoError(t, e.RunProv(p))

	f := provTestLDAP()
	assert.Equal(t, []string{"zimbraServer"}, f.values(provServerDN(provTestHost), "objectClass"))
	assert.Equal(t, []string{"mailbox"}, f.values(provServerDN(provTestHost), "zimbraServiceEnabled"))
	assert.Equal(t, []string{"127.0.0.0/8"}, f.values(provServerDN(provTestHost), "zimbraMtaMyNetworks"))
	assert.Equal(t, []string{"example.com"}, f.values(provGlobalDN, "zimbraDefaultDomainName"))
	assert.Equal(t, []string{"Europe/Rome"}, f.values("cn=default,cn=cos,cn=zimbra", "zimbraPrefTimeZoneId"))
	assert.Empty(t, fakes.Exec.Calls(), "no zmprov process")
	assert.Equal(t, 1, f.opened)
	assert.Equal(t, 1, f.closed)
}

func TestRunProv_EmptyBatchDoesNothing(t *testing.T) {
	e, _, _ := testEnv(t)

	require.NoError(t, e.RunProv(&Prov{}))
	assert.Zero(t, provTestLDAP().opened)
}

func TestRunProv_BinaryAttributeStoresTheContentOfTheFile(t *testing.T) {
	e, fakes, _ := testEnv(t)
	fakes.FS.Put("/tmp/dh.pem", []byte("-----BEGIN DH PARAMETERS-----\nabc\n"), 0o600)

	require.NoError(t, e.Prov("mcf", "zimbraSSLDHParam", "/tmp/dh.pem"))

	assert.Equal(t, []string{"-----BEGIN DH PARAMETERS-----\nabc\n"}, provTestLDAP().values(provGlobalDN, "zimbraSSLDHParam"))
}

func TestRunProv_ErrorsAreReportedAfterEveryCommandRan(t *testing.T) {
	e, _, _ := testEnv(t)

	p := &Prov{}
	p.Add("ms", "nosuch.example.com", "zimbraServiceEnabled", "mailbox")
	p.Add("mcf", "zimbraDefaultDomainName", "example.com")
	p.Add("ca", "admin@nowhere.org", "S3cret-pass-word")

	err := e.RunProv(p)

	require.ErrorIs(t, err, ErrProv)
	assert.Contains(t, err.Error(), "ms nosuch.example.com")
	assert.Contains(t, err.Error(), "ca admin@nowhere.org")
	assert.NotContains(t, err.Error(), "S3cret-pass-word")
	assert.Equal(t, []string{"example.com"}, provTestLDAP().values(provGlobalDN, "zimbraDefaultDomainName"),
		"the commands after a failed one still run")
}

func TestRunProv_DirectoryFailuresAreReported(t *testing.T) {
	e, _, _ := testEnv(t)
	provTestLDAP().openErr = errors.New("connection refused")

	err := e.Prov("mcf", "zimbraDefaultDomainName", "example.com")
	require.ErrorIs(t, err, ErrProv)
	assert.Contains(t, err.Error(), "connection refused")

	provTestLDAP().openErr = nil
	provTestLDAP().FailModify[provGlobalDN] = errors.New("insufficient access")

	err = e.Prov("mcf", "zimbraDefaultDomainName", "example.com")
	require.ErrorIs(t, err, ErrProv)
	assert.Contains(t, err.Error(), "insufficient access")
}

func TestRunProv_RefusesAnAttributeWhoseCallbackIsNotPorted(t *testing.T) {
	e, _, _ := testEnv(t)

	err := e.Prov("mcf", "zimbraTestUnportedCallback", "x")

	require.ErrorIs(t, err, ErrProv)
	assert.Contains(t, err.Error(), "does not implement")
	assert.Empty(t, provTestLDAP().values(provGlobalDN, "zimbraTestUnportedCallback"))
}
