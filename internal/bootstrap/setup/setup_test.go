// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func testEnv(t *testing.T) (*Env, *systest.Fakes, *bytes.Buffer) {
	t.Helper()

	fakes := systest.New()
	out := &bytes.Buffer{}
	e := &Env{
		Ctx:   context.Background(),
		Sys:   fakes.System(),
		Log:   NewLog(out, nil),
		Paths: Paths{Base: "/opt/zextras", History: "/opt/zextras/.install_history"},
		Facts: &Facts{},
	}
	e.perlLib = "/opt/zextras/common/lib/perl5"

	return e, fakes, out
}

func TestRun_ChecksBeforeApplyingAndRecordsStages(t *testing.T) {
	e, fakes, out := testEnv(t)

	var applied []string

	apply := func(name string) func(*Env) error {
		return func(*Env) error { applied = append(applied, name); return nil }
	}
	steps := []Step{
		{Name: "configA", Title: "A", Apply: apply("A")},
		{Name: "configB", Title: "B", When: func(*Env) bool { return false }, Apply: apply("B")},
		{Name: "configC", Title: "C", Done: func(*Env) (bool, error) { return true, nil }, Apply: apply("C")},
		{Title: "D", Optional: true, Apply: func(*Env) error { return errors.New("boom") }},
		{Name: "configE", Title: "E", Apply: apply("E")},
	}

	results, err := Run(e, steps, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "E"}, applied)

	var got []Outcome
	for _, r := range results {
		got = append(got, r.Outcome)
	}

	assert.Equal(t, []Outcome{Applied, Skipped, AlreadyDone, Warned, Applied}, got)
	assert.Contains(t, out.String(), "Warning: D: boom")

	hist, _ := fakes.FS.Data(e.Paths.History)
	var stages []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(hist)), "\n") {
		_, rest, _ := strings.Cut(line, ": ")
		stages = append(stages, rest)
	}

	assert.Equal(t, []string{"CONFIG SESSION START", "CONFIGURED BEGIN", "CONFIGURED configA",
		"CONFIGURED configC", "CONFIGURED configE", "CONFIGURED END", "CONFIG SESSION COMPLETE"}, stages)

	// The history the run wrote ends the new install.
	var f Facts
	f.ReadHistory([]byte("1: INSTALLED carbonio-core\n" + string(hist)))
	assert.False(t, f.NewInstall)
}

func TestRun_StopsAtFailureAndDryRunChangesNothing(t *testing.T) {
	e, fakes, _ := testEnv(t)

	ran := false
	steps := []Step{
		{Title: "A", Apply: func(*Env) error { return errors.New("boom") }},
		{Title: "B", Apply: func(*Env) error { ran = true; return nil }},
	}

	_, err := Run(e, steps, false)
	require.ErrorIs(t, err, ErrStepFailed)
	assert.False(t, ran)

	e2, fakes2, _ := testEnv(t)
	steps[0].Done = func(*Env) (bool, error) { return false, errors.New("LDAP not configured") }
	results, err := Run(e2, steps, true)
	require.NoError(t, err)
	assert.Equal(t, WouldApply, results[0].Outcome)
	require.NoError(t, err)
	assert.Equal(t, WouldApply, results[1].Outcome)
	assert.False(t, ran)
	assert.False(t, fakes2.FS.Exists(e2.Paths.History))
	_ = fakes
}

func TestReadHistory(t *testing.T) {
	for name, tc := range map[string]struct {
		history string
		newInst bool
	}{
		"package installed, never configured": {"1: INSTALLED carbonio-core\n", true},
		"configured":                          {"1: INSTALLED carbonio-core\n2: CONFIGURED END\n", false},
		"upgrade after configuration": {
			"1: INSTALLED carbonio-core\n2: CONFIGURED END\n3: INSTALL SESSION START\n4: INSTALLED carbonio-core\n", true,
		},
		"interrupted session": {
			"1: INSTALLED carbonio-core\n2: CONFIGURED END\n3: CONFIG SESSION START\n4: CONFIGURED BEGIN\n", true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var f Facts
			f.ReadHistory([]byte(tc.history))
			assert.Equal(t, tc.newInst, f.NewInstall)
		})
	}
}

func TestRunProv_BatchesOnStdinAndReportsErrors(t *testing.T) {
	e, fakes, _ := testEnv(t)

	var stdin string

	fakes.Exec.OnPath("runuser", func(c *sys.Cmd) (sys.Result, error) {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(c.Stdin)
		stdin = b.String()
		_, _ = c.Stderr.Write([]byte("ERROR: account.NO_SUCH_DOMAIN (no such domain: x)\n"))

		return sys.Result{ExitCode: 2}, nil
	})

	var p Prov
	p.Add("ca", "admin@example.com", "pa\"ss\\word")
	p.Add("mcf", "zimbraFoo", "a b")

	err := e.RunProv(&p)
	require.ErrorIs(t, err, ErrProv)
	assert.Contains(t, err.Error(), "NO_SUCH_DOMAIN")
	assert.Equal(t, `"ca" "admin@example.com" "pa\"ss\\word"`+"\n"+`"mcf" "zimbraFoo" "a b"`+"\nexit\n", stdin)

	call := fakes.Exec.Calls()[0]
	assert.Equal(t, []string{"-u", "zextras", "--", "sh", "-c", zextrasShim, "/opt/zextras/bin/zmprov", "-r", "-m", "-l"},
		call.Args)
	assert.NotContains(t, strings.Join(call.Args, " "), "word")
}
