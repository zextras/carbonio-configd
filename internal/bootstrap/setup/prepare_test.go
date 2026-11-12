// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func prepEnv(t *testing.T) (*Env, *systest.Fakes) {
	t.Helper()

	e, fakes, _ := testEnv(t)
	fakes.FS.PutDir("/opt/zextras/conf", 0o755)
	fakes.FS.Put("/usr/lib/sysusers.d/carbonio-core.conf", nil, 0o644)
	fakes.FS.Put("/usr/lib/sysusers.d/carbonio-mta.conf", nil, 0o644)
	fakes.FS.Put("/usr/lib/tmpfiles.d/carbonio-core.conf", nil, 0o644)
	fakes.FS.Put("/usr/lib/tmpfiles.d/other.conf", nil, 0o644)
	fakes.Exec.OnCmdline("id -u zextras", systest.Reply("998\n"))
	fakes.Exec.OnCmdline("id -g zextras", systest.Reply("997\n"))

	return e, fakes
}

func TestPrepareHost_RunsSnippetsAndRestoresOwner(t *testing.T) {
	e, fakes := prepEnv(t)

	require.NoError(t, prepareHost(e))

	lines := fakes.Exec.Lines()
	assert.Contains(t, lines, "systemd-sysusers /usr/lib/sysusers.d/carbonio-core.conf")
	assert.Contains(t, lines, "systemd-sysusers /usr/lib/sysusers.d/carbonio-mta.conf")
	assert.Contains(t, lines, "systemd-tmpfiles --create /usr/lib/tmpfiles.d/carbonio-core.conf")
	assert.NotContains(t, strings.Join(lines, "\n"), "other.conf")

	uid, gid, ok := fakes.FS.Owner("/opt/zextras/conf")
	require.True(t, ok)
	assert.Equal(t, [2]int{998, 997}, [2]int{uid, gid})

	// conf is root's while tmpfiles runs.
	chowns := fakes.Rec.Of("fs.chown")

	assert.Equal(t, []string{"/opt/zextras/conf 0:0", "/opt/zextras/conf 998:997"}, chowns)
}

func TestPrepareHost_FailuresAreLoggedNotFatal(t *testing.T) {
	e, fakes := prepEnv(t)
	fakes.Exec.OnPath("systemd-sysusers", systest.Fail(1, "boom"))
	fakes.Exec.OnPath("systemd-tmpfiles", systest.Fail(1, "bang"))

	var file bytes.Buffer

	e.Log = NewLog(nil, &file)

	require.NoError(t, prepareHost(e))
	assert.Contains(t, file.String(), "systemd-sysusers /usr/lib/sysusers.d/carbonio-core.conf failed (ignored)")
	assert.Contains(t, file.String(), "systemd-tmpfiles /usr/lib/tmpfiles.d/carbonio-core.conf failed (ignored)")

	uid, _, _ := fakes.FS.Owner("/opt/zextras/conf")
	assert.Equal(t, 998, uid)
}

func TestPrepareHost_NoSystemdTools(t *testing.T) {
	e, fakes := prepEnv(t)
	notFound := func(*sys.Cmd) (sys.Result, error) { return sys.Result{}, &exec.Error{Name: "x", Err: exec.ErrNotFound} }
	fakes.Exec.OnPath("systemd-sysusers", notFound)
	fakes.Exec.OnPath("systemd-tmpfiles", notFound)

	var file bytes.Buffer

	e.Log = NewLog(nil, &file)

	require.NoError(t, prepareHost(e))
	assert.Contains(t, file.String(), "systemd-sysusers is not installed")
	assert.Contains(t, file.String(), "systemd-tmpfiles is not installed")
	// Skipped after the first miss: one call per tool.
	assert.Len(t, fakes.Exec.Lines(), 2+2)
}

func TestPrepareHost_NoConfDirSkipsChown(t *testing.T) {
	e, fakes, _ := testEnv(t)
	fakes.FS.Put("/usr/lib/tmpfiles.d/carbonio-core.conf", nil, 0o644)

	require.NoError(t, prepareHost(e))
	assert.Equal(t, []string{"systemd-tmpfiles --create /usr/lib/tmpfiles.d/carbonio-core.conf"}, fakes.Exec.Lines())
}

func TestPrepareHost_ChownErrors(t *testing.T) {
	e, fakes := prepEnv(t)
	fakes.Exec.OnCmdline("id -u zextras", systest.Reply("nope\n"))

	err := prepareHost(e)
	require.ErrorContains(t, err, "not a number")

	fakes.Exec.OnCmdline("id -u zextras", systest.Fail(1, "no such user"))
	assert.Error(t, prepareHost(e))
}

func TestStepPrepareHost_FirstStepAlwaysApplies(t *testing.T) {
	first := Steps()[0]
	assert.Equal(t, "Prepare system users and directories", first.Title)
	assert.Empty(t, first.Name)
	assert.Nil(t, first.Done)
	assert.Nil(t, first.When)
}

func TestNotice(t *testing.T) {
	_, fakes, _ := testEnv(t)
	paths := Paths{LicenseNotice: "/opt/zextras/.mini_alue_ce"}

	assert.Empty(t, Notice(fakes.FS, paths))

	fakes.FS.Put(paths.LicenseNotice, []byte("hello licence\n"), 0o644)
	assert.Equal(t, "hello licence", Notice(fakes.FS, paths))
}

func TestWriteNotice(t *testing.T) {
	var b bytes.Buffer

	WriteNotice(&b, strings.Repeat("word ", 30)+"\nsecond")

	out := b.String()
	assert.True(t, strings.HasPrefix(out, "Starting Carbonio Bootstrap...\n"+strings.Repeat("-", 80)+"\n"))

	for _, l := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, len(l), 80)
	}

	assert.Contains(t, out, "\nsecond\n"+strings.Repeat("-", 80))

	b.Reset()
	WriteNotice(&b, "")
	assert.Equal(t, "Starting Carbonio Bootstrap...\n", b.String())
}

func TestWrap(t *testing.T) {
	assert.Equal(t, []string{"aaa bbb", "ccc"}, Wrap("aaa bbb ccc", 8))
	assert.Equal(t, []string{"abcd", "efgh", "ij"}, Wrap("abcdefghij", 4))
	assert.Equal(t, []string{"a", "", "b"}, Wrap("a\n\nb", 10))
	assert.Equal(t, []string{"abc"}, Wrap("abc", 0))
}

func TestRun_ObserverAndStop(t *testing.T) {
	e, _, _ := testEnv(t)

	var events []Event

	e.Observe = func(ev Event) { events = append(events, ev) }

	stop := false
	e.Stop = func() bool { return stop }

	steps := []Step{
		{Title: "A", Apply: func(*Env) error { return nil }},
		{Title: "B", When: func(*Env) bool { return false }},
		{Title: "C", Done: func(*Env) (bool, error) { return true, nil }},
		{Title: "D", Optional: true, Apply: func(*Env) error { return errors.New("soft") }},
		{Title: "E", Apply: func(*Env) error { stop = true; return nil }},
		{Title: "F", Apply: func(*Env) error { t.Fatal("must not run after the stop"); return nil }},
	}

	results, err := Run(e, steps, false)
	require.ErrorIs(t, err, ErrStopped)
	assert.Len(t, results, 5)

	var got []string

	for _, ev := range events {
		kind := "start"
		if ev.Kind == EventFinish {
			kind = ev.Outcome.String()
		}

		got = append(got, ev.Step.Title+":"+kind)
	}

	assert.Equal(t, []string{"A:start", "A:applied", "B:skipped", "C:done", "D:start", "D:warning", "E:start", "E:applied"}, got)
}

func TestRun_ObserverSeesFailure(t *testing.T) {
	e, _, _ := testEnv(t)

	var last Event

	e.Observe = func(ev Event) { last = ev }

	_, err := Run(e, []Step{{Title: "X", Apply: func(*Env) error { return errors.New("kaput") }}}, false)
	require.ErrorIs(t, err, ErrStepFailed)
	assert.Equal(t, Failed, last.Outcome)
	assert.ErrorContains(t, last.Err, "kaput")

	// A dry run emits nothing.
	last = Event{}
	_, err = Run(e, []Step{{Title: "X", Apply: func(*Env) error { return nil }}}, true)
	require.NoError(t, err)
	assert.Nil(t, last.Step)
}
