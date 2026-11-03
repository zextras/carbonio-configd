// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
)

func TestHeavyService(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"mailbox", "mailboxd", "MAILBOXD", "service", "appserver-db", "mysql", "mariadb", "store",
		"ldap", "directory", "directory-server",
	} {
		assert.True(t, HeavyService(name), name)
	}

	for _, name := range []string{"mta", "proxy", "amavis", "antivirus", "memcached", "opendkim", "stats", ""} {
		assert.False(t, HeavyService(name), name)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		action  Action
		service string
		systemd bool
		want    Action
	}{
		{"heavy restart on systemd is pending", ActionRestart, "mailboxd", true, ActionPendingRestart},
		{"ldap restart on systemd is pending", ActionRestart, "ldap", true, ActionPendingRestart},
		{"light restart on systemd is a restart", ActionRestart, "mta", true, ActionRestart},
		{"heavy restart off systemd is a restart", ActionRestart, "mailbox", false, ActionRestart},
		{"heavy start is not flagged", ActionStart, "mailbox", true, ActionStart},
		{"heavy stop is not flagged", ActionStop, "ldap", true, ActionStop},
		{"already pending stays pending", ActionPendingRestart, "store", true, ActionPendingRestart},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, Classify(tc.action, tc.service, tc.systemd))
		})
	}
}

func heavyConf() *config.MtaConfig {
	return &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name:         "mta",
			Restarts:     set("mta", "mailboxd", "ldap"),
			RequiredVars: map[string]string{"zimbraX": "VAR"},
		},
	}}
}

func computeHeavy(t *testing.T, systemd bool) Plan {
	t.Helper()

	services := []string{"mta", "mailboxd", "ldap"}
	tracked := map[string]string{"mta": "running", "mailboxd": "running", "ldap": "running"}
	prev := snap(services, map[string]string{"zimbraX": "a"})
	cur := snap(services, map[string]string{"zimbraX": "b"})

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: heavyConf(), Tracked: tracked, Systemd: systemd})
	require.NoError(t, err)

	return p
}

func TestCompute_SystemdFlagsHeavyRestartsPending(t *testing.T) {
	t.Parallel()

	p := computeHeavy(t, true)

	assert.Equal(t, map[string]Action{
		"mta":      ActionRestart,
		"mailboxd": ActionPendingRestart,
		"ldap":     ActionPendingRestart,
	}, restartNames(&p))

	for _, r := range p.Restarts {
		require.Len(t, r.Reasons, 1, r.Service)
		assert.Equal(t, "section mta changed because VAR zimbraX changed 'a'->'b'", r.Reasons[0].String())
	}
}

func TestCompute_LegacyHostKeepsHeavyRestarts(t *testing.T) {
	t.Parallel()

	p := computeHeavy(t, false)

	assert.Equal(t, map[string]Action{
		"mta":      ActionRestart,
		"mailboxd": ActionRestart,
		"ldap":     ActionRestart,
	}, restartNames(&p))
}

func TestCompute_CarriedHeavyRestartStaysPending(t *testing.T) {
	t.Parallel()

	s := snap([]string{"mta", "ldap"}, baseVars())
	carried := Restart{Service: "ldap", Action: ActionRestart, Reasons: []Reason{{Kind: ReasonForced, Section: "x"}}}

	p, err := Compute(Input{
		Prev: s, Cur: s, Conf: mtaConf(), Systemd: true,
		Tracked: map[string]string{"mta": "running", "ldap": "running"},
		Carry:   Carry{Restarts: []Restart{carried}},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]Action{"ldap": ActionPendingRestart}, restartNames(&p))
}

func TestPlan_RenderPendingRestart(t *testing.T) {
	t.Parallel()

	p := computeHeavy(t, true)

	var buf bytes.Buffer

	require.NoError(t, p.WriteText(&buf))
	assert.Contains(t, buf.String(), "pending-restart mailboxd\n    because: section mta changed")
	assert.Contains(t, buf.String(), "restart      mta\n")

	raw, err := json.Marshal(p.Restarts)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"service":"mailboxd","action":"pending-restart"`)
}
