// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubProvisioningLDAP implements provisioningLDAP with canned responses.
type stubProvisioningLDAP struct {
	serverConfig map[string]string
	globalConfig map[string]string
	allServers   map[string]map[string]string
	err          error
}

func (s *stubProvisioningLDAP) GetServerConfig(string) (map[string]string, error) {
	return s.serverConfig, s.err
}

func (s *stubProvisioningLDAP) GetGlobalConfig() (map[string]string, error) {
	return s.globalConfig, s.err
}

func (s *stubProvisioningLDAP) GetAllServersWithAttributes() (map[string]map[string]string, error) {
	return s.allServers, s.err
}

func TestGetServer_WithStub(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		serverConfig: map[string]string{"zimbraServiceHostname": "mail.example.com"},
	}}

	out, err := e.getserver(context.Background(), "mail.example.com")
	require.NoError(t, err)
	assert.Contains(t, out, "zimbraServiceHostname: mail.example.com")
}

func TestGetServer_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.getserver(context.Background(), "mail.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "native LDAP query failed")
}

func TestGetServerEnabled_WithServices(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		serverConfig: map[string]string{"zimbraServiceEnabled": "proxy\nmta"},
	}}

	out, err := e.getserverenabled(context.Background(), "mail.example.com")
	require.NoError(t, err)
	assert.Equal(t, "zimbraServiceEnabled: proxy\nmta\n", out)
}

func TestGetServerEnabled_NoServices(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		serverConfig: map[string]string{"cn": "mail"},
	}}

	out, err := e.getserverenabled(context.Background(), "mail.example.com")
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestGetServerEnabled_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.getserverenabled(context.Background(), "mail.example.com")
	require.Error(t, err)
}

func TestGetGlobal_WithStub(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		globalConfig: map[string]string{"zimbraSmtpHostname": "smtp.example.com"},
	}}

	out, err := e.getglobal(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "zimbraSmtpHostname: smtp.example.com")
}

func TestGetGlobal_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.getglobal(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "native LDAP query failed for global config")
}

func TestGarpu_BuildsLookupURLs(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		allServers: map[string]map[string]string{
			"proxy1": {
				"zimbraServiceHostname":          "proxy1.example.com",
				"zimbraReverseProxyLookupTarget": "TRUE",
			},
			"mta1": {
				"zimbraServiceHostname":          "mta1.example.com",
				"zimbraReverseProxyLookupTarget": "FALSE",
			},
			"nohost": {
				"zimbraReverseProxyLookupTarget": "TRUE",
			},
		},
	}}

	out, err := e.garpu(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "proxy1.example.com:7072/service/extension/nginx-lookup", out)
}

func TestGarpu_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.garpu(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "garpu")
}

func TestGamau_BuildsAuthURLs(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		allServers: map[string]map[string]string{
			"mta1": {
				"zimbraServiceHostname": "mta1.example.com",
				"zimbraMtaAuthTarget":   "TRUE",
				"zimbraMtaAuthPort":     "7443",
			},
			"mta2": {
				"zimbraServiceHostname": "mta2.example.com",
				"zimbraMtaAuthTarget":   "TRUE",
				// No port: default applies.
			},
			"proxy1": {
				"zimbraServiceHostname": "proxy1.example.com",
				"zimbraMtaAuthTarget":   "FALSE",
			},
		},
	}}

	out, err := e.gamau(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "https://mta1.example.com:7443/service/admin/soap/")
	assert.Contains(t, out, "https://mta2.example.com:7073/service/admin/soap/")
	assert.NotContains(t, out, "proxy1.example.com")
}

func TestGamau_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.gamau(context.Background())
	require.Error(t, err)
}

func TestGarpb_BuildsBackends(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		allServers: map[string]map[string]string{
			"backend1": {
				"zimbraServiceHostname":          "mbx1.example.com",
				"zimbraReverseProxyLookupTarget": "TRUE",
				"zimbraMailMode":                 "http",
				"zimbraMailPort":                 "8080",
			},
		},
	}}

	out, err := e.garpb(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mbx1.example.com:8080", out)
}

func TestGarpb_EmptyFallback(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{
		allServers: map[string]map[string]string{},
	}}

	out, err := e.garpb(context.Background())
	require.NoError(t, err)
	assert.Equal(t, garpbEmptyFallback, out)
}

func TestGarpb_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.garpb(context.Background())
	require.Error(t, err)
}

func TestLDAPCommands_NilClient(t *testing.T) {
	e := NewCommandExecutor(nil)
	ctx := context.Background()

	_, err := e.getserver(ctx, "h")
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.getserverenabled(ctx, "h")
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.getglobal(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.garpu(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.gamau(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.garpb(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)
}
