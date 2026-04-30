// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

func withLocalConfigForLDAP(t *testing.T, fn func() (map[string]string, error)) {
	t.Helper()

	prev := loadLocalConfigForLDAP
	t.Cleanup(func() { loadLocalConfigForLDAP = prev })

	loadLocalConfigForLDAP = fn
}

func TestInitNativeLdapClient_LoadFails(t *testing.T) {
	withLocalConfigForLDAP(t, func() (map[string]string, error) {
		return nil, errors.New("disk read failed")
	})

	cm := &ConfigManager{mainConfig: &config.Config{}}
	cm.initNativeLdapClient(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when load fails")
	}
}

func TestInitNativeLdapClient_MissingURL(t *testing.T) {
	withLocalConfigForLDAP(t, func() (map[string]string, error) {
		return map[string]string{
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	})

	cm := &ConfigManager{mainConfig: &config.Config{}}
	cm.initNativeLdapClient(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when ldap_url is missing")
	}
}

func TestInitNativeLdapClient_WhitespaceURL(t *testing.T) {
	withLocalConfigForLDAP(t, func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "   \t  ",
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	})

	cm := &ConfigManager{mainConfig: &config.Config{}}
	cm.initNativeLdapClient(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when ldap_url has only whitespace")
	}
}

func TestInitNativeLdapClient_MissingBindDN(t *testing.T) {
	withLocalConfigForLDAP(t, func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "ldap://srv1:389 ldap://srv2:389",
			"zimbra_ldap_password": "secret",
		}, nil
	})

	cm := &ConfigManager{mainConfig: &config.Config{}}
	cm.initNativeLdapClient(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when bind DN is missing")
	}
}

func TestInitNativeLdapClient_MissingPassword(t *testing.T) {
	withLocalConfigForLDAP(t, func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":           "ldap://srv1:389",
			"zimbra_ldap_userdn": "uid=zimbra,cn=admins,cn=zimbra",
		}, nil
	})

	cm := &ConfigManager{mainConfig: &config.Config{}}
	cm.initNativeLdapClient(context.Background())

	if cm.NativeLdapClient != nil {
		t.Error("NativeLdapClient should be nil when password is missing")
	}
}

func TestInitNativeLdapClient_MultiURLSucceedsBuilding(t *testing.T) {
	withLocalConfigForLDAP(t, func() (map[string]string, error) {
		return map[string]string{
			"ldap_url":             "ldap://srv3:389 ldap://srv1:389",
			"zimbra_ldap_userdn":   "uid=zimbra,cn=admins,cn=zimbra",
			"zimbra_ldap_password": "secret",
		}, nil
	})

	cm := &ConfigManager{mainConfig: &config.Config{}}
	cm.initNativeLdapClient(context.Background())

	if cm.NativeLdapClient == nil {
		t.Fatal("NativeLdapClient should be created when localconfig is complete (multi-URL form)")
	}
}
