// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var allTracked = map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"}

func deferredActions(p *Plan) map[string]Action {
	out := map[string]Action{}
	for _, r := range p.DeferredRestarts {
		out[r.Service] = r.Action
	}

	return out
}

func TestCompute_RequestedSectionWithChangeDefersItsRestarts(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", "b"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allTracked, Requested: []string{"mta"}})
	require.NoError(t, err)

	assert.Equal(t, []string{"conf/main.cf.in"}, rewriteSources(&p), "the section is still rewritten")
	assert.Empty(t, p.Restarts, "nothing restarts in the REWRITE cycle itself")
	assert.Equal(t, map[string]Action{"mta": ActionRestart}, deferredActions(&p))

	d := p.DeferredRestarts[0]
	assert.Equal(t, []string{"mta"}, d.Sections)
	assert.Equal(t, []Reason{
		varChanged("mta", "VAR", "zimbraMtaRestriction", "a", "b"),
		{Kind: ReasonDeferred, Section: "mta"},
	}, d.Reasons, "only the real change is kept, not the 'requested' reason")
	assert.False(t, p.IsEmpty())
}

func TestCompute_RequestedSectionDefersAllItsRestartKinds(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(baseVars(), "zimbraAmavisX", "2"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allTracked, Requested: []string{"amavis"}})
	require.NoError(t, err)

	assert.Empty(t, p.Restarts)
	assert.Equal(t, map[string]Action{
		"antivirus": ActionRestart,
		"amavis":    ActionStop, // not an enabled service on this node
		"mta":       ActionRestart,
	}, deferredActions(&p))

	for _, d := range p.DeferredRestarts {
		assert.Equal(t, []string{"amavis"}, d.Sections)
	}
}

func TestCompute_RequestedSectionWithoutChangeDefersNothing(t *testing.T) {
	t.Parallel()

	// The ExecStartPre REWRITE of a starting service: nothing changed, so
	// neither a restart nor a deferral may come out of it.
	s := snap(allServices, baseVars())

	p, err := Compute(Input{Prev: s, Cur: s, Conf: mtaConf(), Tracked: allTracked, Requested: []string{"mta", "amavis"}})
	require.NoError(t, err)

	assert.NotEmpty(t, p.Rewrites)
	assert.Empty(t, p.Restarts)
	assert.Empty(t, p.DeferredRestarts)
}

func TestCompute_ForcedSectionWithChangeDefersItsRestarts(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", "b"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allTracked, Forced: []string{"mta"}})
	require.NoError(t, err)

	assert.Empty(t, p.Restarts)
	assert.Equal(t, map[string]Action{"mta": ActionRestart}, deferredActions(&p))
}

func TestCompute_ForcedSectionWithoutChangeDefersNothing(t *testing.T) {
	t.Parallel()

	s := snap(allServices, baseVars())

	p, err := Compute(Input{Prev: s, Cur: s, Conf: mtaConf(), Tracked: allTracked, Forced: []string{"mta"}})
	require.NoError(t, err)

	assert.Empty(t, p.Restarts)
	assert.Empty(t, p.DeferredRestarts)
}

func TestCompute_CollateralSectionStillRestartsImmediately(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	// REWRITE mta; the change is in amavis, a section nobody asked for.
	cur := snap(allServices, changed(baseVars(), "zimbraAmavisX", "2"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allTracked, Requested: []string{"mta"}})
	require.NoError(t, err)

	assert.Equal(t, ActionRestart, restartNames(&p)["mta"])
	assert.Equal(t, ActionRestart, restartNames(&p)["antivirus"])
	assert.Empty(t, p.DeferredRestarts)
}

func TestCompute_FirstRunDefersNothing(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", "b"))

	p, err := Compute(Input{
		Prev: prev, Cur: cur, Conf: mtaConf(), FirstRun: true, Requested: []string{"mta"}, Forced: []string{"mta"},
	})
	require.NoError(t, err)

	assert.Empty(t, p.Restarts)
	assert.Empty(t, p.DeferredRestarts)
}

func TestCompute_PeriodicCycleChangeRestartsImmediately(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", "b"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allTracked})
	require.NoError(t, err)

	assert.Equal(t, ActionRestart, restartNames(&p)["mta"])
	assert.Empty(t, p.DeferredRestarts)
}

func TestCompute_DeferredRestartIsClassifiedOnSystemd(t *testing.T) {
	t.Parallel()

	conf := mtaConf()
	conf.Sections["mta"].Restarts = set("mta", "mailbox")

	services := append([]string{"mailbox"}, allServices...)
	prev := snap(services, baseVars())
	cur := snap(services, changed(baseVars(), "zimbraMtaRestriction", "b"))

	p, err := Compute(Input{
		Prev: prev, Cur: cur, Conf: conf, Systemd: true, Requested: []string{"mta"},
		Tracked: map[string]string{
			"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running", "mailbox": "running",
		},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]Action{"mta": ActionRestart, "mailbox": ActionPendingRestart}, deferredActions(&p))
}

func TestCompute_DeferredRestartsAreInTheTextAndJSONOutput(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", "b"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allTracked, Requested: []string{"mta"}})
	require.NoError(t, err)

	var text bytes.Buffer

	require.NoError(t, p.WriteText(&text))
	assert.Contains(t, text.String(), "deferred     restart mta\n"+
		"    because: section mta changed because VAR zimbraMtaRestriction changed 'a'->'b'\n"+
		"    because: restart deferred from the REWRITE of section mta\n")

	data, err := p.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"deferred_restarts": [`)

	// A plan whose only content is a deferral is not reported as "No changes".
	only := Plan{DeferredRestarts: p.DeferredRestarts}

	text.Reset()
	require.NoError(t, only.WriteText(&text))
	assert.NotContains(t, text.String(), "No changes")
}

func TestPlan_NoDeferralsRendersAnEmptyList(t *testing.T) {
	t.Parallel()

	s := snap(allServices, baseVars())

	p, err := Compute(Input{Prev: s, Cur: s, Conf: mtaConf(), Tracked: allTracked})
	require.NoError(t, err)

	assert.NotNil(t, p.DeferredRestarts)

	data, err := p.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"deferred_restarts": []`)
}

func TestMergeRestarts(t *testing.T) {
	t.Parallel()

	held := []Restart{
		{Service: "mta", Action: ActionRestart, Sections: []string{"amavis"}, Reasons: []Reason{{Kind: ReasonDeferred, Section: "amavis"}}},
		{Service: "antivirus", Action: ActionRestart, Sections: []string{"amavis"}, Reasons: []Reason{{Kind: ReasonDeferred, Section: "amavis"}}},
	}
	fresh := []Restart{
		{Service: "mta", Action: ActionStop, Sections: []string{"mta"}, Reasons: []Reason{{Kind: ReasonServiceDisabled, Service: "mta"}}},
		{Service: "mailbox", Action: ActionRestart, Reasons: []Reason{{Kind: ReasonVarChanged, Section: "mailbox"}}},
	}

	got := MergeRestarts(held, fresh, true)

	require.Len(t, got, 3)
	assert.Equal(t, []string{"antivirus", "mailbox", "mta"}, []string{got[0].Service, got[1].Service, got[2].Service})
	assert.Equal(t, ActionPendingRestart, got[1].Action, "classified for a systemd host")
	assert.Equal(t, ActionStop, got[2].Action, "the fresher action wins")
	assert.Equal(t, []string{"amavis", "mta"}, got[2].Sections)
	assert.Equal(t, []Reason{
		{Kind: ReasonDeferred, Section: "amavis"},
		{Kind: ReasonServiceDisabled, Service: "mta"},
	}, got[2].Reasons, "stale reasons first")

	// The inputs are left alone.
	assert.Equal(t, ActionRestart, held[0].Action)
	assert.Equal(t, []string{"amavis"}, held[0].Sections)
	assert.Len(t, held[0].Reasons, 1)

	assert.Empty(t, MergeRestarts(nil, nil, false))
}

func TestMergeRestarts_SameReasonIsNotDuplicated(t *testing.T) {
	t.Parallel()

	r := Restart{Service: "mta", Action: ActionRestart, Sections: []string{"mta"}, Reasons: []Reason{{Kind: ReasonDeferred, Section: "mta"}}}

	got := MergeRestarts([]Restart{r}, []Restart{r}, false)

	require.Len(t, got, 1)
	assert.Len(t, got[0].Reasons, 1)
	assert.Equal(t, []string{"mta"}, got[0].Sections)
}

func TestReason_DeferredString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "restart deferred from the REWRITE of section amavis",
		Reason{Kind: ReasonDeferred, Section: "amavis"}.String())
}
