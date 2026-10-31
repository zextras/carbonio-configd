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
