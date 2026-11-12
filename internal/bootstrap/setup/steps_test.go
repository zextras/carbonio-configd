// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// stepsIndex returns the position of the step with the given title.
func stepsIndex(t *testing.T, steps []Step, title string) int {
	t.Helper()

	i := slices.IndexFunc(steps, func(s Step) bool { return s.Title == title })
	require.GreaterOrEqual(t, i, 0, "step %q missing", title)

	return i
}

// TestSteps_OrderFollowsSetupPl pins the order of setup.pl: later steps read
// what earlier ones produce (certificates and passwords before LDAP, LDAP and
// the server entry before the services start, the crontab last).
func TestSteps_OrderFollowsSetupPl(t *testing.T) {
	steps := Steps()

	var stages []string

	for _, s := range steps {
		require.NotEmpty(t, s.Title)
		require.NotNil(t, s.Apply, s.Title)

		if s.Name != "" {
			stages = append(stages, s.Name)
		}
	}

	assert.Equal(t, []string{
		"configLCValues", "configInitCore", "configCASetup", "configCreateCert",
		"configInstallCertStore", "configInstallCertMTA", "configInstallCertLDAP", "configInstallCertProxy",
		"configCreateServerEntry", "configSetupLdap", "configCreateServerEntry",
		"configSaveCA", "configSaveCert", "configSetServicePorts", "zimbraPrefUseKeyboardShortcuts",
		"zimbraPrefTimeZoneId", "configInitMta", "configSetEnabledServices", "configCreateDomain",
		"configInitSql", "setupSyslog", "configCreateDefaultDomainGALSyncAcct", "setupCrontab",
	}, stages)

	idx := func(title string) int { return stepsIndex(t, steps, title) }

	ldap := idx("Configure LDAP")
	assert.Less(t, idx("Set up the CA"), idx("Create SSL certificates"))
	assert.Less(t, idx("Install LDAP SSL certificate"), idx("Update the LDAP passwords"))
	assert.Less(t, idx("Install proxy SSL certificate"), ldap, "the certificates are deployed before LDAP is set up")
	assert.Equal(t, ldap-1, idx("Update the LDAP passwords"), "the passwords are written right before configSetupLdap")
	assert.Less(t, idx("Save the CA in LDAP"), idx("Start servers"), "the CA is saved in LDAP before the servers start")
	assert.Less(t, idx("Start LDAP"), idx("Start servers"))
	assert.Less(t, idx("Start servers"), idx("Create GAL sync account"),
		"the GAL sync account needs the mailbox running")
	assert.Less(t, idx("Create GAL sync account"), idx("Set up the zextras crontab"))
	assert.Less(t, idx("Set up the zextras crontab"), idx("Update authorized SSH keys"))
	assert.Equal(t, "Update authorized SSH keys", steps[len(steps)-1].Title)

	// The pre-steps of the main program run before applyConfig.
	assert.Less(t, idx("Update the LDAP directory"), idx("Set mailboxd local config values"))
}

// TestSteps_ServerEntryAroundLdapSetup: a replica creates its server entry
// before configSetupLdap (it is read from the master it replicates), every
// other node after it; exactly one of the two runs on a node.
func TestSteps_ServerEntryAroundLdapSetup(t *testing.T) {
	steps := Steps()
	ldap := stepsIndex(t, steps, "Configure LDAP")

	var entries []int

	for i, s := range steps {
		if s.Name == "configCreateServerEntry" {
			entries = append(entries, i)
		}
	}

	require.Len(t, entries, 2)
	assert.Less(t, entries[0], ldap)
	assert.Greater(t, entries[1], ldap)

	for _, tc := range []struct {
		repl        string
		wantReplica bool
	}{
		{bootstrap.ReplicationReplica, true},
		{bootstrap.ReplicationMMR, true},
		{"", false},
	} {
		e := &Env{Cfg: &bootstrap.Config{}}
		e.Cfg.Roles = []string{bootstrap.RoleDirectoryServer}
		e.Cfg.LDAP.ReplicationType = tc.repl

		before, after := steps[entries[0]].When(e), steps[entries[1]].When(e)
		assert.Equal(t, tc.wantReplica, before, "replication %q before", tc.repl)
		assert.Equal(t, !tc.wantReplica, after, "replication %q after", tc.repl)
	}
}
