// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"crypto/tls"
	"errors"
	"maps"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
	"github.com/zextras/carbonio-configd/internal/testutil/pki"
)

// TestInitNativeLdapClient_StartTLSFromLoadedLocalconfig is the GCR-05
// regression: the flag used to be read from mainConfig, which is only filled
// by LoadAllConfigs after this client is built, so the daemon never attempted
// StartTLS. It must follow the localconfig the client is built from.
func TestInitNativeLdapClient_StartTLSFromLoadedLocalconfig(t *testing.T) {
	t.Parallel()

	pk := pki.NewServer(t, "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{
		TLS: &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
	})

	load := func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":                             srv.URL("ldap", "127.0.0.1"),
			"zimbra_ldap_userdn":                   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password":                 "secret",
			"ldap_starttls_supported":              "1",
			"zimbra_require_interprocess_security": "1",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{}, // not yet populated, as at construction time
		CommandRegistry: commands.NewRegistry(),
	}
	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient == nil {
		t.Fatal("NativeLdapClient not initialised")
	}

	t.Cleanup(func() { _ = cm.NativeLdapClient.Close() })

	if _, err := cm.NativeLdapClient.Search(t.Context(), "cn=zimbra", "(objectClass=*)", []string{"cn"}, goldap.ScopeBaseObject); err != nil {
		t.Fatalf("search: %v", err)
	}

	if srv.StartTLSOps() != 1 || srv.BindsInTLS() != srv.Binds() || srv.Binds() == 0 {
		t.Errorf("startTLS=%d binds=%d bindsInTLS=%d: daemon must bind inside StartTLS",
			srv.StartTLSOps(), srv.Binds(), srv.BindsInTLS())
	}
}

func TestInitNativeLdapClient_LoadFails(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return nil, errors.New("disk read failed")
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}
	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when load fails")
	}
}

func TestInitNativeLdapClient_MissingURL(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}
	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when ldap_url is missing")
	}
}

func TestInitNativeLdapClient_WhitespaceURL(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "   \t  ",
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}
	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when ldap_url has only whitespace")
	}
}

func TestInitNativeLdapClient_MissingBindDN(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "ldap://srv1:389 ldap://srv2:389",
			"zimbra_ldap_password": "secret",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}
	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when bind DN is missing")
	}
}

func TestInitNativeLdapClient_MissingPassword(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":           "ldap://srv1:389",
			"zimbra_ldap_userdn": "uid=zimbra,cn=admins,cn=zimbra",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}
	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when password is missing")
	}
}

func TestInitNativeLdapClient_MultiURLSucceedsBuilding(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "ldap://srv3:389 ldap://srv1:389",
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	}

	// Note: RegisterLDAPCommands now registers to the per-instance registry,
	// so no global cleanup is needed.

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}
	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient == nil {
		t.Fatal("NativeLdapClient should be created when localconfig is complete (multi-URL form)")
	}
}

func TestInitNativeLdapClient_ConfigClientWiredWithRootPassword(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "ldap://srv1:389",
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
			"ldap_root_password":   "rootpw",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
		LdapClient:      ldap.NewLdap(context.Background(), &config.Config{}),
	}
	cm.syncLdapClients(context.Background())

	if cm.ConfigLdapClient == nil {
		t.Fatal("ConfigLdapClient should be created when ldap_root_password is present")
	}
	if cm.LdapClient.ConfigClient == nil {
		t.Error("config client should be wired into the Ldap wrapper")
	}
	// Writes must NOT reuse the data client identity.
	if cm.LdapClient.ConfigClient == cm.LdapClient.NativeClient {
		t.Error("config client must be a distinct client from the data NativeClient")
	}
}

func TestInitNativeLdapClient_ConfigClientNilWithoutRootPassword(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "ldap://srv1:389",
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
		LdapClient:      ldap.NewLdap(context.Background(), &config.Config{}),
	}
	cm.syncLdapClients(context.Background())

	if cm.ConfigLdapClient != nil {
		t.Error("ConfigLdapClient should be nil when ldap_root_password is absent")
	}
}

func TestSyncLdapClients_RetriesUntilLocalconfigIsReady(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{})
	ready := false

	load := func() (map[string]string, error) {
		if !ready {
			return nil, errors.New("localconfig not ready")
		}

		return map[string]string{
			"ldap_url":             srv.URL("ldap", "127.0.0.1"),
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: load,
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}

	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient != nil {
		t.Fatal("client must stay nil while localconfig is not ready")
	}

	ready = true

	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient == nil {
		t.Fatal("client not built on retry")
	}

	t.Cleanup(func() { _ = cm.NativeLdapClient.Close() })

	if _, ok := cm.CommandRegistry.Commands["garpu"]; !ok {
		t.Error("LDAP commands not registered after retry")
	}

	built := cm.NativeLdapClient

	cm.syncLdapClients(context.Background())

	if cm.NativeLdapClient != built {
		t.Error("an existing client must not be rebuilt")
	}
}

// A rotated zimbra_ldap_password or a moved ldap_url reaches the native client
// on the next sync, in place: the registered commands and the watcher hold the
// same client.
func TestSyncLdapClients_AppliesLocalconfigChanges(t *testing.T) {
	t.Parallel()

	first := ldapfake.New(t, ldapfake.Options{})
	second := ldapfake.New(t, ldapfake.Options{})

	lc := map[string]string{
		"ldap_url":             first.URL("ldap", "127.0.0.1"),
		"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password": "old",
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: func() (map[string]string, error) { return maps.Clone(lc), nil },
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
	}

	search := func() error {
		_, err := cm.NativeLdapClient.Search(t.Context(), "cn=zimbra", "(objectClass=*)", []string{"cn"}, goldap.ScopeBaseObject)

		return err
	}

	cm.syncLdapClients(t.Context())
	require.NotNil(t, cm.NativeLdapClient)
	t.Cleanup(cm.Close)

	built := cm.NativeLdapClient

	require.NoError(t, search())
	require.Equal(t, 1, first.Binds())

	cm.syncLdapClients(t.Context())
	require.NoError(t, search())
	require.Equal(t, 1, first.Binds(), "an unchanged localconfig keeps the pooled connection")

	lc["ldap_url"] = second.URL("ldap", "127.0.0.1")
	lc["zimbra_ldap_password"] = "new"

	cm.syncLdapClients(t.Context())
	require.Same(t, built, cm.NativeLdapClient, "reconfigured in place, not replaced")
	require.NoError(t, search())
	require.Equal(t, 1, second.Binds(), "the next operation connects to the new URL")
	require.Equal(t, 1, first.Binds())
}

// The cn=config client failed once (no ldap_root_password yet) is built by a
// later sync, and a changed root password is applied to it afterwards.
func TestSyncLdapClients_RetriesAndReconfiguresConfigClient(t *testing.T) {
	t.Parallel()

	srv := ldapfake.New(t, ldapfake.Options{})

	lc := map[string]string{
		"ldap_url":             srv.URL("ldap", "127.0.0.1"),
		"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password": "secret",
	}

	cm := &ConfigManager{
		env:             hostEnv(),
		loadLocalConfig: func() (map[string]string, error) { return maps.Clone(lc), nil },
		mainConfig:      &config.Config{},
		CommandRegistry: commands.NewRegistry(),
		LdapClient:      ldap.NewLdap(t.Context(), &config.Config{}),
	}

	cm.syncLdapClients(t.Context())
	t.Cleanup(cm.Close)
	require.NotNil(t, cm.NativeLdapClient)
	require.Nil(t, cm.ConfigLdapClient, "no ldap_root_password yet")

	lc["ldap_root_password"] = "root1"

	cm.syncLdapClients(t.Context())
	require.NotNil(t, cm.ConfigLdapClient, "the failed config client is retried")

	configClient := cm.ConfigLdapClient

	lc["ldap_root_password"] = "root2"

	cm.syncLdapClients(t.Context())
	require.Same(t, configClient, cm.ConfigLdapClient)

	changed, err := configClient.Reconfigure(&ldap.ClientConfig{
		URL: ldap.ConfigBackendURL, BindDN: ldap.ConfigBackendDN, Password: "root2",
	})
	require.NoError(t, err)
	require.False(t, changed, "the new root password was already applied by the sync")
}
