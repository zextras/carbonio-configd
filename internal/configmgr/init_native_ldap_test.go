// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{}, // not yet populated, as at construction time
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
	}
	cm.initNativeLdapClient(context.Background())

	if cm.NativeLdapClient == nil {
		t.Fatal("NativeLdapClient not initialised")
	}

	t.Cleanup(func() { _ = cm.NativeLdapClient.Close() })

	if _, err := cm.NativeLdapClient.Search("cn=zimbra", "(objectClass=*)", []string{"cn"}, goldap.ScopeBaseObject); err != nil {
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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
	}
	cm.initNativeLdapClient(context.Background())

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
	}
	cm.initNativeLdapClient(context.Background())

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
	}
	cm.initNativeLdapClient(context.Background())

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
	}
	cm.initNativeLdapClient(context.Background())

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
	}
	cm.initNativeLdapClient(context.Background())

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
	}
	cm.initNativeLdapClient(context.Background())

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
		LdapClient:             ldap.NewLdap(context.Background(), &config.Config{}),
	}
	cm.initNativeLdapClient(context.Background())

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
		env:                    hostEnv(),
		loadLocalConfigForLDAP: load,
		mainConfig:             &config.Config{},
		CommandRegistry:        commands.NewRegistry("", hostEnv()),
		LdapClient:             ldap.NewLdap(context.Background(), &config.Config{}),
	}
	cm.initNativeLdapClient(context.Background())

	if cm.ConfigLdapClient != nil {
		t.Error("ConfigLdapClient should be nil when ldap_root_password is absent")
	}
}
