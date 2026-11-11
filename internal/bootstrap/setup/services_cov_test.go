// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepGALSyncSkipNotice_ExplainsWhyTheAccountIsSkipped(t *testing.T) {
	s := StepGALSyncSkipNotice()

	for name, tc := range map[string]struct {
		start, newInstall, webapp bool
		want                      string
	}{
		"servers not started": {false, true, true, "galsync account creation for default domain skipped"},
		"new but not service": {true, true, false, "not a service node"},
		"existing install":    {true, false, true, "existing install detected"},
	} {
		t.Run(name, func(t *testing.T) {
			e, _, out := srvTestEnv(t, &srvFakeDir{})
			srvEnabled(e, PkgCore, PkgAppserver)
			e.Cfg.StartServices = tc.start
			e.Facts.NewInstall, e.Facts.ServiceWebapp = tc.newInstall, tc.webapp

			require.True(t, s.When(e))
			require.NoError(t, s.Apply(e))
			assert.Contains(t, out.String(), tc.want)
		})
	}

	e, _, _ := srvTestEnv(t, &srvFakeDir{})
	srvEnabled(e, PkgCore, PkgAppserver)
	e.Cfg.StartServices = true
	e.Facts.NewInstall, e.Facts.ServiceWebapp = true, true
	assert.False(t, s.When(e), "the account is created: nothing to explain")

	e.Cfg.StartServices = false
	srvEnabled(e, PkgCore)
	assert.False(t, s.When(e), "no appserver here")
}

func TestSvcInitMta_ToolRunsWithTheMasterAndLocalConfigGetsOwnerDefaults(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{})
	srvEnabled(e, PkgCore, PkgMTA)
	e.Cfg.LDAP.Host, e.Cfg.LDAP.Port = "ldap.example.com", 636
	e.Cfg.MTA.MyNetworks = []string{"127.0.0.0/8", "10.0.0.0/8"}
	fakes.FS.Put("/opt/zextras/libexec/zmmtainit", []byte("#!/bin/sh"), 0o755)
	stdin := provStdin(fakes)

	s := StepConfigInitMta()
	assert.Equal(t, "configInitMta", s.Name)
	require.True(t, s.When(e))
	require.NoError(t, s.Apply(e))

	lc, err := e.LC()
	require.NoError(t, err)
	assert.NotEmpty(t, lc["postfix_mail_owner"])
	assert.NotEmpty(t, lc["postfix_setgid_group"])

	lines := ldapAllLines(fakes)
	assert.Contains(t, lines, "zmmtainit ldap.example.com 636")
	assert.Contains(t, *stdin, "zimbraMtaMyNetworks")
	assert.Contains(t, *stdin, "127.0.0.0/8 10.0.0.0/8")
}

func TestSvcInitMta_KeepsConfiguredOwnerAndSkipsMissingToolAndNetworks(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{})
	srvEnabled(e, PkgCore, PkgMTA)
	require.NoError(t, e.SetLC(map[string]string{"postfix_mail_owner": "custom", "postfix_setgid_group": "customgrp"}))
	stdin := provStdin(fakes)

	require.NoError(t, svcInitMta(e))

	lc, err := e.LC()
	require.NoError(t, err)
	assert.Equal(t, "custom", lc["postfix_mail_owner"], "an owner already set is not overwritten")
	assert.Equal(t, "customgrp", lc["postfix_setgid_group"])
	assert.NotContains(t, ldapAllLines(fakes), "zmmtainit")
	assert.Empty(t, *stdin, "no networks configured: no zmprov call")
}

func TestSvcInitMta_DefaultsLDAPPortAndHost(t *testing.T) {
	e, fakes, _ := srvTestEnv(t, &srvFakeDir{})
	srvEnabled(e, PkgCore, PkgMTA)
	e.Cfg.LDAP.Host, e.Cfg.LDAP.Port = "", 0
	fakes.FS.Put("/opt/zextras/libexec/zmmtainit", []byte("#!/bin/sh"), 0o755)

	require.NoError(t, svcInitMta(e))
	assert.Contains(t, ldapAllLines(fakes), "zmmtainit mail.example.com 389", "falls back to this host and port 389")
}

func TestStepConfigLDAPSchemaVersion_DoneNeedsTheFileAndReachableDirectory(t *testing.T) {
	e, fakes, _ := ldapTestEnv(t)
	s := StepConfigLDAPSchemaVersion()

	_, err := s.Done(e)
	require.ErrorContains(t, err, "could not open", "no conf/attrs-schema")

	fakes.FS.Put("/opt/zextras/conf/attrs-schema", []byte("7\n"), 0o644)

	done, err := s.Done(e)
	require.NoError(t, err, "an unreachable directory is not an error: Apply reports it")
	assert.False(t, done)

	e.Facts.Enabled[ldapPkgDirectory] = false
	assert.False(t, s.When(e))
}
