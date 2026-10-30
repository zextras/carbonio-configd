// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package shadow_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/shadow"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const (
	base       = "/opt/zextras"
	shadowDir  = "/var/shadow"
	secretLine = "s3cr3t-do-not-print"
)

// syncBuffer is a log sink safe for the logger's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// captureLogs routes the global logger to a buffer. Tests using it must not
// run in parallel.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()

	out := &syncBuffer{}

	logger.InitStructuredLogging(&logger.Config{
		Format: logger.FormatJSON, Level: logger.LogLevelDebug, Output: out, TimeFormat: time.RFC3339,
	})
	t.Cleanup(func() {
		logger.InitStructuredLogging(&logger.Config{Format: logger.FormatText, Level: logger.LogLevelError, Output: io.Discard})
	})

	return out
}

// logLines returns the decoded JSON log records whose msg is msg.
func logLines(t *testing.T, out *syncBuffer, msg string) []map[string]any {
	t.Helper()

	var recs []map[string]any

	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}

		if rec["msg"] == msg {
			recs = append(recs, rec)
		}
	}

	return recs
}

// fakeSource is a scripted Source.
type fakeSource struct {
	loadErr   error
	planErr   error
	plans     []*plan.Plan
	rendered  map[string]string
	renderErr error

	loads, plansTaken, done int
	onPlan                  func(n int)
}

func (f *fakeSource) Load(context.Context) error {
	f.loads++

	return f.loadErr
}

func (f *fakeSource) Plan(context.Context) (*plan.Plan, error) {
	if f.planErr != nil {
		return nil, f.planErr
	}

	pl := f.plans[min(f.plansTaken, len(f.plans)-1)]
	f.plansTaken++

	if f.onPlan != nil {
		f.onPlan(f.plansTaken)
	}

	return pl, nil
}

func (f *fakeSource) RenderRewrite(_ context.Context, r *plan.Rewrite) ([]byte, os.FileMode, error) {
	if f.renderErr != nil {
		return nil, 0, f.renderErr
	}

	return []byte(f.rendered[r.Source]), 0o640, nil
}

func (f *fakeSource) CycleDone() { f.done++ }

func drift(section string) []plan.Reason {
	return []plan.Reason{{Kind: plan.ReasonDrift, Section: section, Detail: "d"}}
}

func fullPlan() *plan.Plan {
	return &plan.Plan{
		FirstRun: true,
		Rewrites: []plan.Rewrite{
			{Source: "conf/nginx.conf.in", Target: "conf/nginx.conf", Mode: "0640",
				Sections: []string{"proxy"}, Reasons: []plan.Reason{{Kind: plan.ReasonFirstRun, Section: "proxy"}}},
		},
		Postconf: []plan.Postconf{
			{Key: "zimbra_ldap_password", Value: secretLine, Sections: []string{"mta"}, Reasons: drift("mta")},
			{Key: "myhostname", Value: "VAR:zimbraServiceHostname", Sections: []string{"mta"}, Reasons: drift("mta")},
		},
		Restarts: []plan.Restart{
			{Service: "proxy", Action: plan.ActionRestart, Sections: []string{"proxy"}, Reasons: drift("proxy")},
		},
		Proxygen: &plan.Proxygen{Sections: []string{"proxy"}, Reasons: drift("proxy")},
	}
}

func emptyPlan() *plan.Plan { return &plan.Plan{} }

type harness struct {
	src   *fakeSource
	out   *systest.Fakes
	clock *systest.Clock
	sh    *shadow.Shadow
}

func newHarness(t *testing.T, dir string, src *fakeSource, tweak func(*shadow.Options)) *harness {
	t.Helper()

	out := systest.New()
	opts := shadow.Options{
		Source:   src,
		Clock:    out.Clock,
		Interval: func() time.Duration { return time.Hour },
		Once:     true,
		Dir:      dir,
		Out:      out.FS,
		BaseDir:  base,
	}

	if tweak != nil {
		tweak(&opts)
	}

	sh, err := shadow.New(&opts)
	require.NoError(t, err)

	return &harness{src: src, out: out, clock: out.Clock, sh: sh}
}

func TestCycle_LogsOneShadowPlanLinePerActionWithReasons(t *testing.T) {
	logs := captureLogs(t)
	src := &fakeSource{plans: []*plan.Plan{fullPlan()}}
	h := newHarness(t, "", src, nil)

	require.NoError(t, h.sh.Cycle(t.Context()))

	lines := logLines(t, logs, "Shadow plan")
	require.Len(t, lines, 5)

	got := make(map[string]map[string]any)
	for _, l := range lines {
		got[l["action"].(string)+" "+l["subject"].(string)] = l
	}

	rw := got["rewrite conf/nginx.conf.in -> conf/nginx.conf"]
	require.NotNil(t, rw)
	assert.Equal(t, "INFO", rw["level"])
	assert.Equal(t, []any{"first run: section proxy rendered for the first time"}, rw["reasons"])
	assert.Equal(t, []any{"proxy"}, rw["sections"])
	assert.EqualValues(t, 1, rw["cycle"])

	assert.Contains(t, got, "postconf myhostname=VAR:zimbraServiceHostname")
	assert.Contains(t, got, "restart proxy")
	assert.Contains(t, got, "proxygen regenerate proxy configuration")

	assert.NotContains(t, logs.String(), secretLine, "secret values must be redacted from the log")

	done := logLines(t, logs, "Shadow cycle completed")
	require.Len(t, done, 1)
	assert.EqualValues(t, 5, done[0]["actions"])
	assert.Equal(t, 1, src.done)
}

func TestCycle_EmptyPlanLogsNoActionLines(t *testing.T) {
	logs := captureLogs(t)
	h := newHarness(t, "", &fakeSource{plans: []*plan.Plan{emptyPlan()}}, nil)

	require.NoError(t, h.sh.Cycle(t.Context()))

	assert.Empty(t, logLines(t, logs, "Shadow plan"))
	assert.Len(t, logLines(t, logs, "Shadow cycle completed"), 1)
}

func TestCycle_WritesRenderedFilesAndPlanJSON(t *testing.T) {
	captureLogs(t)

	src := &fakeSource{
		plans:    []*plan.Plan{fullPlan()},
		rendered: map[string]string{"conf/nginx.conf.in": "worker_processes 4;\n"},
	}
	h := newHarness(t, shadowDir, src, nil)

	require.NoError(t, h.sh.Cycle(t.Context()))

	data, ok := h.out.FS.Data(shadowDir + "/rendered/conf/nginx.conf")
	require.True(t, ok)
	assert.Equal(t, "worker_processes 4;\n", string(data))

	mode, _ := h.out.FS.Mode(shadowDir + "/rendered/conf/nginx.conf")
	assert.Equal(t, os.FileMode(0o600), mode.Perm(), "rendered files may carry credentials")

	latest, ok := h.out.FS.Data(shadowDir + "/plan-latest.json")
	require.True(t, ok)
	assert.NotContains(t, string(latest), secretLine)
	assert.True(t, json.Valid(latest))

	var perCycle []string

	for _, p := range h.out.FS.Paths() {
		if strings.HasPrefix(p, shadowDir+"/plans/") {
			perCycle = append(perCycle, p)
		}
	}

	require.Len(t, perCycle, 1)
	assert.Contains(t, perCycle[0], "plan-20260101T000000Z-000001.json")

	copyData, _ := h.out.FS.Data(perCycle[0])
	assert.Equal(t, string(latest), string(copyData))
}

func TestCycle_EmptyPlanKeepsOnlyTheLatestJSON(t *testing.T) {
	captureLogs(t)

	h := newHarness(t, shadowDir, &fakeSource{plans: []*plan.Plan{emptyPlan()}}, nil)

	require.NoError(t, h.sh.Cycle(t.Context()))
	require.NoError(t, h.sh.Cycle(t.Context()))

	assert.True(t, h.out.FS.Exists(shadowDir+"/plan-latest.json"))

	for _, p := range h.out.FS.Paths() {
		assert.False(t, strings.HasPrefix(p, shadowDir+"/plans/"), "unexpected per-cycle file %s", p)
	}
}

func TestCycle_TargetCannotEscapeTheReportDirectory(t *testing.T) {
	captureLogs(t)

	pl := &plan.Plan{Rewrites: []plan.Rewrite{{
		Source: "conf/evil.in", Target: "../../etc/cron.d/evil", Reasons: drift("x"),
	}}}
	src := &fakeSource{plans: []*plan.Plan{pl}, rendered: map[string]string{"conf/evil.in": "x"}}
	h := newHarness(t, shadowDir, src, nil)

	require.NoError(t, h.sh.Cycle(t.Context()))

	assert.True(t, h.out.FS.Exists(shadowDir+"/rendered/etc/cron.d/evil"))

	for _, p := range h.out.FS.Paths() {
		assert.False(t, strings.HasPrefix(p, "/etc"), "%s is outside the report directory", p)
	}
}

func TestCycle_RenderFailureIsReportedAndOtherFilesStillWritten(t *testing.T) {
	logs := captureLogs(t)

	src := &fakeSource{plans: []*plan.Plan{fullPlan()}, renderErr: errors.New("boom")}
	h := newHarness(t, shadowDir, src, nil)

	err := h.sh.Cycle(t.Context())

	require.ErrorContains(t, err, "render conf/nginx.conf.in")
	assert.True(t, h.out.FS.Exists(shadowDir+"/plan-latest.json"))
	assert.Len(t, logLines(t, logs, "Shadow report failed"), 1)
	assert.Len(t, logLines(t, logs, "Shadow plan"), 5, "the plan is logged even when a render fails")
}

func TestCycle_NoDirTouchesNoFiles(t *testing.T) {
	captureLogs(t)

	h := newHarness(t, "", &fakeSource{plans: []*plan.Plan{fullPlan()}}, nil)

	require.NoError(t, h.sh.Cycle(t.Context()))

	assert.Empty(t, h.out.Rec.Ops())
}

func TestCycle_FailuresAreLoggedAndDoNotEndTheFirstRun(t *testing.T) {
	logs := captureLogs(t)

	loadFail := &fakeSource{loadErr: errors.New("ldap down"), plans: []*plan.Plan{emptyPlan()}}
	require.ErrorContains(t, newHarness(t, "", loadFail, nil).sh.Cycle(t.Context()), "load: ldap down")
	assert.Zero(t, loadFail.done)

	planFail := &fakeSource{planErr: plan.ErrAllServicesDisabled}
	require.ErrorIs(t, newHarness(t, "", planFail, nil).sh.Cycle(t.Context()), plan.ErrAllServicesDisabled)
	assert.Zero(t, planFail.done)

	assert.Len(t, logLines(t, logs, "Shadow cycle failed"), 2)
}

func TestNew_RejectsADirectoryOverlappingTheInstallTree(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"/opt/zextras", "/opt/zextras/", "/opt", "/"} {
		_, err := shadow.New(&shadow.Options{Dir: dir, BaseDir: base})
		require.ErrorIs(t, err, shadow.ErrUnsafeDir, dir)
	}

	for _, dir := range []string{"/var/shadow", "/opt/zextras/tmp/shadow", "/opt/zextras2", ""} {
		_, err := shadow.New(&shadow.Options{Dir: dir, BaseDir: base})
		require.NoError(t, err, dir)
	}
}

func TestRun_OnceReturnsTheCycleError(t *testing.T) {
	captureLogs(t)

	h := newHarness(t, "", &fakeSource{loadErr: errors.New("ldap down")}, nil)

	require.ErrorContains(t, h.sh.Run(t.Context()), "ldap down")
}

func TestRun_LoopsOnReloadAndStopsOnShutdown(t *testing.T) {
	captureLogs(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	reload := make(chan struct{}, 1)
	src := &fakeSource{plans: []*plan.Plan{fullPlan(), emptyPlan()}}
	src.onPlan = func(n int) {
		switch n {
		case 1:
			reload <- struct{}{} // wakes the wait after cycle 1
		case 2:
			cancel() // shutdown during cycle 2
		}
	}

	h := newHarness(t, "", src, func(o *shadow.Options) {
		o.Once = false
		o.Reload = reload
	})

	require.NoError(t, h.sh.Run(ctx))
	assert.Equal(t, 2, src.plansTaken)
}

func TestRun_FailedCycleRetriesAfterTheBackoffNotTheInterval(t *testing.T) {
	captureLogs(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	src := &fakeSource{loadErr: errors.New("ldap down")}

	var waited time.Duration

	h := newHarness(t, "", src, func(o *shadow.Options) {
		o.Once = false
		o.Clock = durationSpy{Clock: o.Clock, got: &waited, cancel: cancel}
	})

	require.NoError(t, h.sh.Run(ctx))
	assert.Equal(t, 60*time.Second, waited)
}

// durationSpy records the first wait and then cancels the run.
type durationSpy struct {
	sys.Clock
	got    *time.Duration
	cancel context.CancelFunc
}

func (d durationSpy) NewTimer(dur time.Duration) sys.Timer {
	*d.got = dur

	d.cancel()

	return d.Clock.NewTimer(dur)
}
