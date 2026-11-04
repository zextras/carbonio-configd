// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/testutil"
)

// restartLog records the services the restart policy queues and the reasons
// they were queued with.
type restartLog struct {
	services []string
	reasons  map[string][]string
}

func (l *restartLog) reset() {
	l.services, l.reasons = nil, map[string][]string{}
}

// newDeferredCM builds a cycle-ready manager on a fully enabled node whose
// amavis section restarts amavis, antivirus and mta, past its first run.
func newDeferredCM(t *testing.T) (*ConfigManager, *restartLog) {
	t.Helper()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"amavis": {
			Name:         "amavis",
			RequiredVars: map[string]string{"zimbraMtaMyNetworks": configTypeVAR},
			Restarts:     map[string]bool{"amavis": true, "antivirus": true, "mta": true},
		},
		"proxy": {
			Name:         "proxy",
			RequiredVars: map[string]string{"zimbraProxyX": configTypeVAR},
			Restarts:     map[string]bool{"proxy": true},
		},
	}}

	cm, _ := newPlanCM(t, conf)
	log := &restartLog{}
	log.reset()

	cm.ServiceMgr = &testutil.MockServiceManager{
		AddRestartWithReasonsFn: func(_ context.Context, service string, reasons []string) error {
			log.services = append(log.services, service)
			log.reasons[service] = reasons

			return nil
		},
	}
	cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)
	cm.mainConfig.RestartConfig = true

	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "10.0.0.0/8", "zimbraProxyX": "p"})
	setServer(cm, nil, enabled("amavis", "antivirus", "mta", "proxy"))
	mustCycle(t, cm)
	cm.State.SetFirstRun(false)
	log.reset()

	return cm, log
}

func requestRewrite(cm *ConfigManager, sections ...string) {
	cm.State.AddRequestedConfigs(context.Background(), sections)
}

func heldServices(cm *ConfigManager) []string {
	out := []string{}
	for _, r := range cm.deferred {
		out = append(out, r.Service)
	}

	return out
}

// TestCycle_RestartDeferredFromRewriteRunsInNextPeriodicCycle is the CO-4566
// scenario: a variable changes and `configd rewrite amavis` rewrites the
// section in the same cycle. The restarts must not be lost with the consumed
// change; the next periodic cycle runs them, once.
func TestCycle_RestartDeferredFromRewriteRunsInNextPeriodicCycle(t *testing.T) {
	cm, log := newDeferredCM(t)

	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "10.0.0.0/8 192.168.0.0/16", "zimbraProxyX": "p"})
	requestRewrite(cm, "amavis")

	var buf bytes.Buffer

	pl, err := cm.Plan(captureLogCtx(&buf))
	require.NoError(t, err)
	require.NoError(t, cm.Execute(context.Background(), pl))
	cm.ApplyRestarts(context.Background(), pl)

	assert.Empty(t, pl.Restarts, "the REWRITE cycle does not restart")
	assert.Empty(t, log.services)
	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, heldServices(cm))
	assert.Contains(t, buf.String(), `msg="Config changed in a REWRITE cycle; restart deferred to next cycle"`)
	assert.Contains(t, buf.String(), `section=amavis`)
	assert.Contains(t, buf.String(), `services="[amavis antivirus mta]"`)
	assert.Contains(t, buf.String(), "zimbraMtaMyNetworks changed '10.0.0.0/8'->'10.0.0.0/8 192.168.0.0/16'")
	assert.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("restart deferred to next cycle")), "once per section")

	// Another REWRITE cycle, with nothing changed: the held restarts stay held.
	requestRewrite(cm, "amavis")

	pl = mustCycle(t, cm)
	assert.Empty(t, pl.Restarts)
	assert.Empty(t, pl.DeferredRestarts)
	assert.Empty(t, log.services)
	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, heldServices(cm))

	// The next periodic cycle runs them.
	buf.Reset()

	pl, err = cm.Plan(captureLogCtx(&buf))
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Executing restart deferred from a REWRITE cycle")
	require.NoError(t, cm.Execute(context.Background(), pl))
	cm.ApplyRestarts(context.Background(), pl)

	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, log.services)
	assert.Empty(t, cm.deferred, "cleared once handed to the restart policy")
	assert.Empty(t, cm.carry.Restarts)

	for _, service := range log.services {
		assert.Contains(t, log.reasons[service],
			"section amavis changed because VAR zimbraMtaMyNetworks changed '10.0.0.0/8'->'10.0.0.0/8 192.168.0.0/16'")
		assert.Contains(t, log.reasons[service], "restart deferred from the REWRITE of section amavis")
	}

	// And nothing more afterwards.
	log.reset()
	mustCycle(t, cm)
	assert.Empty(t, log.services)
}

// TestCycle_RestartedServiceRewriteDoesNotLoop checks the loop safety: the
// restart a deferral causes makes the unit's ExecStartPre send REWRITE <svc>
// again; with no variable change it must neither restart nor defer.
func TestCycle_RestartedServiceRewriteDoesNotLoop(t *testing.T) {
	cm, log := newDeferredCM(t)

	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "changed", "zimbraProxyX": "p"})
	requestRewrite(cm, "amavis")
	mustCycle(t, cm)
	require.NotEmpty(t, cm.deferred)

	mustCycle(t, cm) // periodic: the deferred restarts run
	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, log.services)

	log.reset()

	for range 3 {
		// ExecStartPre of each restarted unit.
		requestRewrite(cm, "amavis")

		pl := mustCycle(t, cm)

		assert.Empty(t, pl.Restarts)
		assert.Empty(t, pl.DeferredRestarts)
		assert.Empty(t, cm.deferred)

		pl = mustCycle(t, cm) // periodic
		assert.Empty(t, pl.Restarts)
	}

	assert.Empty(t, log.services, "the loop ended with the first restart")
}

// TestCycle_DeferredRestartMergesWithTheRestartsOfTheCycle checks that a
// service due both ways restarts once, with the reasons of both.
func TestCycle_DeferredRestartMergesWithTheRestartsOfTheCycle(t *testing.T) {
	cm, log := newDeferredCM(t)

	// amavis changed during REWRITE amavis ...
	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "changed", "zimbraProxyX": "p"})
	requestRewrite(cm, "amavis")
	mustCycle(t, cm)

	// ... and the proxy variable changes in a periodic cycle.
	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "changed", "zimbraProxyX": "q"})

	pl := mustCycle(t, cm)

	got := map[string]plan.Action{}
	for _, r := range pl.Restarts {
		got[r.Service] = r.Action
	}

	assert.Equal(t, map[string]plan.Action{
		"amavis": plan.ActionRestart, "antivirus": plan.ActionRestart,
		"mta": plan.ActionRestart, "proxy": plan.ActionRestart,
	}, got)
	assert.Equal(t, []string{"amavis", "antivirus", "mta", "proxy"}, log.services)
}

// TestCycle_DeferredRestartsOfTwoRewriteCyclesAreMerged holds the restarts of
// the same service from two REWRITE cycles as one entry.
func TestCycle_DeferredRestartsOfTwoRewriteCyclesAreMerged(t *testing.T) {
	cm, log := newDeferredCM(t)

	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "one", "zimbraProxyX": "p"})
	requestRewrite(cm, "amavis")
	mustCycle(t, cm)

	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "two", "zimbraProxyX": "p"})
	requestRewrite(cm, "amavis")
	mustCycle(t, cm)

	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, heldServices(cm))
	assert.Empty(t, log.services)

	mustCycle(t, cm)

	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, log.services, "each service restarts once")
	assert.Empty(t, cm.deferred)
}

// TestCycle_DeferredRestartIsKeptWhileRestartsAreDisabled checks the hand-over:
// with config restarts disabled the policy returns the restarts as carry, so a
// released deferral is neither lost nor run.
func TestCycle_DeferredRestartIsKeptWhileRestartsAreDisabled(t *testing.T) {
	cm, log := newDeferredCM(t)
	cm.mainConfig.RestartConfig = false

	setGlobal(cm, map[string]string{"zimbraMtaMyNetworks": "changed", "zimbraProxyX": "p"})
	requestRewrite(cm, "amavis")
	mustCycle(t, cm)

	mustCycle(t, cm)

	assert.Empty(t, cm.deferred)
	assert.Empty(t, log.services)
	require.Len(t, cm.carry.Restarts, 3, "carried until restarts are enabled")

	cm.mainConfig.RestartConfig = true

	mustCycle(t, cm)

	assert.Equal(t, []string{"amavis", "antivirus", "mta"}, log.services)
}

// TestCycle_RequestedSectionWithoutChangeKeepsNothing is the ExecStartPre case
// on its own: no change, no restart, no deferral.
func TestCycle_RequestedSectionWithoutChangeKeepsNothing(t *testing.T) {
	cm, log := newDeferredCM(t)

	requestRewrite(cm, "amavis", "proxy")

	pl := mustCycle(t, cm)

	assert.Empty(t, pl.Restarts)
	assert.Empty(t, pl.DeferredRestarts)
	assert.Empty(t, cm.deferred)
	assert.Empty(t, log.services)
}

// TestCycle_DeferredRestartIsClassifiedOnSystemd runs the release through the
// systemd rules: a heavy service is only flagged, the rest is reloaded.
func TestCycle_DeferredRestartIsClassifiedOnSystemd(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name:         "mta",
			RequiredVars: map[string]string{"zimbraMtaX": configTypeVAR},
			Restarts:     map[string]bool{"mta": true, "mailboxd": true},
		},
	}}

	cm, _ := newPlanCM(t, conf)

	mgr := &systemdFakeManager{}
	cm.ServiceMgr = mgr
	cm.policy = restart.New(mgr, cm.env.Clock).WithSystemd(mgr)
	cm.mainConfig.RestartConfig = true

	setGlobal(cm, map[string]string{"zimbraMtaX": "1"})
	setServer(cm, nil, enabled("mta", "mailboxd"))
	mustCycle(t, cm)
	cm.State.SetFirstRun(false)

	setGlobal(cm, map[string]string{"zimbraMtaX": "2"})
	requestRewrite(cm, "mta")

	pl := mustCycle(t, cm)
	require.Len(t, pl.DeferredRestarts, 2)
	assert.Empty(t, mgr.applied)

	pl = mustCycle(t, cm)

	got := map[string]plan.Action{}
	for _, r := range pl.Restarts {
		got[r.Service] = r.Action
	}

	assert.Equal(t, map[string]plan.Action{"mta": plan.ActionRestart, "mailboxd": plan.ActionPendingRestart}, got)
	assert.Equal(t, []string{"restart:mta"}, mgr.applied)
}
