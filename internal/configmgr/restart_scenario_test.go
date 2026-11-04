// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/testutil"
)

// TestCycle_AmavisChangeRestartsEachServiceOnce replays the container
// differential: after `zmprov mcf zimbraAmavisMaxServers 12` the legacy
// zmconfigd restarts antivirus, amavis and mta exactly once each, although
// the sections carry DEPENDS. Legacy only cascades DEPENDS after a stop or
// start, never after a restart, so the config-driven path must neither queue
// a dependent a second time nor hand the DEPENDS graph to the service manager.
func TestCycle_AmavisChangeRestartsEachServiceOnce(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"amavis": {
			Name:         "amavis",
			RequiredVars: map[string]string{"zimbraAmavisMaxServers": configTypeVAR},
			Restarts:     map[string]bool{"amavis": true, "antivirus": true, "mta": true},
			Depends:      map[string]bool{"antivirus": true, "mta": true},
		},
		"antivirus": {
			Name:         "antivirus",
			RequiredVars: map[string]string{"zimbraAntivirusX": configTypeVAR},
			Depends:      map[string]bool{"amavis": true},
		},
		"mta": {
			Name:         "mta",
			RequiredVars: map[string]string{"zimbraMtaX": configTypeVAR},
			Depends:      map[string]bool{"amavis": true},
		},
	}}

	cm, _ := newPlanCM(t, conf)

	var (
		added      []string
		processed  int
		depsCalled int
	)

	cm.ServiceMgr = &testutil.MockServiceManager{
		AddRestartWithReasonsFn: func(_ context.Context, service string, _ []string) error {
			added = append(added, service)

			return nil
		},
		ProcessRestartsFn: func(_ context.Context, _ func(string) string) error {
			processed++

			return nil
		},
		SetDependenciesFn: func(context.Context, map[string][]string) { depsCalled++ },
	}
	cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)
	cm.mainConfig.RestartConfig = true

	setGlobal(cm, map[string]string{"zimbraAmavisMaxServers": "10", "zimbraAntivirusX": "a", "zimbraMtaX": "m"})
	setServer(cm, nil, enabled("amavis", "antivirus", "mta"))
	mustCycle(t, cm)
	cm.State.SetFirstRun(false)

	added = nil
	processed = 0

	setGlobal(cm, map[string]string{"zimbraAmavisMaxServers": "12", "zimbraAntivirusX": "a", "zimbraMtaX": "m"})

	pl := mustCycle(t, cm)

	require.Equal(t, []string{"amavis"}, sectionNames(pl))
	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, added, "each service queued exactly once")
	assert.Equal(t, 1, processed)
	assert.Zero(t, depsCalled, "config-driven restarts must not cascade through DEPENDS")
}

// systemdFakeManager is a service manager of a systemd host: it records the
// actions the restart policy carries out on units.
type systemdFakeManager struct {
	testutil.MockServiceManager

	applied []string
	queued  []string
}

func (m *systemdFakeManager) IsSystemdMode() bool { return true }

func (m *systemdFakeManager) ApplySystemd(_ context.Context, service string, action plan.Action) error {
	m.applied = append(m.applied, string(action)+":"+service)

	return nil
}

// TestCycle_SystemdHostFlagsHeavyServicesAndReloadsTheRest runs a whole cycle
// on a systemd host: the plan (what "configd plan" prints) shows the heavy
// services as pending-restart, and the policy only acts on the others, through
// systemctl try-reload-or-restart, never through the restart queue.
func TestCycle_SystemdHostFlagsHeavyServicesAndReloadsTheRest(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name:         "mta",
			RequiredVars: map[string]string{"zimbraMtaX": configTypeVAR},
			Restarts:     map[string]bool{"mta": true, "mailboxd": true, "ldap": true, "memcached": true},
		},
	}}

	cm, _ := newPlanCM(t, conf)

	mgr := &systemdFakeManager{}
	mgr.AddRestartWithReasonsFn = func(_ context.Context, service string, _ []string) error {
		mgr.queued = append(mgr.queued, service)

		return nil
	}
	cm.ServiceMgr = mgr
	cm.policy = restart.New(mgr, cm.env.Clock).WithSystemd(mgr)
	cm.mainConfig.RestartConfig = true

	setGlobal(cm, map[string]string{"zimbraMtaX": "1"})
	setServer(cm, nil, enabled("mta", "mailboxd", "ldap", "memcached"))
	mustCycle(t, cm)
	cm.State.SetFirstRun(false)

	mgr.applied, mgr.queued = nil, nil

	setGlobal(cm, map[string]string{"zimbraMtaX": "2"})

	pl := mustCycle(t, cm)

	got := map[string]plan.Action{}
	for _, r := range pl.Restarts {
		got[r.Service] = r.Action
	}

	assert.Equal(t, map[string]plan.Action{
		"ldap":      plan.ActionPendingRestart,
		"mailboxd":  plan.ActionPendingRestart,
		"memcached": plan.ActionRestart,
		"mta":       plan.ActionRestart,
	}, got)
	assert.Equal(t, []string{"restart:memcached", "restart:mta"}, mgr.applied)
	assert.Empty(t, mgr.queued, "systemd hosts do not use the legacy restart queue")
	assert.Empty(t, cm.carry.Restarts, "flagged and applied restarts are consumed")
}

// TestSystemdMode pins which service managers put the config manager (and so
// `configd plan`) in systemd mode.
func TestSystemdMode_FalseForManagersWithoutTheNotion(t *testing.T) {
	// A manager the policy can ask about systemd gets the systemd rules; a test
	// double without the notion never does (see NewConfigManager).
	cm := &ConfigManager{ServiceMgr: &testutil.MockServiceManager{}, policy: restart.New(nil, nil)}
	assert.False(t, cm.systemdMode())

	sd := &systemdFakeManager{}
	cm = &ConfigManager{ServiceMgr: sd, policy: restart.New(nil, nil).WithSystemd(sd)}
	assert.True(t, cm.systemdMode())
}
