// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
)

func plan(n int) []PlanStep {
	var p []PlanStep

	for i := range n {
		p = append(p, PlanStep{Index: i, Title: fmt.Sprintf("Step number %d", i), Done: i == 1})
	}

	return p
}

func newApply(t *testing.T, n int, mod func(*ApplyOptions)) *ApplyModel {
	t.Helper()

	clock := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	o := &ApplyOptions{
		Plan:      plan(n),
		Feed:      NewFeed(),
		Hostname:  "mail.example.com",
		Info:      Info{OS: "Ubuntu 24.04"},
		SavedPath: "/etc/carbonio/bootstrap.yaml",
		LogPath:   "/opt/zextras/log/bootstrap.log",
		NoColor:   true,
		Now:       func() time.Time { clock = clock.Add(5 * time.Second); return clock },
	}

	if mod != nil {
		mod(o)
	}

	m := NewApply(o)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	return m
}

func ev(kind setup.EventKind, i int, out setup.Outcome, err error) setup.Event {
	return setup.Event{
		Kind: kind, Index: i, Step: &setup.Step{Title: fmt.Sprintf("Step number %d", i)},
		Outcome: out, Err: err, Elapsed: 1500 * time.Millisecond,
	}
}

func feed(m *ApplyModel, events ...setup.Event) {
	for _, e := range events {
		m.opts.Feed.Observe(e)
	}

	m.Update(spinner.TickMsg{})
}

func TestApply_ProgressAndMarkers(t *testing.T) {
	m := newApply(t, 4, nil)
	cmd := m.Init()
	require.NotNil(t, cmd)
	assert.True(t, m.running)

	out := screen(m)
	assert.Contains(t, out, "CARBONIO BOOTSTRAP")
	assert.Contains(t, out, "by Zextras")
	assert.Contains(t, out, "review & apply")
	assert.Contains(t, out, "1 / 4")
	assert.Contains(t, out, "↷ Step number 1")
	assert.Contains(t, out, "4 steps")
	assert.Contains(t, out, "1 already done")
	assert.Contains(t, out, "/etc/carbonio/bootstrap.yaml")
	assert.Contains(t, out, "checking")

	feed(m,
		ev(setup.EventStart, 0, 0, nil),
		ev(setup.EventFinish, 0, setup.Applied, nil),
		ev(setup.EventFinish, 1, setup.AlreadyDone, nil),
		ev(setup.EventStart, 2, 0, nil),
	)
	out = screen(m)
	assert.Contains(t, out, "✓ Step number 0")
	assert.Contains(t, out, "2s") // 1.5s rounds to 2s
	assert.Contains(t, out, "2 / 4")
	assert.Contains(t, out, "Step number 3")
	assert.Equal(t, stateRunning, m.rows[2].state)

	m.opts.Feed.Write([]byte("warning: first\nsecond line\npart"))
	assert.Contains(t, screen(m), "second line")

	m.Update(finishedMsg{})
	assert.True(t, m.finished)
	assert.Contains(t, screen(m), "Configuration complete")

	_, quit := m.Update(keyMsg("q"))
	assert.NotNil(t, quit)
	assert.True(t, m.quit)

	fin, err := m.Result()
	assert.NoError(t, err)
	assert.True(t, fin)
}

func TestApply_Failure(t *testing.T) {
	m := newApply(t, 3, nil)
	m.Init()

	boom := errors.New("ldap: bind failed")
	feed(m, ev(setup.EventStart, 0, 0, nil), ev(setup.EventFinish, 0, setup.Failed, boom))
	m.Update(finishedMsg{err: boom})

	out := screen(m)
	assert.Contains(t, out, "✗ Step number 0")
	assert.Contains(t, out, "ldap: bind failed")
	assert.Contains(t, out, "Fix the cause")
	assert.Contains(t, out, "failed")

	_, err := m.Result()
	assert.Equal(t, boom, err)
}

func TestApply_Warning(t *testing.T) {
	m := newApply(t, 2, nil)
	feed(m, ev(setup.EventFinish, 0, setup.Warned, errors.New("slow")), ev(setup.EventFinish, 5, setup.Skipped, nil))

	out := screen(m)
	assert.Contains(t, out, "! Step number 0")
	assert.Equal(t, stateSkipped, m.rows[len(m.rows)-1].state, "unknown steps are added")
	assert.NotContains(t, out, "Step number 5", "skipped steps are not listed")

	done, total := m.progress()
	assert.Equal(t, 2, done) // warned + already done
	assert.Equal(t, 2, total)
}

func TestApply_StopAfterCurrentStep(t *testing.T) {
	m := newApply(t, 3, nil)
	m.Init()
	feed(m, ev(setup.EventStart, 0, 0, nil))

	press2(m, "ctrl+c")
	assert.True(t, m.stopping)
	assert.True(t, m.opts.Feed.Stopping())
	assert.False(t, m.quit, "ctrl+c does not leave while a step runs")
	assert.Contains(t, screen(m), "Stopping after the current step")

	feed(m, ev(setup.EventFinish, 0, setup.Applied, nil))
	m.Update(finishedMsg{err: setup.ErrStopped})
	assert.Contains(t, screen(m), "Run carbonio-bootstrap again to resume")

	press2(m, "ctrl+c")
	assert.True(t, m.quit)
}

func press2(m *ApplyModel, k string) { m.Update(keyMsg(k)) }

func TestApply_OverflowAndRunningInView(t *testing.T) {
	m := newApply(t, 40, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	out := screen(m)
	assert.Contains(t, out, "⋮")
	assert.Contains(t, out, "more")
	assert.Len(t, strings.Split(out, "\n"), 24)

	feed(m, ev(setup.EventStart, 30, 0, nil))
	out = screen(m)
	assert.Contains(t, out, "Step number 30")

	feed(m, ev(setup.EventFinish, 30, setup.Failed, errors.New("x")))
	assert.Contains(t, screen(m), "Step number 30")
}

func TestApply_LogViewer(t *testing.T) {
	m := newApply(t, 3, nil)

	for i := range 60 {
		fmt.Fprintf(m.opts.Feed, "log line %d\n", i)
	}

	press2(m, "l")
	assert.True(t, m.logView)
	assert.Contains(t, screen(m), "log line 59")

	for _, k := range []string{"up", "k", "pgup", "g", "down", "j", "pgdown", "G", "x"} {
		press2(m, k)
	}

	assert.Contains(t, screen(m), "log line 59")
	press2(m, "esc")
	assert.False(t, m.logView)

	// Tick refreshes the open viewer.
	press2(m, "l")
	m.opts.Feed.Write([]byte("fresh\n"))
	m.Update(spinner.TickMsg{})
	press2(m, "l")
	assert.False(t, m.logView)
}

func TestApply_WelcomeThenRun(t *testing.T) {
	ran := make(chan struct{}, 1)
	m := newApply(t, 2, func(o *ApplyOptions) {
		o.Notice = "Some licence text"
		o.Run = func(obs setup.Observer, stop func() bool) error {
			obs(ev(setup.EventStart, 0, 0, nil))
			ran <- struct{}{}

			return nil
		}
	})

	cmd := m.Init()
	require.NotNil(t, cmd)
	assert.False(t, m.running)
	assert.Contains(t, screen(m), "Some licence text")

	_, start := m.Update(keyMsg("enter"))
	require.NotNil(t, start)
	assert.True(t, m.running)

	msg := start()
	assert.IsType(t, finishedMsg{}, msg)
	<-ran
	m.Update(msg)
	assert.True(t, m.finished)
	assert.Equal(t, stateRunning, m.rows[0].state)
}

func TestApply_WelcomeQuit(t *testing.T) {
	m := newApply(t, 2, func(o *ApplyOptions) { o.Notice = "text" })
	_, cmd := m.Update(keyMsg("q"))
	assert.NotNil(t, cmd)

	fin, err := m.Result()
	assert.Error(t, err)
	assert.True(t, fin)
}

func TestApply_TooSmallAndBackground(t *testing.T) {
	m := newApply(t, 2, nil)
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	assert.Contains(t, screen(m), "Terminal too small")
	m.Update(tea.BackgroundColorMsg{})
}

func TestApply_PreflightMessage(t *testing.T) {
	m := newApply(t, 2, func(o *ApplyOptions) {
		o.Preflight = func() []Check {
			return []Check{{CheckOK, "running as root"}, {CheckWarn, "memory 1024 MB, 8192 MB recommended"}}
		}
	})

	cmd := m.preflight()
	require.NotNil(t, cmd)
	m.Update(cmd())

	out := screen(m)
	assert.Contains(t, out, "✓ running as root")
	assert.Contains(t, out, "! memory 1024 MB")
}

func TestFeed_LinesAndRedactionBoundary(t *testing.T) {
	f := NewFeed()
	f.Write([]byte("a\r\nb"))
	f.Write([]byte("c\n"))
	assert.Equal(t, []string{"a", "bc"}, f.logLines())
	f.Write([]byte("tail"))
	assert.Equal(t, []string{"a", "bc", "tail"}, f.logLines())
	assert.False(t, f.Stopping())
}

type portHost struct{ fakeHost }

func (portHost) MemoryMB() int { return 1024 }

func TestPreflight(t *testing.T) {
	r := bootstrap.NewResolved(nil)
	host := portHost{fakeHost{roles: allRoles()}}
	require.NoError(t, r.ApplyDefaults(host))

	in := &PreflightInput{
		Cfg: &r.Config, Host: host, Root: false,
		Resolver: fakeResolver{hosts: map[string][]string{"mail.example.com": {"10.0.0.5"}}},
		PortFree: func(p int) bool { return p != 25 },
	}

	got := Preflight(context.Background(), in)

	var text []string
	for _, c := range got {
		text = append(text, fmt.Sprintf("%d:%s", c.Status, c.Text))
	}

	all := strings.Join(text, "\n")
	assert.Contains(t, all, "1:not running as root")
	assert.Contains(t, all, "0:mail.example.com")
	assert.Contains(t, all, "ports in use: 25")
	assert.Contains(t, all, "1:memory 1024 MB, 8192 MB recommended")
	assert.Contains(t, all, "has no MX")

	// Root, enough memory, no resolver: only the local checks.
	in.Root, in.Resolver, in.PortFree = true, nil, func(int) bool { return true }
	got = Preflight(context.Background(), in)
	assert.Equal(t, "running as root", got[0].Text)
	assert.Contains(t, got[1].Text, "needed ports free")

	assert.Equal(t, []int{25, 389}, neededPorts(&bootstrap.Config{Roles: []string{"mta", "directory-server"}, LDAP: bootstrap.LDAPConfig{Port: 389}}))
}

func TestApply_StepThatWarnedShowsAsWarning(t *testing.T) {
	m := newApply(t, 4, nil)
	m.Init()

	e := ev(setup.EventFinish, 2, setup.Applied, nil)
	e.Warnings = []string{"skipped: appserver not started"}
	feed(m, ev(setup.EventStart, 2, 0, nil), e)

	assert.Contains(t, screen(m), "! Step number 2")
}
