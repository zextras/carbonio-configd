// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/shadow"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	shadowBase = "/opt/zextras"
	shadowOut  = "/var/shadow"
)

// shadowFixture is a node whose "mta" section holds one directive of every
// kind the executor knows (template rewrite, postconf, postconfd, LDAP write,
// restart). host is the fake host the shadow cycle observes; report is the
// separate writable filesystem of the shadow report directory.
type shadowFixture struct {
	host   *systest.Fakes
	report *systest.Fakes
	cm     *configmgr.ConfigManager
	appSt  *state.State
	sh     *shadow.Shadow
}

func newShadowFixture(t *testing.T) *shadowFixture {
	t.Helper()

	logger.InitStructuredLogging(&logger.Config{Format: logger.FormatText, Level: logger.LogLevelError, Output: io.Discard})

	host := systest.New()
	host.FS.PutDir(shadowBase+"/conf", 0o750)
	host.FS.Put(shadowBase+"/conf/main.cf.in", []byte("alpha = 1\nbeta = 2\n"), 0o644)
	host.FS.Put(shadowBase+"/conf/main.cf", []byte("stale\n"), 0o640)

	appState := state.NewState()
	appState.MtaConfig.Sections["mta"] = &config.MtaConfigSection{
		Name:      "mta",
		Depends:   map[string]bool{},
		Restarts:  map[string]bool{"mta": true},
		Rewrites:  map[string]config.RewriteEntry{"conf/main.cf.in": {Value: "conf/main.cf", Mode: "0640"}},
		Postconf:  map[string]string{"myhostname": "mail.example.com"},
		Postconfd: map[string]string{"smtpd_banner": ""},
		Ldap:      map[string]string{"ldap_common_loglevel": "256"},
	}
	appState.Publish(snapshot.New(snapshot.Parts{
		Services: map[string]string{"mta": "zimbraServiceEnabled"},
	}))

	cfg := &config.Config{BaseDir: shadowBase, Hostname: "testhost", RestartConfig: true}
	cm := configmgr.NewConfigManager(context.Background(), cfg, appState, nil,
		cache.New(context.Background(), true), sys.ReadOnly(host.System()))

	report := systest.New()
	src := &shadowSource{ConfigManager: cm, state: appState, load: func(context.Context) error { return nil }}

	sh, err := shadow.New(&shadow.Options{
		Source:   src,
		Clock:    host.Clock,
		Interval: func() time.Duration { return time.Hour },
		Once:     true,
		Dir:      shadowOut,
		Out:      report.FS,
		BaseDir:  shadowBase,
	})
	require.NoError(t, err)

	return &shadowFixture{host: host, report: report, cm: cm, appSt: appState, sh: sh}
}

func TestShadowCycle_PerformsZeroMutatingOpsOnTheHost(t *testing.T) {
	f := newShadowFixture(t)

	require.NoError(t, f.sh.Cycle(t.Context()))

	// The recorder sits below sys.ReadOnly: anything in it reached the host.
	assert.Empty(t, f.host.Rec.Ops(), "a shadow cycle must not execute, write, rename, remove, chmod or signal")

	// The live file is untouched, the would-be file is in the report.
	live, ok := f.host.FS.Data(shadowBase + "/conf/main.cf")
	require.True(t, ok)
	assert.Equal(t, "stale\n", string(live))

	wouldBe, ok := f.report.FS.Data(shadowOut + "/rendered/conf/main.cf")
	require.True(t, ok)
	assert.Equal(t, "alpha = 1\nbeta = 2\n", string(wouldBe))

	latest, ok := f.report.FS.Data(shadowOut + "/plan-latest.json")
	require.True(t, ok)
	assert.Contains(t, string(latest), `"first_run": true`)
	assert.Contains(t, string(latest), "ldap_common_loglevel")
	assert.Contains(t, string(latest), "smtpd_banner")
	assert.Contains(t, string(latest), "myhostname")

	assert.False(t, f.appSt.IsFirstRun(), "the first successful cycle ends the first run")
}

func TestShadowCycle_SecondCycleOfAQuietNodePlansNothing(t *testing.T) {
	f := newShadowFixture(t)

	require.NoError(t, f.sh.Cycle(t.Context()))
	require.NoError(t, f.sh.Cycle(t.Context()))

	latest, ok := f.report.FS.Data(shadowOut + "/plan-latest.json")
	require.True(t, ok)
	assert.Contains(t, string(latest), `"first_run": false`)
	assert.Contains(t, string(latest), `"rewrites": []`)
	assert.Empty(t, f.host.Rec.Ops())
}

// TestShadowCycle_ExecutorIsRefusedOnTheReadOnlySystem proves the guard bites:
// even if a future change wired the executor into the shadow cycle, its writes
// would fail and nothing would reach the host.
func TestShadowCycle_ExecutorIsRefusedOnTheReadOnlySystem(t *testing.T) {
	f := newShadowFixture(t)

	pl, err := f.cm.Plan(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, pl.Rewrites)

	err = f.cm.Execute(t.Context(), &plan.Plan{Rewrites: pl.Rewrites})

	require.ErrorIs(t, err, sys.ErrReadOnly)
	assert.Empty(t, f.host.Rec.Ops())

	live, _ := f.host.FS.Data(shadowBase + "/conf/main.cf")
	assert.Equal(t, "stale\n", string(live))
}

func TestRejectSideEffectFlags(t *testing.T) {
	t.Parallel()

	require.NoError(t, rejectSideEffectFlags(&Args{}))

	for flag, args := range map[string]*Args{
		"--cpuprofile":     {CPUProfile: "cpu.out"},
		"--memprofile":     {MemProfile: "mem.out"},
		"--trace":          {Trace: "t.out"},
		"--enable-tracing": {EnableTracing: true},
	} {
		err := rejectSideEffectFlags(args)
		require.ErrorIs(t, err, errShadowFlag, flag)
		assert.Contains(t, err.Error(), flag)
	}
}

func TestCLI_Shadow(t *testing.T) {
	t.Parallel()

	cli := &CLI{}

	parser, err := kong.New(cli, kong.Vars{"version": "test"})
	require.NoError(t, err)

	ctx, err := parser.Parse([]string{"shadow", "--shadow-dir", "/tmp/s", "--single-cycle"})
	require.NoError(t, err)

	assert.Equal(t, "shadow", ctx.Command())
	assert.Equal(t, "/tmp/s", cli.Shadow.Dir)
	assert.True(t, cli.Shadow.Once)
}

func TestCLI_ShadowDefaults(t *testing.T) {
	t.Parallel()

	cli := &CLI{}

	parser, err := kong.New(cli, kong.Vars{"version": "test"})
	require.NoError(t, err)

	_, err = parser.Parse([]string{"shadow"})
	require.NoError(t, err)

	assert.Empty(t, cli.Shadow.Dir)
	assert.False(t, cli.Shadow.Once)
}

func TestRunShadow(t *testing.T) {
	cfg := &config.Config{BaseDir: shadowBase, Hostname: "testhost", Interval: 60}

	tests := []struct {
		name     string
		dir      string
		loadErr  error
		want     int
		wantFile bool
	}{
		{"single cycle reports the plan", shadowOut, nil, 0, true},
		{"report directory overlapping the install tree is refused", shadowBase, nil, 1, false},
		{"failed single cycle exits with status 1", shadowOut, errors.New("ldap down"), 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newShadowFixture(t)
			src := &shadowSource{ConfigManager: f.cm, state: f.appSt, load: func(context.Context) error { return tt.loadErr }}

			got := runShadow(t.Context(), f.host.System(), cfg, f.appSt, src, &ShadowCmd{Dir: tt.dir, Once: true})

			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantFile, f.host.FS.Exists(shadowOut+"/plan-latest.json"))
		})
	}
}
