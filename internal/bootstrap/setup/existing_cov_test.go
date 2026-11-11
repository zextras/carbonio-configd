// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

func existCovJoinEnv(t *testing.T) *Env {
	t.Helper()

	e := existEnv(t, nil)
	e.Facts.NewInstall = true
	e.Cfg = &bootstrap.Config{}
	e.Cfg.Host.Hostname = "new.example.com"
	e.Cfg.LDAP = bootstrap.LDAPConfig{Host: "ldap.example.com", Port: 389, AdminPassword: "adminpw"}

	return e
}

func TestExistingFromMaster_ReadsTheMasterAndDropsTheSeeds(t *testing.T) {
	e := existCovJoinEnv(t)
	f := &existFakeLDAP{server: map[string]string{"zimbraId": "x", "zimbraIPMode": "ipv4"}, global: existGlobal()}

	got := existingFromMaster(e, existOpen(f), func() bool { return true })

	assert.Equal(t, "new.example.com", f.hostAsked, "the node's own entry is looked up")
	assert.True(t, f.closed)
	assert.Equal(t, "example.com", got["domain.name"])
	assert.Equal(t, "ipv4", got["host.ip_mode"])

	for _, seed := range []string{"host.hostname", "ldap.host", "ldap.port", "ldap.admin_password"} {
		assert.NotContains(t, got, seed, "the operator's own settings keep their layer")
	}
}

func TestExistingFromMaster_SkippedWhenThereIsNothingToJoin(t *testing.T) {
	open := func(f *existFakeLDAP) existOpenLDAP {
		return func() (existLDAPReader, error) {
			t.Fatal("the LDAP must not be opened")

			return f, nil
		}
	}

	for name, mutate := range map[string]func(*Env){
		"no configuration":    func(e *Env) { e.Cfg = nil },
		"no ldap host":        func(e *Env) { e.Cfg.LDAP.Host = "" },
		"this node is master": func(e *Env) { e.Cfg.LDAP.Host = e.Cfg.Host.Hostname },
	} {
		t.Run(name, func(t *testing.T) {
			e := existCovJoinEnv(t)
			mutate(e)

			got := existingFromMaster(e, open(&existFakeLDAP{}), func() bool { return true })
			assert.Empty(t, got)
		})
	}

	t.Run("master unavailable", func(t *testing.T) {
		got := existingFromMaster(existCovJoinEnv(t), open(&existFakeLDAP{}), func() bool { return false })
		assert.Empty(t, got)
	})
}

func TestExistingFromMaster_UnreachableMasterGivesNothing(t *testing.T) {
	got := existingFromMaster(existCovJoinEnv(t), existFailOpen(), func() bool { return true })
	assert.Empty(t, got)
}

func TestExistingValues_NewInstallWithoutMasterReadsNothing(t *testing.T) {
	e := existCovJoinEnv(t)
	e.Cfg.LDAP.Host = e.Cfg.Host.Hostname

	got, err := ExistingValues(e)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestExistingValues_ExistingInstallReadsLocalConfigAndSurvivesMissingLDAPSettings(t *testing.T) {
	e := existEnv(t, existBaseLC(), PkgAppserver)
	e.Facts.NewInstall = false

	// The localconfig has no ldap_url/ldap_master_url: opening the client
	// fails, which only costs the LDAP part of the values.
	got, err := ExistingValues(e)
	require.NoError(t, err)
	assert.Equal(t, "636", got["ldap.port"])
	assert.Equal(t, "mail1.example.com", got["host.hostname"])
}

func TestEnv_LDAPFailsWithoutMasterURL(t *testing.T) {
	e := existEnv(t, map[string]string{"zimbra_ldap_password": "x"})

	c, err := e.LDAP()
	require.ErrorContains(t, err, "LDAP parameters")
	assert.Nil(t, c)
}
