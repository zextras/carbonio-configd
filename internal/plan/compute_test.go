// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/snapshot"
)

// snap builds a snapshot with the given enabled services, VAR values and
// observed values for every VAR (so Observed mirrors what the daemon records).
func snap(services []string, vars map[string]string) *snapshot.Snapshot {
	svc := map[string]string{}
	for _, s := range services {
		svc[s] = "zimbraServiceEnabled"
	}

	obs := map[snapshot.Lookup]string{}
	for k, v := range vars {
		obs[snapshot.Lookup{Type: snapshot.TypeVar, Key: k}] = v
	}

	return snapshot.New(snapshot.Parts{Global: vars, Services: svc}).WithObserved(obs)
}

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}

	return m
}

// mtaConf is a small configd.cf: mta (restarts mta, depends on amavis),
// amavis (restarts amavis antivirus mta) and an unrelated proxy section.
func mtaConf() *config.MtaConfig {
	return &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name:         "mta",
			Depends:      set("amavis"),
			Restarts:     set("mta"),
			RequiredVars: map[string]string{"zimbraMtaRestriction": "VAR"},
			Rewrites:     map[string]config.RewriteEntry{"conf/main.cf.in": {Value: "conf/main.cf", Mode: "0640"}},
			Postconf:     map[string]string{"smtpd_restrictions": "VAR:zimbraMtaRestriction"},
			Postconfd:    map[string]string{"old_param": ""},
			Ldap:         map[string]string{"zimbraMtaMyNetworks": "LOCAL:mynetworks", "zimbraEmpty": ""},
		},
		"amavis": {
			Name:         "amavis",
			Restarts:     set("antivirus", "amavis", "mta"),
			RequiredVars: map[string]string{"zimbraAmavisX": "VAR"},
		},
		"proxy": {
			Name:         "proxy",
			Restarts:     set("proxy"),
			RequiredVars: map[string]string{"zimbraProxyX": "VAR"},
			Proxygen:     true,
		},
	}}
}

var allServices = []string{"mta", "proxy", "antivirus", "antispam"}

func baseVars() map[string]string {
	return map[string]string{"zimbraMtaRestriction": "a", "zimbraAmavisX": "1", "zimbraProxyX": "p"}
}

func changed(vars map[string]string, k, v string) map[string]string {
	out := map[string]string{}
	for key, val := range vars {
		out[key] = val
	}

	out[k] = v

	return out
}

func restartNames(p *Plan) map[string]Action {
	out := map[string]Action{}
	for _, r := range p.Restarts {
		out[r.Service] = r.Action
	}

	return out
}

func rewriteSources(p *Plan) []string {
	out := []string{}
	for _, r := range p.Rewrites {
		out = append(out, r.Source)
	}

	return out
}

func TestCompute_NoChangeIsEmpty(t *testing.T) {
	t.Parallel()

	s := snap(allServices, baseVars())

	p, err := Compute(Input{Prev: s, Cur: s, Conf: mtaConf(), Tracked: map[string]string{
		"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running",
	}})
	require.NoError(t, err)

	assert.True(t, p.IsEmpty())
	assert.Empty(t, p.Sections)
	assert.Empty(t, p.Restarts)
	assert.Empty(t, p.Cascade)
}

func TestCompute_SingleVarChange(t *testing.T) {
	t.Parallel()

	tracked := map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"}
	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", "b"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: tracked})
	require.NoError(t, err)

	require.Len(t, p.Sections, 1)
	assert.Equal(t, "mta", p.Sections[0].Name)

	assert.Equal(t, []string{"conf/main.cf.in"}, rewriteSources(&p))
	assert.Equal(t, "conf/main.cf", p.Rewrites[0].Target)
	assert.Equal(t, "0640", p.Rewrites[0].Mode)

	require.Len(t, p.Postconf, 1)
	assert.Equal(t, "smtpd_restrictions", p.Postconf[0].Key)
	assert.Equal(t, "VAR:zimbraMtaRestriction", p.Postconf[0].Value)

	require.Len(t, p.Postconfd, 1)
	assert.Equal(t, "old_param", p.Postconfd[0].Key)

	// An empty LDAP value means "no change" and is not planned.
	require.Len(t, p.Ldap, 1)
	assert.Equal(t, "zimbraMtaMyNetworks", p.Ldap[0].Key)

	assert.Nil(t, p.Proxygen, "unrelated proxy section must not be planned")
	assert.Equal(t, map[string]Action{"mta": ActionRestart}, restartNames(&p))

	want := "section mta changed because VAR zimbraMtaRestriction changed 'a'->'b'"
	for _, reasons := range [][]Reason{
		p.Sections[0].Reasons, p.Rewrites[0].Reasons, p.Postconf[0].Reasons, p.Restarts[0].Reasons,
	} {
		require.Len(t, reasons, 1)
		assert.Equal(t, want, reasons[0].String())
	}

	// DEPENDS: restarting mta cascades to amavis, which is always queued.
	require.Len(t, p.Cascade, 1)
	assert.Equal(t, "amavis", p.Cascade[0].Service)
	assert.Equal(t, "mta", p.Cascade[0].Via)
}

func TestCompute_VarChangedFromUnsetAndToUnset(t *testing.T) {
	t.Parallel()

	tracked := map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"}
	withVar := snap(allServices, baseVars())
	without := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", ""))

	for _, tc := range []struct {
		name      string
		prev, cur *snapshot.Snapshot
		want      string
	}{
		{"set to unset", withVar, without, "section mta changed because VAR zimbraMtaRestriction changed 'a'-><unset>"},
		{"unset to set", without, withVar, "section mta changed because VAR zimbraMtaRestriction changed <unset>->'a'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := Compute(Input{Prev: tc.prev, Cur: tc.cur, Conf: mtaConf(), Tracked: tracked})
			require.NoError(t, err)
			require.Len(t, p.Sections, 1)
			assert.Equal(t, tc.want, p.Sections[0].Reasons[0].String())
		})
	}
}

func TestCompute_EmptyToEmptyIsNotAChange(t *testing.T) {
	t.Parallel()

	empty := snap(allServices, changed(baseVars(), "zimbraMtaRestriction", ""))
	p, err := Compute(Input{Prev: empty, Cur: empty, Conf: mtaConf(), Tracked: map[string]string{
		"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running",
	}})
	require.NoError(t, err)
	assert.True(t, p.IsEmpty())
}

func TestCompute_SecretValuesAreRedactedInReasons(t *testing.T) {
	t.Parallel()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {Name: "mta", RequiredVars: map[string]string{"ldap_postfix_password": "VAR"}},
	}}

	p, err := Compute(Input{
		Prev:    snap([]string{"mta"}, map[string]string{"ldap_postfix_password": "hunter2"}),
		Cur:     snap([]string{"mta"}, map[string]string{"ldap_postfix_password": "hunter3"}),
		Conf:    conf,
		Tracked: map[string]string{"mta": "running"},
	})
	require.NoError(t, err)

	data, err := p.JSON()
	require.NoError(t, err)
	assert.NotContains(t, string(data), "hunter")
	assert.Contains(t, p.Sections[0].Reasons[0].String(), "[REDACTED]")
}

func TestCompute_ForcedSection(t *testing.T) {
	t.Parallel()

	tracked := map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"}
	prev := snap(allServices, baseVars())
	// proxy changed too, but is outside the forced scope.
	cur := snap(allServices, changed(baseVars(), "zimbraProxyX", "q"))

	p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: tracked, Forced: []string{"mta"}})
	require.NoError(t, err)

	require.Len(t, p.Sections, 1)
	assert.Equal(t, "mta", p.Sections[0].Name)
	assert.Equal(t, "section mta forced by REWRITE request", p.Sections[0].Reasons[0].String())
	assert.Equal(t, []string{"conf/main.cf.in"}, rewriteSources(&p))
	assert.Empty(t, p.Restarts, "a forced section never restarts its own services")
	assert.Nil(t, p.Proxygen)
}

func TestCompute_RestartSkipIsSectionScoped(t *testing.T) {
	t.Parallel()

	tracked := map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"}
	prev := snap(allServices, baseVars())
	// REWRITE mta arrives while the amavis section's variable also changed.
	cur := snap(allServices, changed(baseVars(), "zimbraAmavisX", "2"))

	p, err := Compute(Input{
		Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: tracked, Requested: []string{"mta"},
	})
	require.NoError(t, err)

	got := restartNames(&p)
	assert.Equal(t, ActionRestart, got["antivirus"])
	assert.Equal(t, ActionStop, got["amavis"], "amavis is not an enabled service on this node")
	assert.Equal(t, ActionRestart, got["mta"], "collateral amavis section restarts mta")

	var names []string
	for _, s := range p.Sections {
		names = append(names, s.Name)
	}

	assert.Equal(t, []string{"amavis", "mta"}, names)
	assert.Equal(t, "section mta requested by REWRITE command", p.Sections[1].Reasons[0].String())
}

func TestCompute_FirstRun(t *testing.T) {
	t.Parallel()

	cur := snap(allServices, baseVars())

	p, err := Compute(Input{Cur: cur, Conf: mtaConf(), FirstRun: true})
	require.NoError(t, err)

	assert.True(t, p.FirstRun)
	// "amavis" is gated on the mta service which is enabled: all three sections run.
	require.Len(t, p.Sections, 3)

	for _, s := range p.Sections {
		require.Len(t, s.Reasons, 1)
		assert.Equal(t, ReasonFirstRun, s.Reasons[0].Kind)
	}

	assert.Empty(t, p.Restarts, "no restarts on first run")
	assert.NotNil(t, p.Proxygen)
	assert.Equal(t, map[string]string{
		"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running",
	}, p.Tracked)
}

func TestCompute_DisabledSectionServiceIsSkipped(t *testing.T) {
	t.Parallel()

	// No proxy service on this node: the proxy section is never processed.
	cur := snap([]string{"mta", "antivirus"}, baseVars())

	p, err := Compute(Input{Cur: cur, Conf: mtaConf(), FirstRun: true})
	require.NoError(t, err)

	for _, s := range p.Sections {
		assert.NotEqual(t, "proxy", s.Name)
	}

	assert.Nil(t, p.Proxygen)
}

func TestCompute_DependencyCascade(t *testing.T) {
	t.Parallel()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"antivirus": {
			Name: "antivirus", Depends: set("amavis"), Restarts: set("antivirus"),
			RequiredVars: map[string]string{"zimbraAv": "VAR"},
		},
		"mta":      {Name: "mta", Depends: set("amavis"), Restarts: set("mta")},
		"opendkim": {Name: "opendkim", Depends: set("mta"), Restarts: set("opendkim")},
	}}
	services := []string{"antivirus", "mta", "opendkim"}
	tracked := map[string]string{"antivirus": "running", "mta": "running", "opendkim": "running"}

	p, err := Compute(Input{
		Prev:    snap(services, map[string]string{"zimbraAv": "1"}),
		Cur:     snap(services, map[string]string{"zimbraAv": "2"}),
		Conf:    conf,
		Tracked: tracked,
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]Action{"antivirus": ActionRestart}, restartNames(&p))
	require.Len(t, p.Cascade, 1)
	assert.Equal(t, Cascade{
		Service: "amavis", Via: "antivirus",
		Reasons: []Reason{{Kind: ReasonDependency, Service: "antivirus"}},
	}, p.Cascade[0])

	// opendkim -> mta (enabled) -> amavis (always): a two-level cascade.
	conf.Sections["opendkim"].RequiredVars = map[string]string{"zimbraOd": "VAR"}
	p, err = Compute(Input{
		Prev:    snap(services, map[string]string{"zimbraOd": "1"}),
		Cur:     snap(services, map[string]string{"zimbraOd": "2"}),
		Conf:    conf,
		Tracked: tracked,
	})
	require.NoError(t, err)
	require.Len(t, p.Cascade, 2)
	assert.Equal(t, "mta", p.Cascade[0].Service)
	assert.Equal(t, "amavis", p.Cascade[1].Service)
	assert.Equal(t, "mta", p.Cascade[1].Via)
}

func TestCompute_DisabledServiceIsNotRestarted(t *testing.T) {
	t.Parallel()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name: "mta", Restarts: set("mta", "antispam", "archiving", "opendkim", "cbpolicyd"),
			RequiredVars: map[string]string{"k": "VAR"},
		},
	}}
	// mta enabled; antispam, archiving, cbpolicyd and opendkim are not.
	services := []string{"mta"}

	p, err := Compute(Input{
		Prev:    snap(services, map[string]string{"k": "1"}),
		Cur:     snap(services, map[string]string{"k": "2"}),
		Conf:    conf,
		Tracked: map[string]string{"mta": "running"},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]Action{
		"mta":       ActionRestart,
		"antispam":  ActionStop,
		"opendkim":  ActionRestart, // mta is enabled: restarted instead of stopped
		"cbpolicyd": ActionStop,
		// archiving: never stopped
	}, restartNames(&p))
}

func TestCompute_ServiceStatusChanges(t *testing.T) {
	t.Parallel()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{}}
	tracked := map[string]string{"mta": "running", "antispam": "running"}

	// antispam disabled, antivirus newly enabled.
	cur := snap([]string{"mta", "antivirus"}, nil)

	p, err := Compute(Input{Prev: cur, Cur: cur, Conf: conf, Tracked: tracked})
	require.NoError(t, err)

	assert.Equal(t, map[string]Action{"antispam": ActionStop, "antivirus": ActionStart}, restartNames(&p))
	assert.Equal(t, "service antispam was disabled", p.Restarts[0].Reasons[0].String())
	assert.Equal(t, "service antivirus was enabled", p.Restarts[1].Reasons[0].String())
	// Tracking only grows on first run: antivirus keeps being started each cycle.
	assert.Equal(t, tracked, p.Tracked)
}

func TestCompute_AllServicesDisabledGuard(t *testing.T) {
	t.Parallel()

	_, err := Compute(Input{
		Cur:     snap(nil, nil),
		Conf:    &config.MtaConfig{},
		Tracked: map[string]string{"mta": "running", "proxy": "running"},
	})
	require.ErrorIs(t, err, ErrAllServicesDisabled)

	// A single tracked service never trips the guard.
	_, err = Compute(Input{
		Cur: snap(nil, nil), Conf: &config.MtaConfig{}, Tracked: map[string]string{"mta": "running"},
	})
	require.NoError(t, err)
}

func TestCompute_Conditionals(t *testing.T) {
	t.Parallel()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {
			Name:     "mta",
			Postconf: map[string]string{"bare": "base"},
			Conditionals: []config.Conditional{
				{Type: "SERVICE", Key: "antispam", Postconf: map[string]string{"bare": "spam"}},
				{Type: "SERVICE", Key: "antispam", Negated: true, Postconfd: map[string]string{"gone": ""}},
				{Type: "VAR", Key: "missing", Postconf: map[string]string{"never": "x"}},
				{Type: "VAR", Key: "!missing", Postconf: map[string]string{"always": "y\nz"}},
			},
		},
	}}

	p, err := Compute(Input{Cur: snap([]string{"mta", "antispam"}, nil), Conf: conf, FirstRun: true})
	require.NoError(t, err)

	got := map[string]string{}
	for _, c := range p.Postconf {
		got[c.Key] = c.Value
	}

	assert.Equal(t, map[string]string{"bare": "spam", "always": "y z"}, got)
	assert.Empty(t, p.Postconfd, "negated SERVICE block does not hold while antispam is enabled")
}

func TestCompute_MapfileOpsAndHeal(t *testing.T) {
	t.Parallel()

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": {Name: "mta", RequiredVars: map[string]string{
			"zimbraSSLDHParam": "MAPFILE", "!mapped.local": "MAPLOCAL", "plain": "VAR",
		}},
	}}
	tracked := map[string]string{"mta": "running"}

	// Unchanged cycle: the MAPFILE is only re-synced (heal), nothing else.
	obs := map[snapshot.Lookup]string{
		{Type: snapshot.TypeMapFile, Key: "zimbraSSLDHParam"}: "/p/dhparam.pem",
		{Type: snapshot.TypeMapLocal, Key: "mapped.local"}:    "/p/local",
		{Type: snapshot.TypeVar, Key: "plain"}:                "x",
	}
	cur := snap([]string{"mta"}, nil).WithObserved(obs)

	p, err := Compute(Input{Prev: cur, Cur: cur, Conf: conf, Tracked: tracked})
	require.NoError(t, err)

	assert.True(t, p.IsEmpty(), "self-heal alone is not a change")
	require.Len(t, p.Heal, 1)
	assert.Equal(t, "zimbraSSLDHParam", p.Heal[0].Key)

	// First run: both are explicit mapfile ops and heal is deduplicated.
	p, err = Compute(Input{Cur: cur, Conf: conf, FirstRun: true})
	require.NoError(t, err)
	assert.Empty(t, p.Heal)
	require.Len(t, p.Mapfiles, 2)
	assert.Equal(t, Mapfile{
		Key: "mapped.local", Local: true, Sections: []string{"mta"},
		Reasons: []Reason{{Kind: ReasonFirstRun, Section: "mta"}},
	}, p.Mapfiles[0])
	assert.Equal(t, "zimbraSSLDHParam", p.Mapfiles[1].Key)
	assert.False(t, p.Mapfiles[1].Local)
}

func TestCompute_CarryIsRetried(t *testing.T) {
	t.Parallel()

	s := snap(allServices, baseVars())
	carry := Carry{
		Rewrites: []Rewrite{{Source: "a.in", Target: "a", Reasons: []Reason{{Kind: ReasonFirstRun, Section: "x"}}}},
		Postconf: []Postconf{{Key: "k", Value: "v"}},
		Ldap:     []Ldap{{Key: "l", Value: "v"}},
		Proxygen: &Proxygen{},
		Restarts: []Restart{{Service: "mta", Action: ActionRestart}},
	}

	p, err := Compute(Input{Prev: s, Cur: s, Conf: mtaConf(), Carry: carry, Tracked: map[string]string{
		"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running",
	}})
	require.NoError(t, err)

	assert.Equal(t, []string{"a.in"}, rewriteSources(&p))
	assert.Equal(t, ReasonRetry, p.Rewrites[0].Reasons[len(p.Rewrites[0].Reasons)-1].Kind)
	assert.Len(t, p.Postconf, 1)
	assert.Len(t, p.Ldap, 1)
	assert.NotNil(t, p.Proxygen)
	assert.Equal(t, map[string]Action{"mta": ActionRestart}, restartNames(&p))
	assert.False(t, p.IsEmpty())
}

func TestCompute_HostPrunesToDrift(t *testing.T) {
	t.Parallel()

	s := snap(allServices, baseVars())
	in := Input{
		Prev: s, Cur: s, Conf: mtaConf(), AssumeChanged: true,
		Tracked: map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"},
		Scope:   []string{"mta"},
	}

	candidate, err := Compute(in)
	require.NoError(t, err)
	assert.Equal(t, []string{"conf/main.cf.in"}, rewriteSources(&candidate))
	assert.Equal(t, map[string]Action{"mta": ActionRestart}, restartNames(&candidate))

	// Everything in sync: nothing to do.
	in.Host = &Host{}
	synced, err := Compute(in)
	require.NoError(t, err)
	assert.True(t, synced.IsEmpty())
	assert.Empty(t, synced.Sections)

	// The rewrite drifted: it is planned, with the drift as its reason, and
	// the section's restart follows.
	in.Host = &Host{Rewrites: map[string]string{"conf/main.cf.in": "content differs"}}
	drifted, err := Compute(in)
	require.NoError(t, err)
	assert.Equal(t, []string{"conf/main.cf.in"}, rewriteSources(&drifted))
	assert.Empty(t, drifted.Postconf)
	assert.Equal(t, "drift: content differs", drifted.Rewrites[0].Reasons[0].String())
	assert.Equal(t, map[string]Action{"mta": ActionRestart}, restartNames(&drifted))

	// An unreadable target (LDAP) is listed but does not make its section,
	// and so its restart, look drifted.
	in.Host = &Host{Ldap: map[string]string{"zimbraMtaMyNetworks": "not read"}}
	ldapOnly, err := Compute(in)
	require.NoError(t, err)
	assert.Len(t, ldapOnly.Ldap, 1)
	assert.Empty(t, ldapOnly.Restarts)
	assert.Empty(t, ldapOnly.Rewrites)
	assert.Empty(t, ldapOnly.Sections)
}

func TestCompute_DeterministicOutput(t *testing.T) {
	t.Parallel()

	tracked := map[string]string{"mta": "running", "proxy": "running", "antivirus": "running", "antispam": "running"}
	prev := snap(allServices, baseVars())
	cur := snap(allServices, changed(changed(baseVars(), "zimbraMtaRestriction", "b"), "zimbraAmavisX", "2"))

	build := func() (Plan, []byte) {
		p, err := Compute(Input{Prev: prev, Cur: cur, Conf: mtaConf(), Tracked: tracked, Requested: []string{"proxy"}})
		require.NoError(t, err)

		data, err := p.JSON()
		require.NoError(t, err)

		return p, data
	}

	first, firstJSON := build()

	for range 50 {
		again, againJSON := build()

		assert.Equal(t, first, again)
		assert.True(t, bytes.Equal(firstJSON, againJSON), "JSON must be byte-identical")
	}
}

func TestPlan_WriteText(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	empty := Plan{}
	require.NoError(t, empty.WriteText(&buf))
	assert.Equal(t, "No changes.\n", buf.String())

	buf.Reset()

	p := Plan{
		Rewrites: []Rewrite{{Source: "a.in", Target: "a", Reasons: []Reason{{Kind: ReasonForced, Section: "mta"}}}},
		Postconf: []Postconf{{Key: "smtp_sasl_password", Value: "secret"}},
		Restarts: []Restart{{Service: "mta", Action: ActionRestart}},
	}
	require.NoError(t, p.WriteText(&buf))

	out := buf.String()
	assert.Contains(t, out, "rewrite      a.in -> a\n    because: section mta forced by REWRITE request\n")
	assert.Contains(t, out, "postconf -e  smtp_sasl_password=[REDACTED]")
	assert.NotContains(t, out, "secret")
	assert.Contains(t, out, "restart      mta")
}
