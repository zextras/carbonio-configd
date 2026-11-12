// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
)

// reportClock hands out a time that only moves when the test says so.
type reportClock struct{ t time.Time }

func (c *reportClock) now() time.Time { return c.t }

func (c *reportClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestReporter(t *testing.T, o ReportOptions) (*Reporter, *bytes.Buffer, *bytes.Buffer, *reportClock) {
	t.Helper()

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	clock := &reportClock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	o.Out, o.Err, o.Now = out, errb, clock.now

	if o.Plan == nil {
		o.Plan = []PlanStep{{0, "Prepare system users and directories"}, {2, "Check port conflicts"}, {5, "Generate dhparam key"}}
	}

	return NewReporter(&o), out, errb, clock
}

func step(title string) *Step { return &Step{Title: title} }

func finish(r *Reporter, clock *reportClock, i int, title string, o Outcome, d time.Duration, err error, warns ...string) {
	if o != AlreadyDone {
		r.Observe(Event{Kind: EventStart, Index: i, Step: step(title)})
		clock.advance(d)
	}

	r.Observe(Event{Kind: EventFinish, Index: i, Step: step(title), Outcome: o, Elapsed: d, Err: err, Warnings: warns})
}

func TestReporter_PipedRunIsOneAlignedLinePerStepWithNoEscapes(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{})

	finish(r, clock, 0, "Prepare system users and directories", Applied, 400*time.Millisecond, nil)
	r.Observe(Event{Kind: EventFinish, Index: 1, Step: step("Not applicable"), Outcome: Skipped})
	finish(r, clock, 2, "Check port conflicts", AlreadyDone, 0, nil)
	finish(r, clock, 5, "Generate dhparam key", Applied, 65*time.Second, nil)

	want := "[1/3] Prepare system users and directories  ok      0.4s\n" +
		"[2/3] Check port conflicts                  skip    already done\n" +
		"[3/3] Generate dhparam key                  ok      1:05\n"
	assert.Equal(t, want, out.String(), "steps that do not apply are not listed nor counted")
	assert.NotContains(t, out.String(), "\x1b")
	assert.NotContains(t, out.String(), "\r")
}

func TestReporter_StepMessagesAreIndentedUnderTheirStepLine(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{})

	r.Observe(Event{Kind: EventStart, Index: 0, Step: step("Prepare system users and directories")})
	r.Note(Note{Text: "carbonio-appserver certificate created"})
	clock.advance(3100 * time.Millisecond)
	r.Observe(Event{Kind: EventFinish, Index: 0, Step: step("Prepare system users and directories"),
		Outcome: Applied, Elapsed: 3100 * time.Millisecond})

	assert.Equal(t, "[1/3] Prepare system users and directories  ok      3.1s\n"+
		"      · carbonio-appserver certificate created\n", out.String())
}

func TestReporter_PrefixWidthFollowsTheNumberOfSteps(t *testing.T) {
	plan := make([]PlanStep, 49)
	for i := range plan {
		plan[i] = PlanStep{Index: i, Title: "Step"}
	}

	r, out, _, clock := newTestReporter(t, ReportOptions{Plan: plan})

	r.Observe(Event{Kind: EventStart, Index: 0, Step: step("Step")})
	r.Note(Note{Text: "hello"})
	clock.advance(time.Second)
	r.Observe(Event{Kind: EventFinish, Index: 0, Step: step("Step"), Outcome: Applied, Elapsed: time.Second})

	lines := strings.Split(out.String(), "\n")
	assert.True(t, strings.HasPrefix(lines[0], "[ 1/49] Step  ok"), lines[0])
	assert.Equal(t, "        · hello", lines[1], "the details line up under the title")
}

func TestReporter_WarningIsInlineWhenItFitsAndListedInTheSummary(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{})

	r.Observe(Event{Kind: EventStart, Index: 0, Step: step("Create GAL sync account")})
	r.Note(Note{Warn: true, Text: "skipped: appserver not started"})
	r.Note(Note{Text: "other detail"})
	clock.advance(time.Second)
	r.Observe(Event{Kind: EventFinish, Index: 0, Step: step("Create GAL sync account"), Outcome: Applied,
		Warnings: []string{"skipped: appserver not started"}})

	assert.Contains(t, out.String(), "warn    skipped: appserver not started\n")
	assert.NotContains(t, strings.TrimPrefix(out.String(), "["), "· skipped", "the inline warning is not repeated")
	assert.Contains(t, out.String(), "· other detail")

	finish(r, clock, 2, "Optional step", Warned, time.Second, errors.New("zmgsautil: exit status 1"))
	assert.Contains(t, out.String(), "warn    zmgsautil: exit status 1")

	r.Summary(&Summary{})

	sum := out.String()
	assert.Contains(t, sum, "Done in 2.0s · 1 applied · 0 already done · 2 warnings\n")
	assert.Contains(t, sum, "Warnings:\n  · Create GAL sync account: skipped: appserver not started\n"+
		"  · Optional step: zmgsautil: exit status 1\n")
}

func TestReporter_LongWarningGoesUnderTheLine(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{})
	long := strings.Repeat("very long cause ", 8)

	finish(r, clock, 0, "Check port conflicts", Warned, time.Second, errors.New(long))

	lines := strings.Split(out.String(), "\n")
	assert.Regexp(t, `Check port conflicts\s+warn\s+1\.0s$`, lines[0], "no room for the cause: the duration stays")
	assert.Contains(t, out.String(), "· very long cause")

	for _, l := range lines {
		assert.LessOrEqual(t, len([]rune(l)), reportWidth, l)
	}
}

func TestReporter_FailureShowsTheErrorAndHowToGoOn(t *testing.T) {
	r, out, errb, clock := newTestReporter(t, ReportOptions{})

	finish(r, clock, 0, "Prepare system users and directories", Applied, time.Second, nil)
	finish(r, clock, 2, "Check port conflicts", Failed, 3200*time.Millisecond, errors.New("port 25 is in use"))
	r.Summary(&Summary{Err: errors.New("bootstrap step failed: Check port conflicts: port 25 is in use")})

	assert.Contains(t, out.String(), "[2/3] Check port conflicts                  FAIL    3.2s\n      · port 25 is in use\n")
	assert.NotContains(t, out.String(), "Done in")
	assert.Equal(t, "\nFailed at step 2/3: Check port conflicts\n  port 25 is in use\n"+resumeHint+"\n", errb.String())
}

func TestReporter_FailureWithoutAStepIsAnError(t *testing.T) {
	r, _, errb, _ := newTestReporter(t, ReportOptions{})

	r.Summary(&Summary{Err: errors.New("writing history: disk full")})

	assert.Equal(t, "\nError: writing history: disk full\n"+resumeHint+"\n", errb.String())
}

func TestReporter_SecretsInMessagesAreMasked(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{})

	finish(r, clock, 0, "Prepare system users and directories", Failed, time.Second,
		errors.New("bind to ldap://admin:"+"hunter2"+"hunter2"+"@"+"ldap.example.com failed"))

	assert.NotContains(t, out.String(), "hunter2hunter2")
}

func TestReporter_TerminalRunIsColouredWithALiveLine(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{Color: true, Live: true, Dark: true})

	r.Observe(Event{Kind: EventStart, Index: 0, Step: step("Prepare system users and directories")})
	assert.Contains(t, out.String(), "\r\x1b[2K", "the running step has a live line")
	assert.Contains(t, out.String(), "0.0s")
	assert.NotContains(t, out.String(), "\n")

	clock.advance(2 * time.Second)

	r.mu.Lock()
	r.draw()
	r.mu.Unlock()

	assert.Contains(t, out.String(), "2.0s", "the live line shows the elapsed time")

	r.Observe(Event{Kind: EventFinish, Index: 0, Step: step("Prepare system users and directories"),
		Outcome: Applied, Elapsed: 2 * time.Second})
	finish(r, clock, 2, "Check port conflicts", AlreadyDone, 0, nil)
	finish(r, clock, 5, "Generate dhparam key", Warned, time.Second, errors.New("x"))

	all := out.String()
	for _, status := range []string{"ok", "skip", "warn"} {
		assert.Regexp(t, "\x1b\\[[0-9;]*m"+status+" +\x1b\\[m", all, "%s is coloured", status)
	}

	assert.Contains(t, out.String(), "38;2;139;195;74", "ok is the Carbonio success green (#8bc34a)")
	assert.Contains(t, out.String(), "38;2;255;193;7", "warn is the Carbonio amber (#ffc107)")
}

func TestReporter_LightTerminalGetsTheDarkerShades(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{Color: true})

	finish(r, clock, 0, "Prepare system users and directories", Applied, time.Second, nil)
	finish(r, clock, 5, "Generate dhparam key", Warned, time.Second, errors.New("x"))

	assert.Contains(t, out.String(), "38;2;90;143;42", "success on a light background (#5a8f2a)")
	assert.Contains(t, out.String(), "38;2;179;134;0", "warning on a light background (#b38600)")
}

func TestReporter_PlainOutputKeepsEscapesOutEvenOnALiveFreeRun(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{Color: false, Live: false})

	finish(r, clock, 0, "Prepare system users and directories", Failed, time.Second, errors.New("x"))
	r.Header(&Header{Version: "26.12.0", Hostname: "mx.example.com", Install: "new install"})

	assert.NotContains(t, out.String(), "\x1b")
	assert.NotContains(t, out.String(), "\r")
}

func TestReporter_StepsOutsideThePlanAreNumberedAfterIt(t *testing.T) {
	r, out, _, clock := newTestReporter(t, ReportOptions{})

	finish(r, clock, 9, "Late step", Applied, time.Second, nil)

	assert.Contains(t, out.String(), "[4/3] Late step")
}

func TestReporter_SummaryNextStepsAndURLs(t *testing.T) {
	r, out, _, _ := newTestReporter(t, ReportOptions{})

	r.Note(Note{Warn: true, Text: "unknown package installed for x"})
	r.Summary(&Summary{Next: Next{
		StartCommand: "systemctl start carbonio-appserver.target",
		Admin:        "https://mail.example.com:6071", Webmail: "https://mail.example.com",
	}})

	assert.Contains(t, out.String(), "Done in 0.0s · 0 applied · 0 already done · 1 warning\n")
	assert.Contains(t, out.String(), "Warnings:\n  · unknown package installed for x\n")
	assert.Contains(t, out.String(), "Services not started (start_services: no). Start them with: "+
		"systemctl start carbonio-appserver.target\n")
	assert.Contains(t, out.String(), "Admin console: https://mail.example.com:6071   Webmail: https://mail.example.com\n")

	r2, out2, _, _ := newTestReporter(t, ReportOptions{})
	r2.Summary(&Summary{})
	assert.NotContains(t, out2.String(), "Services not started")
	assert.NotContains(t, out2.String(), "Admin console")
	assert.NotContains(t, out2.String(), "Warnings:")
}

func TestReporter_Header(t *testing.T) {
	r, out, _, _ := newTestReporter(t, ReportOptions{})

	r.Header(&Header{
		Version: "26.12.0", Hostname: "mx.example.com", Install: "new install",
		Config: "/opt/product.config", ConfigKind: "legacy", Dropped: 27, LogPath: "/opt/zextras/log/bootstrap.1.log",
	})

	assert.Equal(t, "Carbonio Bootstrap 26.12.0 · mx.example.com · new install\n"+
		"Config:  /opt/product.config (legacy) · 27 keys not imported, see log\n"+
		"Log:     /opt/zextras/log/bootstrap.1.log\n\n", out.String())

	r2, out2, _, _ := newTestReporter(t, ReportOptions{})
	r2.Header(&Header{Hostname: "mx.example.com", Install: "existing install"})
	assert.Equal(t, "Carbonio Bootstrap · mx.example.com · existing install\n\n", out2.String(), "no file, no Config line")

	r3, out3, _, _ := newTestReporter(t, ReportOptions{})
	r3.Header(&Header{Config: "/x.yaml", ConfigKind: "yaml", Dropped: 1, DropLines: []string{"EXPANDMENU: obsolete"}})
	assert.Equal(t, "Carbonio Bootstrap\nConfig:  /x.yaml (yaml) · 1 key not imported:\n         · EXPANDMENU: obsolete\n\n",
		out3.String(), "--verbose lists the keys")

	r4, out4, _, _ := newTestReporter(t, ReportOptions{})
	r4.Header(&Header{Config: "/x.conf", ConfigKind: "legacy", Dropped: 2})
	assert.Contains(t, out4.String(), "2 keys not imported, use --verbose")
}

func TestFmtDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		400 * time.Millisecond: "0.4s", 3100 * time.Millisecond: "3.1s", 12 * time.Second: "12s",
		59 * time.Second: "59s", time.Minute: "1:00", 65 * time.Second: "1:05", 192 * time.Second: "3:12",
	} {
		assert.Equal(t, want, fmtDuration(d))
	}
}

func TestReporter_LongTitleIsCut(t *testing.T) {
	long := strings.Repeat("x", 80)
	r, out, _, clock := newTestReporter(t, ReportOptions{Plan: []PlanStep{{0, long}}})

	finish(r, clock, 0, long, Applied, time.Second, nil)

	assert.Contains(t, out.String(), strings.Repeat("x", maxTitleWidth-1)+"…")
}

func TestLog_NotesGoToTheHandlerAndWarningsAreCollected(t *testing.T) {
	var (
		got  []Note
		file bytes.Buffer
		out  bytes.Buffer
	)

	l := NewLog(&out, &file)
	l.OnNote(func(n Note) { got = append(got, n) })

	l.Info("hello %d", 1)
	l.Warn("careful")
	l.Detail("only in the file")

	assert.Equal(t, []Note{{Text: "hello 1"}, {Warn: true, Text: "careful"}}, got)
	assert.Empty(t, out.String(), "the handler replaces the output")
	assert.Contains(t, file.String(), "Warning: careful")
	assert.Contains(t, file.String(), "only in the file")
	assert.Equal(t, []string{"careful"}, l.takeWarnings())
	assert.Empty(t, l.takeWarnings())
}

func TestLog_WithoutHandlerWritesLines(t *testing.T) {
	var out bytes.Buffer

	l := NewLog(&out, nil)
	l.Info("a")
	l.Warn("b")

	assert.Equal(t, "a\nWarning: b\n", out.String())
}

func TestRun_ReportsStepWarningsInTheEvent(t *testing.T) {
	e, _, _ := testEnv(t)

	var events []Event

	e.Observe = func(ev Event) { events = append(events, ev) }

	_, err := Run(e, []Step{{Title: "W", Apply: func(e *Env) error { e.Log.Warn("watch out"); return nil }},
		{Title: "Clean", Apply: func(*Env) error { return nil }}}, false)
	require.NoError(t, err)

	var finishes []Event

	for _, ev := range events {
		if ev.Kind == EventFinish {
			finishes = append(finishes, ev)
		}
	}

	require.Len(t, finishes, 2)
	assert.Equal(t, []string{"watch out"}, finishes[0].Warnings)
	assert.Empty(t, finishes[1].Warnings, "a step starts with a clean slate")
}

func TestNextSteps(t *testing.T) {
	cfg := func() *bootstrap.Config {
		c := &bootstrap.Config{Roles: []string{bootstrap.RoleDirectoryServer, bootstrap.RoleProxy, bootstrap.RoleMTA}}
		c.Host.Hostname = "mx.example.com"
		c.Proxy.WebEnabled = true
		c.Proxy.Ports.HTTPS = 443

		return c
	}

	e := &Env{Cfg: cfg(), Facts: &Facts{Systemd: true}}
	n := NextSteps(e)
	assert.Equal(t, "systemctl start carbonio-directory-server.target carbonio-proxy.target carbonio-mta.target",
		n.StartCommand)
	assert.Equal(t, "https://mx.example.com:6071", n.Admin)
	assert.Equal(t, "https://mx.example.com", n.Webmail)

	e = &Env{Cfg: cfg(), Facts: &Facts{}}
	e.Cfg.StartServices = true
	e.Cfg.Host.PublicHostname = "mail.example.com"
	e.Cfg.Proxy.Ports.HTTPS = 8443
	n = NextSteps(e)
	assert.Empty(t, n.StartCommand, "the services were started")
	assert.Equal(t, "https://mail.example.com:8443", n.Webmail)

	e = &Env{Cfg: cfg(), Facts: &Facts{}}
	assert.Equal(t, "su - zextras -c 'zmcontrol start'", NextSteps(e).StartCommand, "no systemd: zmcontrol")

	e.Cfg.Roles = []string{bootstrap.RoleMTA}
	n = NextSteps(e)
	assert.Empty(t, n.Admin)
	assert.Empty(t, n.Webmail, "no proxy here")

	e = &Env{Cfg: &bootstrap.Config{}, Facts: &Facts{Systemd: true}}
	assert.Contains(t, NextSteps(e).StartCommand, "carbonio-appserver.target", "no roles: every target")
}
