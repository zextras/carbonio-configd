// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestServerAttrs_WithStub(t *testing.T) {
	want := map[string]string{"zimbraServiceHostname": "mail.example.com", "zimbraServiceEnabled": "proxy\nmta"}
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{serverConfig: want}}

	got, err := e.ServerAttrs(context.Background(), "mail.example.com")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestServerAttrs_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.ServerAttrs(context.Background(), "mail.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "native LDAP query failed for server mail.example.com")
}

func TestGlobalAttrs_WithStub(t *testing.T) {
	want := map[string]string{"zimbraSmtpHostname": "smtp.example.com"}
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{globalConfig: want}}

	got, err := e.GlobalAttrs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestGlobalAttrs_LDAPError(t *testing.T) {
	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{err: errors.New("boom")}}

	_, err := e.GlobalAttrs(context.Background())
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

	_, err := e.ServerAttrs(ctx, "h")
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.GlobalAttrs(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.garpu(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.gamau(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)

	_, err = e.garpb(ctx)
	assert.ErrorContains(t, err, errLDAPNotInitialized)
}

// TestServerListCommands_StableOrder checks that garpu, garpb and gamau return
// the same server order (by server name, as legacy getAllServers) on every
// call. Go map iteration is random: an unstable order makes configd see a
// "Variable changed" and restart proxy/mta/sasl with no real change.
func TestServerListCommands_StableOrder(t *testing.T) {
	servers := map[string]map[string]string{}
	for _, n := range []string{"srv4", "srv1", "srv5", "srv3", "srv2"} {
		servers[n] = map[string]string{
			"zimbraServiceHostname":          n + ".example.com",
			"zimbraReverseProxyLookupTarget": "TRUE",
			"zimbraMtaAuthTarget":            "TRUE",
			"zimbraMailMode":                 "http",
			"zimbraMailPort":                 "8080",
		}
	}

	e := &CommandExecutor{ldapClient: &stubProvisioningLDAP{allServers: servers}}
	hosts := []string{"srv1", "srv2", "srv3", "srv4", "srv5"}

	join := func(format string) string {
		parts := make([]string, len(hosts))
		for i, h := range hosts {
			parts[i] = fmt.Sprintf(format, h+".example.com")
		}

		return strings.Join(parts, " ")
	}

	for name, tc := range map[string]struct {
		run  func(context.Context, ...string) (string, error)
		want string
	}{
		"garpu": {e.garpu, join("%s:7072/service/extension/nginx-lookup")},
		"garpb": {e.garpb, join("%s:8080")},
		"gamau": {e.gamau, join("https://%s:7073/service/admin/soap/")},
	} {
		t.Run(name, func(t *testing.T) {
			for range 50 {
				out, err := tc.run(context.Background())
				require.NoError(t, err)
				require.Equal(t, tc.want, out)
			}
		})
	}
}
