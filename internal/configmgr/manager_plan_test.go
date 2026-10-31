// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/redact"
	"github.com/zextras/carbonio-configd/internal/restart"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/testutil"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

// These tests cover the ConfigManager side of the diff (Observe, Plan,
// CheckConditional); the pure decisions are table-tested in internal/plan.

func TestMain(m *testing.M) {
	// Initialize logger for tests
	logger.InitStructuredLogging(&logger.Config{
		Level:  slog.LevelError, // Reduce noise in tests
		Format: "text",
	})
	os.Exit(m.Run())
}

const svcSource = "zimbraServiceEnabled"

// hostRecorder records the mta operations Execute performs.
type hostRecorder struct {
	mu       sync.Mutex
	postconf []mtaops.PostconfOperation
	mapfile  []mtaops.MapfileOperation
}

func (h *hostRecorder) executor() *testutil.MockMtaExecutor {
	return &testutil.MockMtaExecutor{
		ExecutePostconfFn: func(_ context.Context, op mtaops.PostconfOperation) error {
			h.mu.Lock()
			defer h.mu.Unlock()

			h.postconf = append(h.postconf, op)

			return nil
		},
		ExecutePostconfBatchFn: func(_ context.Context, ops []mtaops.PostconfOperation) error {
			h.mu.Lock()
			defer h.mu.Unlock()

			h.postconf = append(h.postconf, ops...)

			return nil
		},
		ExecuteMapfileFn: func(_ context.Context, op mtaops.MapfileOperation) error {
			h.mu.Lock()
			defer h.mu.Unlock()

			h.mapfile = append(h.mapfile, op)

			return nil
		},
	}
}

// newPlanCM builds a ConfigManager over a temp BaseDir whose mta executor
// records its operations. The first-run flag is set, as in a fresh daemon.
func newPlanCM(t *testing.T, conf *config.MtaConfig) (*ConfigManager, *hostRecorder) {
	t.Helper()

	ctx := context.Background()
	rec := &hostRecorder{}
	st := state.NewState()
	st.SetFirstRun(true)
	st.SetMtaConfig(conf)

	cm := &ConfigManager{
		env: hostEnv(),
		mainConfig: &config.Config{
			BaseDir:  t.TempDir(),
			Hostname: "testhost",
		},
		State:       st,
		ServiceMgr:  &testutil.MockServiceManager{},
		Cache:       cache.New(ctx, false),
		mtaExecutor: rec.executor(),
		mtaResolver: &testutil.MockMtaResolver{},
	}
	cm.Transformer = transformer.NewTransformer(cm)
	cm.policy = restart.New(cm.ServiceMgr, cm.env.Clock)

	return cm, rec
}

func enabled(names ...string) map[string]string {
	m := make(map[string]string, len(names))
	for _, n := range names {
		m[n] = svcSource
	}

	return m
}

func varSection(name, key string) *config.MtaConfigSection {
	return &config.MtaConfigSection{
		Name:         name,
		RequiredVars: map[string]string{key: configTypeVAR},
		Postconf:     map[string]string{name + "_param": "VAR:" + key},
	}
}

func varChanges(pl *plan.Plan, section string) map[string][2]string {
	out := map[string][2]string{}

	for _, s := range pl.Sections {
		if s.Name != section {
			continue
		}

		for _, r := range s.Reasons {
			if r.Kind == plan.ReasonVarChanged {
				out[r.Key] = [2]string{r.Old, r.New}
			}
		}
	}

	return out
}

func sectionNames(pl *plan.Plan) []string {
	out := []string{}
	for _, s := range pl.Sections {
		out = append(out, s.Name)
	}

	return out
}

func hasReason(pl *plan.Plan, section string, kind plan.ReasonKind) bool {
	for _, s := range pl.Sections {
		if s.Name != section {
			continue
		}

		for _, r := range s.Reasons {
			if r.Kind == kind {
				return true
			}
		}
	}

	return false
}

func TestObserve(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, cm *ConfigManager)
		conf  *config.MtaConfigSection
		want  map[snapshot.Lookup]string
	}{
		{
			name: "VAR local service and negated keys",
			setup: func(_ *testing.T, cm *ConfigManager) {
				setGlobal(cm, map[string]string{"gvar": "g", "empty": ""})
				setLocal(cm, map[string]string{"lvar": "l"})
				setServer(cm, nil, enabled("mta"))
			},
			conf: &config.MtaConfigSection{
				RequiredVars: map[string]string{
					"gvar": configTypeVAR, "!gvar2": configTypeVAR, "empty": configTypeVAR,
					"missing": configTypeVAR, "lvar": configTypeLOCAL,
				},
				Conditionals: []config.Conditional{
					{Type: configTypeSERVICE, Key: "mta"},
					{Type: configTypeSERVICE, Key: "!proxy"},
					{Type: configTypeVAR, Key: "!gvar"},
				},
			},
			want: map[snapshot.Lookup]string{
				{Type: configTypeVAR, Key: "gvar"}:      "g",
				{Type: configTypeVAR, Key: "gvar2"}:     "",
				{Type: configTypeVAR, Key: "empty"}:     "",
				{Type: configTypeVAR, Key: "missing"}:   "",
				{Type: configTypeLOCAL, Key: "lvar"}:    "l",
				{Type: configTypeSERVICE, Key: "mta"}:   snapshot.ServiceTrue,
				{Type: configTypeSERVICE, Key: "proxy"}: snapshot.ServiceFalse,
			},
		},
		{
			name: "FILE is the transformed non-empty lines, missing file is empty",
			setup: func(t *testing.T, cm *ConfigManager) {
				dir := filepath.Join(cm.mainConfig.BaseDir, "conf")
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "present"), []byte("a\n\n b \n"), 0o600))
			},
			conf: &config.MtaConfigSection{
				RequiredVars: map[string]string{"present": configTypeFILE, "absent": configTypeFILE},
			},
			want: map[snapshot.Lookup]string{
				{Type: configTypeFILE, Key: "present"}: "a, b",
				{Type: configTypeFILE, Key: "absent"}:  "",
			},
		},
		{
			name: "MAPFILE is the target path iff a value is configured, whatever is on disk",
			setup: func(_ *testing.T, cm *ConfigManager) {
				setGlobal(cm, map[string]string{"zimbraSSLDHParam": "-----BEGIN DH PARAMETERS-----"})
			},
			conf: &config.MtaConfigSection{
				RequiredVars: map[string]string{"zimbraSSLDHParam": configTypeMAPFILE},
			},
			want: map[snapshot.Lookup]string{
				{Type: configTypeMAPFILE, Key: "zimbraSSLDHParam"}: "BASE/conf/dhparam.pem",
			},
		},
		{
			name: "MAPFILE without a value is empty even when the file exists",
			setup: func(t *testing.T, cm *ConfigManager) {
				dir := filepath.Join(cm.mainConfig.BaseDir, "conf")
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "dhparam.pem"), []byte("x"), 0o600))
			},
			conf: &config.MtaConfigSection{
				RequiredVars: map[string]string{"zimbraSSLDHParam": configTypeMAPFILE},
			},
			want: map[snapshot.Lookup]string{
				{Type: configTypeMAPFILE, Key: "zimbraSSLDHParam"}: "",
			},
		},
		{
			name:  "MAPLOCAL is the path of the existing file, empty when absent",
			setup: func(_ *testing.T, _ *ConfigManager) {},
			conf: &config.MtaConfigSection{
				RequiredVars: map[string]string{"zimbraSSLDHParam": configTypeMAPLOCAL},
			},
			want: map[snapshot.Lookup]string{
				{Type: configTypeMAPLOCAL, Key: "zimbraSSLDHParam"}: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{"s": tt.conf}}
			cm, rec := newPlanCM(t, conf)
			tt.setup(t, cm)

			base := cm.State.Snapshot()
			got := cm.Observe(context.Background(), base, conf)

			for lk, want := range tt.want {
				if rest, ok := strings.CutPrefix(want, "BASE"); ok {
					want = cm.mainConfig.BaseDir + rest
				}

				v, ok := got.Observed(lk.Type, lk.Key)
				assert.True(t, ok, "%v must be recorded, even when undefined", lk)
				assert.Equal(t, want, v, "%v", lk)
			}

			_, seen := base.Observed(configTypeVAR, "gvar")
			assert.False(t, seen, "Observe must not mutate the base snapshot")
			assert.Empty(t, rec.mapfile, "Observe must not write mapped files")
		})
	}

	t.Run("MAPLOCAL with the file present", func(t *testing.T) {
		conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{"s": {
			RequiredVars: map[string]string{"zimbraSSLDHParam": configTypeMAPLOCAL},
		}}}
		cm, _ := newPlanCM(t, conf)

		dir := filepath.Join(cm.mainConfig.BaseDir, "conf")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dhparam.pem"), []byte("x"), 0o600))

		got := cm.Observe(context.Background(), cm.State.Snapshot(), conf)
		v, _ := got.Observed(configTypeMAPLOCAL, "zimbraSSLDHParam")
		assert.Equal(t, filepath.Join(cm.mainConfig.BaseDir, "conf/dhparam.pem"), v)
	})
}

func TestPlan_FirstRunThenSteadyState(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta":   varSection("mta", "zimbraA"),
		"proxy": varSection("proxy", "zimbraB"),
	}}
	cm, rec := newPlanCM(t, conf)
	setGlobal(cm, map[string]string{"zimbraA": "a", "zimbraB": "b"})
	setServer(cm, nil, enabled("mta", "proxy"))

	first := mustCycle(t, cm)
	assert.True(t, first.FirstRun)
	assert.Equal(t, []string{"mta", "proxy"}, sectionNames(first))
	assert.True(t, hasReason(first, "mta", plan.ReasonFirstRun))
	assert.Equal(t, "running", first.Tracked["mta"])
	assert.Len(t, first.Postconf, 2)
	assert.Len(t, rec.postconf, 2, "the first run renders every enabled section")

	cm.State.SetFirstRun(false)

	second := mustCycle(t, cm)
	assert.False(t, second.FirstRun)
	assert.True(t, second.IsEmpty(), "nothing changed: %+v", second)
	assert.Empty(t, second.Sections)
	assert.Len(t, rec.postconf, 2, "an unchanged cycle executes nothing")
}

func TestPlan_ChangedVariable(t *testing.T) {
	tests := []struct {
		name    string
		before  map[string]string
		after   map[string]string
		wantOld string
		wantNew string
	}{
		{"value changes", map[string]string{"zimbraA": "1"}, map[string]string{"zimbraA": "2"}, "1", "2"},
		{"value appears", map[string]string{}, map[string]string{"zimbraA": "2"}, "", "2"},
		{"value becomes undefined", map[string]string{"zimbraA": "1"}, map[string]string{}, "1", ""},
		{"value becomes empty", map[string]string{"zimbraA": "1"}, map[string]string{"zimbraA": ""}, "1", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
				"mta":   varSection("mta", "zimbraA"),
				"proxy": varSection("proxy", "zimbraB"),
			}}
			cm, _ := newPlanCM(t, conf)
			setGlobal(cm, tt.before)
			setServer(cm, nil, enabled("mta", "proxy"))
			mustCycle(t, cm)
			cm.State.SetFirstRun(false)

			setGlobal(cm, tt.after)

			var buf bytes.Buffer

			pl, err := cm.Plan(captureLogCtx(&buf))
			require.NoError(t, err)

			assert.Equal(t, []string{"mta"}, sectionNames(pl), "only the section using the variable")
			assert.Equal(t, map[string][2]string{"zimbraA": {tt.wantOld, tt.wantNew}}, varChanges(pl, "mta"))
			require.Len(t, pl.Postconf, 1)
			assert.Equal(t, "mta_param", pl.Postconf[0].Key)

			logs := configmgrLogLines(buf.String())
			if tt.wantNew == "" {
				assert.Contains(t, logs, "Variable changed to undefined")
				assert.Contains(t, logs, "previous_value="+tt.wantOld)
			} else {
				assert.Contains(t, logs, "Variable changed")
				assert.NotContains(t, logs, "to undefined")
				assert.Contains(t, logs, "current_value="+tt.wantNew)
			}
		})
	}
}

func TestPlan_EmptyValueSteadyState(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{"mta": varSection("mta", "k")}}
	cm, _ := newPlanCM(t, conf)
	setGlobal(cm, map[string]string{"k": ""})
	setServer(cm, nil, enabled("mta"))
	mustCycle(t, cm)
	cm.State.SetFirstRun(false)

	for range 3 {
		assert.True(t, mustCycle(t, cm).IsEmpty(), "a defined-but-empty value never reports a change")
	}
}

func TestPlan_SecretChangeIsNotLogged(t *testing.T) {
	const (
		oldSecret = "old-hunter2-secret"
		newSecret = "new-hunter2-secret"
	)

	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{"mta": {
		Name: "mta",
		RequiredVars: map[string]string{
			"ldap_amavis_password": configTypeLOCAL, "zimbraImapBindPort": configTypeVAR,
		},
	}}}

	tests := []struct {
		name      string
		newSecret string
		wantLog   string
	}{
		{"changed", newSecret, "Variable changed"},
		{"became undefined", "", "Variable changed to undefined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm, _ := newPlanCM(t, conf)
			setServer(cm, nil, enabled("mta"))
			setLocal(cm, map[string]string{"ldap_amavis_password": oldSecret})
			setGlobal(cm, map[string]string{"zimbraImapBindPort": "143"})
			mustCycle(t, cm)
			cm.State.SetFirstRun(false)

			setLocal(cm, map[string]string{"ldap_amavis_password": tt.newSecret})
			setGlobal(cm, map[string]string{"zimbraImapBindPort": "7143"})

			var buf bytes.Buffer

			pl, err := cm.Plan(captureLogCtx(&buf))
			require.NoError(t, err)

			out := configmgrLogLines(buf.String())
			assert.NotContains(t, out, oldSecret)
			assert.NotContains(t, out, newSecret)
			assert.Contains(t, out, tt.wantLog)
			assert.Contains(t, out, "ldap_amavis_password", "key name must still be logged")
			assert.Contains(t, out, redact.Placeholder)
			assert.Contains(t, out, "previous_value=143", "non-secret values stay visible")

			change := varChanges(pl, "mta")["ldap_amavis_password"]
			assert.NotContains(t, change[0]+change[1], "hunter2", "plan reasons are redacted too")
		})
	}
}

func TestPlan_ForcedAndRequestedSections(t *testing.T) {
	newCM := func(t *testing.T) *ConfigManager {
		t.Helper()

		conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
			"mta":   varSection("mta", "zimbraA"),
			"proxy": varSection("proxy", "zimbraB"),
		}}
		cm, _ := newPlanCM(t, conf)
		setGlobal(cm, map[string]string{"zimbraA": "1", "zimbraB": "1"})
		setServer(cm, nil, enabled("mta", "proxy"))
		mustCycle(t, cm)
		cm.State.SetFirstRun(false)

		return cm
	}

	t.Run("forced mode compares only the forced sections", func(t *testing.T) {
		cm := newCM(t)
		cm.State.SetForcedConfig(context.Background(), "mta")
		setGlobal(cm, map[string]string{"zimbraA": "2", "zimbraB": "2"})

		pl := mustCycle(t, cm)
		assert.Equal(t, []string{"mta"}, sectionNames(pl))
		assert.True(t, hasReason(pl, "mta", plan.ReasonForced))
		assert.True(t, hasReason(pl, "mta", plan.ReasonVarChanged))
	})

	t.Run("forced section without change is still processed", func(t *testing.T) {
		cm := newCM(t)
		cm.State.SetForcedConfig(context.Background(), "proxy")

		pl := mustCycle(t, cm)
		assert.Equal(t, []string{"proxy"}, sectionNames(pl))
		assert.True(t, hasReason(pl, "proxy", plan.ReasonForced))
		assert.Len(t, pl.Postconf, 1)
	})

	t.Run("requested section is processed once and the request consumed", func(t *testing.T) {
		cm := newCM(t)
		cm.State.AddRequestedConfigs(context.Background(), []string{"proxy"})

		pl := mustCycle(t, cm)
		assert.Equal(t, []string{"proxy"}, sectionNames(pl))
		assert.True(t, hasReason(pl, "proxy", plan.ReasonRequested))

		assert.True(t, mustCycle(t, cm).IsEmpty(), "the request is consumed by a successful Plan")
	})
}

func TestPlan_AllServicesDisabledKeepsState(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta":   varSection("mta", "zimbraA"),
		"proxy": varSection("proxy", "zimbraB"),
	}}
	cm, _ := newPlanCM(t, conf)
	setGlobal(cm, map[string]string{"zimbraA": "1", "zimbraB": "1"})
	setServer(cm, nil, enabled("mta", "proxy"))
	mustCycle(t, cm)
	cm.State.SetFirstRun(false)

	prev, tracked := cm.prev, cm.tracked
	require.NotNil(t, prev)
	require.Len(t, tracked, 2)

	// A transient answer: no service enabled, a changed variable, a request.
	setServer(cm, nil, nil)
	setGlobal(cm, map[string]string{"zimbraA": "2", "zimbraB": "1"})
	cm.State.AddRequestedConfigs(context.Background(), []string{"proxy"})

	pl, err := cm.Plan(context.Background())
	require.ErrorIs(t, err, plan.ErrAllServicesDisabled)
	assert.EqualError(t, err, "all services detected disabled")
	assert.Nil(t, pl)
	assert.Same(t, prev, cm.prev, "the baseline must not advance")
	assert.Equal(t, tracked, cm.tracked, "tracked services must not advance")

	// The next healthy cycle still sees the change and the requested section.
	setServer(cm, nil, enabled("mta", "proxy"))

	pl, err = cm.Plan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"mta", "proxy"}, sectionNames(pl))
	assert.Equal(t, map[string][2]string{"zimbraA": {"1", "2"}}, varChanges(pl, "mta"))
	assert.True(t, hasReason(pl, "proxy", plan.ReasonRequested), "the request was re-queued")
}

func TestPlan_ServiceTransitions(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{
		"mta": varSection("mta", "zimbraA"),
	}}
	cm, _ := newPlanCM(t, conf)
	setGlobal(cm, map[string]string{"zimbraA": "1"})
	setServer(cm, nil, enabled("mta", "proxy"))
	mustCycle(t, cm)
	cm.State.SetFirstRun(false)

	// proxy goes away, imapd shows up.
	setServer(cm, nil, enabled("mta", "imapd"))

	var buf bytes.Buffer

	pl, err := cm.Plan(captureLogCtx(&buf))
	require.NoError(t, err)

	got := map[string]plan.Action{}
	for _, r := range pl.Restarts {
		got[r.Service] = r.Action
	}

	assert.Equal(t, plan.ActionStop, got["proxy"])
	assert.Equal(t, plan.ActionStart, got["imapd"])
	assert.Equal(t, map[string]string{"mta": "running", "proxy": "running"}, pl.Tracked,
		"tracking only grows on the first run (legacy)")
	assert.Contains(t, buf.String(), "Service was disabled, need to stop")
	assert.Contains(t, buf.String(), "Service was enabled, need to start")
}

func TestPlan_MapfileHealOnlyInExecute(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{"mta": {
		Name:         "mta",
		RequiredVars: map[string]string{"zimbraSSLDHParam": configTypeMAPFILE},
	}}}
	cm, rec := newPlanCM(t, conf)
	setGlobal(cm, map[string]string{"zimbraSSLDHParam": "-----BEGIN DH PARAMETERS-----"})
	setServer(cm, nil, enabled("mta"))

	ctx := context.Background()

	pl, err := cm.Plan(ctx)
	require.NoError(t, err)
	require.Len(t, pl.Mapfiles, 1, "first run syncs the mapped file as a regular entry")
	assert.Equal(t, "zimbraSSLDHParam", pl.Mapfiles[0].Key)
	assert.Empty(t, pl.Heal, "an entry already in Mapfiles is not healed twice")
	assert.Empty(t, rec.mapfile, "Plan has no side effects")
	assert.NoFileExists(t, filepath.Join(cm.mainConfig.BaseDir, "conf/dhparam.pem"))

	require.NoError(t, cm.Execute(ctx, pl))

	require.Len(t, rec.mapfile, 1)

	for _, op := range rec.mapfile {
		assert.Equal(t, "zimbraSSLDHParam", op.Key)
		assert.NotEmpty(t, op.Base64Data, "the configured value must be written, not deleted")
	}

	// Steady state: no change, but the file is still healed every cycle.
	cm.State.SetFirstRun(false)

	rec.mapfile = nil

	pl, err = cm.Plan(ctx)
	require.NoError(t, err)
	assert.Empty(t, pl.Mapfiles, "no variable changed")
	assert.True(t, pl.IsEmpty(), "heal does not make a plan non-empty")
	require.Len(t, pl.Heal, 1)
	assert.Empty(t, rec.mapfile)

	require.NoError(t, cm.Execute(ctx, pl))
	require.Len(t, rec.mapfile, 1)
	assert.NotEmpty(t, rec.mapfile[0].Base64Data)
}

func TestPlan_PerformsNoHostWrites(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{"mta": {
		Name:         "mta",
		RequiredVars: map[string]string{"zimbraA": configTypeVAR, "zimbraSSLDHParam": configTypeMAPFILE},
		Postconf:     map[string]string{"p": "VAR:zimbraA"},
		Ldap:         map[string]string{"attr": "VAR:zimbraA"},
	}}}
	cm, rec := newPlanCM(t, conf)
	setGlobal(cm, map[string]string{"zimbraA": "1", "zimbraSSLDHParam": "x"})
	setServer(cm, nil, enabled("mta"))

	pl, err := cm.Plan(context.Background())
	require.NoError(t, err)
	assert.False(t, pl.IsEmpty())
	assert.Empty(t, rec.postconf)
	assert.Empty(t, rec.mapfile)

	entries, err := os.ReadDir(cm.mainConfig.BaseDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "Plan must not create files")

	require.NoError(t, cm.Execute(context.Background(), pl))
	assert.Len(t, rec.postconf, 1)
}

// TestPlan_PostconfOverrideOrder mirrors the mta section shapes of configd.cf:
// a bare POSTCONF followed by a true conditional VAR override (smtpd_milters)
// and a VAR followed by a true conditional bare POSTCONF (header_checks); in
// both the last directive in file order wins and an empty value is kept.
func TestPlan_PostconfOverrideOrder(t *testing.T) {
	conf := &config.MtaConfig{Sections: map[string]*config.MtaConfigSection{"mta": {
		Name: "mta",
		Postconf: map[string]string{
			"smtpd_milters": "",
			"header_checks": "VAR:zimbraMtaHeaderChecks",
		},
		Conditionals: []config.Conditional{
			{
				Type: configTypeVAR, Key: "zimbraMtaSmtpdMilters",
				Postconf: map[string]string{"smtpd_milters": "VAR:zimbraMtaSmtpdMilters"},
			},
			{
				Type: configTypeVAR, Key: "zimbraMtaBlockedExtensionWarnRecipient",
				Postconf: map[string]string{"header_checks": ""},
			},
			{
				Type: configTypeSERVICE, Key: "nope", Postconf: map[string]string{"skipped": "x"},
			},
		},
	}}}
	cm, _ := newPlanCM(t, conf)
	setServer(cm, map[string]string{
		"zimbraMtaSmtpdMilters":                  "inet:127.0.0.1:7026",
		"zimbraMtaBlockedExtensionWarnRecipient": "TRUE",
	}, enabled("mta"))

	pl, err := cm.Plan(context.Background())
	require.NoError(t, err)

	got := map[string]string{}
	for _, p := range pl.Postconf {
		got[p.Key] = p.Value
	}

	assert.Equal(t, map[string]string{
		"smtpd_milters": "VAR:zimbraMtaSmtpdMilters",
		"header_checks": "",
	}, got)
}

func TestCheckConditional(t *testing.T) {
	tests := []struct {
		name    string
		global  map[string]string
		local   map[string]string
		service []string
		cfgType string
		key     string
		want    bool
	}{
		{"enabled service", nil, nil, []string{"imapd"}, "SERVICE", "imapd", true},
		{"disabled service", nil, nil, nil, "SERVICE", "imapd", false},
		{"negated enabled service", nil, nil, []string{"imapd"}, "SERVICE", "!imapd", false},
		{"negated disabled service", nil, nil, nil, "SERVICE", "!imapd", true},
		{"missing key", nil, nil, nil, "VAR", "nonexistent", false},
		{"negated missing key", nil, nil, nil, "VAR", "!nonexistent", true},
		{"VAR set", map[string]string{"zimbraIPMode": "ipv4"}, nil, nil, "VAR", "zimbraIPMode", true},
		{"VAR zero is false", map[string]string{"counter": "0"}, nil, nil, "VAR", "counter", false},
		{"VAR FALSE is false", map[string]string{"flag": "FALSE"}, nil, nil, "VAR", "flag", false},
		{"negated VAR FALSE", map[string]string{"flag": "FALSE"}, nil, nil, "VAR", "!flag", true},
		{"LOCAL TRUE", nil, map[string]string{"flag": "TRUE"}, nil, "LOCAL", "flag", true},
		{"unknown type behaves as a failed lookup", nil, nil, nil, "BOGUS", "k", false},
		{"unknown type negated", nil, nil, nil, "BOGUS", "!k", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm, _ := newPlanCM(t, &config.MtaConfig{})
			setGlobal(cm, tt.global)
			setLocal(cm, tt.local)
			setServer(cm, nil, enabled(tt.service...))

			got, err := cm.CheckConditional(context.Background(), tt.cfgType, tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCheckConditional_SecretValueNotLogged(t *testing.T) {
	const secret = "cond-hunter2-secret"

	cm, _ := newPlanCM(t, &config.MtaConfig{})
	setLocal(cm, map[string]string{"mailboxd_keystore_password": secret, "ldap_url": "ldap://x"})

	var buf bytes.Buffer

	ctx := captureLogCtx(&buf)

	got, err := cm.CheckConditional(ctx, configTypeLOCAL, "mailboxd_keystore_password")
	require.NoError(t, err)
	assert.True(t, got, "the decision uses the real value")

	got, err = cm.CheckConditional(ctx, configTypeLOCAL, "!ldap_url")
	require.NoError(t, err)
	assert.False(t, got)

	out := buf.String()
	assert.NotContains(t, out, secret)
	assert.Contains(t, out, "mailboxd_keystore_password")
	assert.Contains(t, out, "value=ldap://x", "non-secret values stay visible")
}
