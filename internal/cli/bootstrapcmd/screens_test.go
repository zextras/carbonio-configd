// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/bootstrap/tui"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// fakeScreens records what the pages were given; Apply drives the run the
// way the real page does.
type fakeScreens struct {
	configured []*tui.ConfigureOptions
	applied    []*tui.ApplyOptions
	action     tui.Action
	configErr  error
	applyErr   error
	events     []setup.Event
}

func (f *fakeScreens) screens() *Screens {
	return &Screens{
		Configure: func(_ context.Context, _ io.Reader, _ io.Writer, o *tui.ConfigureOptions) (tui.Action, error) {
			f.configured = append(f.configured, o)

			return f.action, f.configErr
		},
		Apply: func(_ context.Context, _ io.Reader, _ io.Writer, o *tui.ApplyOptions) error {
			f.applied = append(f.applied, o)

			if f.applyErr != nil {
				return f.applyErr
			}

			return o.Run(func(ev setup.Event) { f.events = append(f.events, ev) }, func() bool { return false })
		},
	}
}

func screenOpts() *RunOptions {
	return &RunOptions{Options: Options{Wizard: WizardMissing, Environ: runTestEnviron}}
}

func TestRunSetup_ApplyScreenRunsTheStepsAndReportsEvents(t *testing.T) {
	e, _ := runTestEnv(t)
	e.Facts = nil

	var applied []string

	fake := &fakeScreens{}
	session := &Session{Notice: "Licence text", Feed: tui.NewFeed(), LogPath: "/log", SavedPath: "/saved.yaml"}
	e.Log = setup.NewLog(session.Feed, nil)

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, screenOpts(),
		&Deps{Host: host{}, Terminal: true, Screens: fake.screens(), Session: session}, runTestSteps(&applied))

	require.Equal(t, 0, status, errb.String())
	assert.Equal(t, []string{"first", "second"}, applied)
	assert.Empty(t, fake.configured, "everything is given: no configure page")
	require.Len(t, fake.applied, 1)

	ao := fake.applied[0]
	assert.Equal(t, "Licence text", ao.Notice, "the apply page shows the notice when configure did not")
	assert.Equal(t, "new install", ao.Info.Install)
	assert.Equal(t, "/saved.yaml", ao.SavedPath)
	assert.Equal(t, "mail.example.com", ao.Hostname)
	require.Len(t, ao.Plan, 2)
	assert.Equal(t, "first", ao.Plan[0].Title)
	assert.Len(t, fake.events, 4, "start and finish of each step")
	assert.Empty(t, out.String(), "the plain output stays silent behind the screen")

	checks := ao.Preflight()
	assert.NotEmpty(t, checks)
}

func TestRunSetup_ApplyScreenFailureIsReported(t *testing.T) {
	e, _ := runTestEnv(t)

	var applied []string

	fake := &fakeScreens{applyErr: errors.New("boom")}
	session := &Session{Feed: tui.NewFeed()}

	var out, errb strings.Builder

	status := runSetup(e, strings.NewReader(""), &out, &errb, screenOpts(),
		&Deps{Host: host{}, Terminal: true, Screens: fake.screens(), Session: session}, runTestSteps(&applied))

	assert.Equal(t, 1, status)
	assert.Contains(t, errb.String(), "boom")
	assert.Contains(t, errb.String(), "run carbonio-bootstrap again")
}

func TestRunSetup_PlainFlagsKeepThePlainOutput(t *testing.T) {
	for name, mod := range map[string]func(*RunOptions){
		"--plain":           func(o *RunOptions) { o.Plain = true },
		"--non-interactive": func(o *RunOptions) { o.NonInteractive = true },
	} {
		t.Run(name, func(t *testing.T) {
			e, _ := runTestEnv(t)

			var applied []string

			opts := screenOpts()
			mod(opts)

			fake := &fakeScreens{}

			var out, errb strings.Builder

			status := runSetup(e, strings.NewReader(""), &out, &errb, opts,
				&Deps{Host: host{}, Terminal: true, Screens: fake.screens(), Session: &Session{}}, runTestSteps(&applied))

			require.Equal(t, 0, status, errb.String())
			assert.Empty(t, fake.applied)
			assert.Empty(t, fake.configured)
			assert.Equal(t, []string{"first", "second"}, applied)
		})
	}
}

func TestRun_ConfigureScreen(t *testing.T) {
	fake := &fakeScreens{action: tui.ActionDone}
	status, out, errb := runCmd(t, &Options{Wizard: WizardAll, Environ: runTestEnviron},
		Deps{Terminal: true, Screens: fake.screens()}, "")

	require.Equal(t, 0, status, errb)
	require.Len(t, fake.configured, 1)

	co := fake.configured[0]
	assert.False(t, co.Apply, "config only shows the result")
	assert.Equal(t, defaultSavePath, co.SavePath)
	assert.Empty(t, co.Notice)
	assert.Contains(t, out, "hostname: mail.example.com", "the YAML is printed after the page")
}

func TestRun_ConfigureScreenQuitAborts(t *testing.T) {
	fake := &fakeScreens{action: tui.ActionQuit}
	status, _, errb := runCmd(t, &Options{Wizard: WizardAll, Environ: runTestEnviron},
		Deps{Terminal: true, Screens: fake.screens()}, "")

	assert.Equal(t, 1, status)
	assert.Contains(t, errb, "aborted")

	fake = &fakeScreens{configErr: errors.New("no tty")}
	status, _, errb = runCmd(t, &Options{Wizard: WizardAll, Environ: runTestEnviron},
		Deps{Terminal: true, Screens: fake.screens()}, "")

	assert.Equal(t, 1, status)
	assert.Contains(t, errb, "no tty")
}

func TestRunConfigure_SaveCallbackAndNotice(t *testing.T) {
	f := systest.New()
	fake := &fakeScreens{action: tui.ActionApply}
	session := &Session{Notice: "Licence", Install: "new install"}
	opts := &Options{Wizard: WizardAll, Environ: runTestEnviron, Output: "/out.yaml"}

	status, _, errb := func() (int, string, string) {
		var out, errb strings.Builder

		deps := Deps{Host: host{}, Terminal: true, Screens: fake.screens(), Session: session}
		st := run(context.Background(), f.System(), strings.NewReader(""), &out, &errb, opts, &deps)

		return st, out.String(), errb.String()
	}()

	require.Equal(t, 0, status, errb)
	require.Len(t, fake.configured, 1)

	co := fake.configured[0]
	assert.True(t, co.Apply)
	assert.True(t, session.NoticeShown)
	assert.Equal(t, "Licence", co.Notice)
	assert.Equal(t, "new install", co.Info.Install)
	assert.Equal(t, "/out.yaml", co.SavePath)

	require.NoError(t, co.Save("/chosen.yaml"))
	assert.Equal(t, "/chosen.yaml", session.SavedPath)
	assert.True(t, f.FS.Exists("/chosen.yaml"))

	data, ok := f.FS.Data("/chosen.yaml")
	require.True(t, ok)
	assert.NotContains(t, string(data), "Sup3rSecret!")
}

func TestHelpers(t *testing.T) {
	f := systest.New()
	assert.Empty(t, osName(f.System()))

	f.FS.Put("/etc/os-release", []byte("NAME=x\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n"), 0o644)
	assert.Equal(t, "Ubuntu 24.04.1 LTS", osName(f.System()))

	f.FS.Put("/etc/os-release", []byte("NAME=x\n"), 0o644)
	assert.Empty(t, osName(f.System()))

	assert.True(t, noColor([]string{"A=b", "NO_COLOR=1"}))
	assert.False(t, noColor([]string{"NO_COLOR="}))
	assert.False(t, noColor(nil))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = l.Close() }()

	port := l.Addr().(*net.TCPAddr).Port
	assert.False(t, portFree(port))
	require.NoError(t, l.Close())
	assert.True(t, portFree(port))

	assert.False(t, useScreens(&Options{}, &Deps{}))
	assert.True(t, useScreens(&Options{}, &Deps{Screens: &Screens{}}))
	assert.False(t, useScreens(&Options{Plain: true}, &Deps{Screens: &Screens{}}))
	assert.False(t, useScreens(&Options{NonInteractive: true}, &Deps{Screens: &Screens{}}))
}
