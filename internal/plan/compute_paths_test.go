// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

func allRunning() map[string]string {
	return map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"}
}

func TestSectionEnabled(t *testing.T) {
	t.Parallel()

	enabled := func(svcs ...string) func(string) bool {
		return func(s string) bool { return set(svcs...)[s] }
	}

	tests := []struct {
		name    string
		section string
		enabled []string
		want    bool
	}{
		{"own name enabled", "mta", []string{"mta"}, true},
		{"own name disabled", "mta", nil, false},
		{"mapped to another service", "amavis", []string{"mta"}, true},
		{"mapped service disabled", "nginx", []string{"mta"}, false},
		{"negated mapping holds when disabled", "dhparam", nil, true},
		{"negated mapping fails when enabled", "dhparam", []string{"dhparam"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, SectionEnabled(tt.section, enabled(tt.enabled...)))
		})
	}
}

func TestCompute_NegatedServiceConditionRemovesTheParameter(t *testing.T) {
	t.Parallel()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {Name: "mta", Conditionals: []config.Conditional{
			{Type: "SERVICE", Key: "antispam", Negated: true, Postconfd: map[string]string{"gone": "x\ny"}},
		}},
	}}

	p, err := Compute(Input{Cur: snap([]string{"mta"}, nil), Conf: conf, FirstRun: true})
	require.NoError(t, err)

	require.Len(t, p.Postconfd, 1)
	assert.Equal(t, "gone", p.Postconfd[0].Key)
	assert.Equal(t, "x y", p.Postconfd[0].Value)
	assert.Equal(t, []string{"mta"}, p.Postconfd[0].Sections)
}

func TestCompute_FirstRunTracksServiceStatus(t *testing.T) {
	t.Parallel()

	cur := snapshot.New(snapshot.Parts{Services: map[string]string{"mta": "TRUE", "antispam": "FALSE"}})

	p, err := Compute(Input{Cur: cur, FirstRun: true})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"mta": "running", "antispam": "stopped"}, p.Tracked)
	assert.Empty(t, p.Restarts, "first run restarts nothing")
}

func TestCompute_DisabledServiceStopMergesWithSectionRestart(t *testing.T) {
	t.Parallel()

	// antivirus is tracked but no longer enabled; the amavis section also
	// asks for its stop. One entry remains, carrying both reasons.
	services := []string{"mta", "proxy", "antispam"}
	prev := snap(services, baseVars())
	cur := snap(services, changed(baseVars(), "zimbraAmavisX", "2"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allRunning()})
	require.NoError(t, err)

	var av *Restart

	for i := range p.Restarts {
		if p.Restarts[i].Service == "antivirus" {
			av = &p.Restarts[i]
		}
	}

	require.NotNil(t, av)
	assert.Equal(t, ActionStop, av.Action)
	assert.Equal(t, []string{"amavis"}, av.Sections)

	kinds := []ReasonKind{}
	for _, r := range av.Reasons {
		kinds = append(kinds, r.Kind)
	}

	assert.Contains(t, kinds, ReasonVarChanged)
	assert.Contains(t, kinds, ReasonServiceDisabled)
}

func TestCompute_CarryMergesWithFreshActions(t *testing.T) {
	t.Parallel()

	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(changed(baseVars(), "zimbraMtaRestriction", "b"), "zimbraProxyX", "q"))

	carry := Carry{
		Rewrites:  []Rewrite{{Source: "conf/main.cf.in", Target: "stale"}},
		Postconf:  []Postconf{{Key: "smtpd_restrictions", Value: "stale"}},
		Postconfd: []Postconfd{{Key: "old_param"}, {Key: "carried_only"}},
		Ldap:      []Ldap{{Key: "zimbraMtaMyNetworks", Value: "stale"}},
		Proxygen:  &Proxygen{Sections: []string{"stale"}},
		Restarts:  []Restart{{Service: "mta", Action: ActionRestart}, {Service: "antispam", Action: ActionStop}},
	}

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: allRunning(), Carry: carry})
	require.NoError(t, err)

	last := func(r []Reason) ReasonKind { return r[len(r)-1].Kind }

	assert.Equal(t, "conf/main.cf", p.Rewrites[0].Target, "the fresh action wins over the carried one")
	assert.Equal(t, ReasonRetry, last(p.Rewrites[0].Reasons))
	assert.Equal(t, "VAR:zimbraMtaRestriction", p.Postconf[0].Value)
	assert.Equal(t, ReasonRetry, last(p.Postconf[0].Reasons))

	require.Len(t, p.Postconfd, 2)
	assert.Equal(t, "carried_only", p.Postconfd[0].Key)
	assert.Equal(t, []Reason{{Kind: ReasonRetry}}, p.Postconfd[0].Reasons)
	assert.Equal(t, ReasonRetry, last(p.Postconfd[1].Reasons))

	assert.Equal(t, ReasonRetry, last(p.Ldap[0].Reasons))
	assert.Equal(t, []string{"proxy"}, p.Proxygen.Sections, "fresh proxygen is kept")
	assert.Equal(t, ReasonRetry, last(p.Proxygen.Reasons))

	assert.Equal(t, map[string]Action{"mta": ActionRestart, "antispam": ActionStop, "proxy": ActionRestart}, restartNames(&p))
}

// driftConf has one section with every kind of action and two sections'
// worth of mapped files, rewrites, removals and LDAP writes (two of each so
// the ordering is observable).
func driftConf() *config.MtaConfig {
	return &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name:     "mta",
			Restarts: set("mta"),
			RequiredVars: map[string]string{
				"zimbraSSLDHParam": "MAPFILE", "!shared": "MAPLOCAL", "other": "MAPFILE",
			},
			Rewrites:  map[string]config.RewriteEntry{"b.in": {Value: "b"}, "a.in": {Value: "a"}},
			Postconf:  map[string]string{"pb": "1", "pa": "2"},
			Postconfd: map[string]string{"db": "", "da": ""},
			Ldap:      map[string]string{"lb": "1", "la": "2"},
			Proxygen:  true,
		},
		"zextras": {Name: "zextras", RequiredVars: map[string]string{"shared": "MAPFILE", "zv": "VAR"}},
	}}
}

// driftInput changes only the mta section; with zextrasChanged the zextras
// section changes too.
func driftInput(zextrasChanged bool) Input {
	zv := "same"
	if zextrasChanged {
		zv = "new"
	}

	obs := func(v, z string) map[snapshot.Lookup]string {
		return map[snapshot.Lookup]string{
			{Type: snapshot.TypeMapFile, Key: "zimbraSSLDHParam"}: v,
			{Type: snapshot.TypeMapFile, Key: "other"}:            "o",
			{Type: snapshot.TypeVar, Key: "zv"}:                   z,
		}
	}
	svcs := snap([]string{"mta", "zextras"}, nil)

	return Input{
		Prev: svcs.WithObserved(obs("old", "same")), Cur: svcs.WithObserved(obs("new", zv)),
		Conf: driftConf(), Tracked: map[string]string{"mta": "running", "zextras": "running"},
	}
}

func TestCompute_ActionsAreSortedByKey(t *testing.T) {
	t.Parallel()

	p, err := Compute(driftInput(true))
	require.NoError(t, err)

	keys := func(n int, f func(i int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = f(i)
		}

		return out
	}

	assert.Equal(t, []string{"a.in", "b.in"}, keys(len(p.Rewrites), func(i int) string { return p.Rewrites[i].Source }))
	assert.Equal(t, []string{"pa", "pb"}, keys(len(p.Postconf), func(i int) string { return p.Postconf[i].Key }))
	assert.Equal(t, []string{"da", "db"}, keys(len(p.Postconfd), func(i int) string { return p.Postconfd[i].Key }))
	assert.Equal(t, []string{"la", "lb"}, keys(len(p.Ldap), func(i int) string { return p.Ldap[i].Key }))
	assert.Equal(t, []string{"other", "shared", "shared", "zimbraSSLDHParam"},
		keys(len(p.Mapfiles), func(i int) string { return p.Mapfiles[i].Key }))
	assert.False(t, p.Mapfiles[1].Local, "for one key the remote sync comes before the local check")
	assert.True(t, p.Mapfiles[2].Local)
}

func TestCompute_HostKeepsOnlyDriftedTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host Host
		want func(t *testing.T, p *Plan)
	}{
		{
			name: "in sync drops every action",
			host: Host{},
			want: func(t *testing.T, p *Plan) {
				t.Helper()
				assert.True(t, p.IsEmpty())
				assert.Empty(t, p.Heal)
				assert.Empty(t, p.Sections)
			},
		},
		{
			name: "mapped file drift replaces the reasons of the changed sync",
			host: Host{Mapfiles: map[string]string{"zimbraSSLDHParam": "file missing"}},
			want: func(t *testing.T, p *Plan) {
				t.Helper()
				require.Len(t, p.Mapfiles, 1)
				assert.Equal(t, "zimbraSSLDHParam", p.Mapfiles[0].Key)
				assert.Equal(t, []Reason{{Kind: ReasonDrift, Detail: "file missing"}}, p.Mapfiles[0].Reasons)
				assert.Equal(t, map[string]Action{"mta": ActionRestart}, restartNames(p))
			},
		},
		{
			name: "heal-only drift becomes a regular mapfile write",
			host: Host{Mapfiles: map[string]string{"shared": "edited by hand"}},
			want: func(t *testing.T, p *Plan) {
				t.Helper()
				require.Len(t, p.Mapfiles, 2, "the promoted remote sync and the local check of the same name")
				assert.Equal(t, "shared", p.Mapfiles[0].Key)
				assert.Equal(t, []string{"zextras"}, p.Mapfiles[0].Sections)
				assert.Empty(t, p.Heal)
			},
		},
		{
			name: "directive and proxygen drift",
			host: Host{
				Postconf:  map[string]string{"pa": "differs"},
				Postconfd: map[string]string{"db": "still set"},
				Proxygen:  "proxy config differs",
			},
			want: func(t *testing.T, p *Plan) {
				t.Helper()
				require.Len(t, p.Postconf, 1)
				assert.Equal(t, "pa", p.Postconf[0].Key)
				require.Len(t, p.Postconfd, 1)
				assert.Equal(t, "db", p.Postconfd[0].Key)
				require.NotNil(t, p.Proxygen)
				assert.Equal(t, "drift: proxy config differs", p.Proxygen.Reasons[0].String())
				assert.Empty(t, p.Rewrites)
				assert.Empty(t, p.Mapfiles)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := driftInput(false)
			in.Host = &tt.host

			p, err := Compute(in)
			require.NoError(t, err)

			tt.want(t, &p)
		})
	}
}
