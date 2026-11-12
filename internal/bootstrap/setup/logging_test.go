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

	"github.com/zextras/carbonio-configd/internal/redact"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// logLines returns the log file lines without their timestamp.
func logLines(file *bytes.Buffer) []string {
	var out []string

	for l := range strings.SplitSeq(strings.TrimRight(file.String(), "\n"), "\n") {
		_, text, _ := strings.Cut(l, " ")
		out = append(out, text)
	}

	return out
}

// openssl prints its progress a character per write: one log line per line
// of output, not one per write (that was 950 of 1416 lines of a real run).
func TestRun_CommandOutputIsLoggedALineAtATime(t *testing.T) {
	e, fakes, _ := testEnv(t)

	var file bytes.Buffer

	e.Log = NewLog(nil, &file)

	fakes.Exec.OnPath("openssl", func(c *sys.Cmd) (sys.Result, error) {
		for _, s := range []string{".", ".", "+", "+\n", "\n", "...", "+\r\n", "tail"} {
			_, _ = c.Stderr.Write([]byte(s))
		}

		return sys.Result{}, nil
	})

	require.NoError(t, e.Root("openssl", "dhparam"))
	assert.Equal(t, []string{"*** Running as root: openssl dhparam", "..++", "...+", "tail"}, logLines(&file))
}

func TestRun_BriefOutputIsLoggedOnlyOnFailureOrVerbose(t *testing.T) {
	e, fakes, _ := testEnv(t)

	var file bytes.Buffer

	e.Log = NewLog(nil, &file)

	fakes.Exec.OnCmdline("tool ok", func(c *sys.Cmd) (sys.Result, error) {
		_, _ = c.Stdout.Write([]byte("** Copying a to b\n"))

		return sys.Result{}, nil
	})
	fakes.Exec.OnCmdline("tool bad", func(c *sys.Cmd) (sys.Result, error) {
		_, _ = c.Stdout.Write([]byte("** Copying a to b\nERROR: no space\n"))

		return sys.Result{ExitCode: 1}, &sys.ExitError{Code: 1}
	})

	require.NoError(t, e.Run(&Cmd{Path: "tool", Args: []string{"ok"}, Brief: true}))
	assert.NotContains(t, file.String(), "Copying", "a success logs the command line only")

	require.Error(t, e.Run(&Cmd{Path: "tool", Args: []string{"bad"}, Brief: true}))
	assert.Contains(t, file.String(), "ERROR: no space", "a failure logs the output")

	file.Reset()

	e.Verbose = true
	require.NoError(t, e.Run(&Cmd{Path: "tool", Args: []string{"ok"}, Brief: true}))
	assert.Contains(t, file.String(), "** Copying a to b")
}

func TestShownArgs_KeepsTheCommandOnOneLine(t *testing.T) {
	assert.Equal(t, `rpm -qa --qf "%{NAME} install ok installed\n" carbonio-*`,
		shownArgs([]string{"rpm", "-qa", "--qf", "%{NAME} install ok installed\n", "carbonio-*"}))
	assert.Equal(t, `x "" y`, shownArgs([]string{"x", "", "y"}))
	assert.Equal(t, "zmprov -l --password "+redact.Placeholder, shownArgs([]string{"zmprov", "-l", "--password", "s3cret"}))
}

func TestLogSummary(t *testing.T) {
	step := func(title string) *Step { return &Step{Title: title} }
	results := []Result{
		{Step: step("a"), Outcome: Applied, Warnings: []string{"port 10024 in use"}},
		{Step: step("b"), Outcome: AlreadyDone},
		{Step: step("c"), Outcome: Warned, Err: errors.New("zmgsautil exited 1")},
		{Step: step("d"), Outcome: Skipped},
	}

	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "Configuration complete."},
		{ErrStopped, "Result: stopped by the operator; run carbonio-bootstrap again to go on"},
		{errors.New("boom"), "Result: failed: boom"},
	} {
		e, _, _ := testEnv(t)

		var file bytes.Buffer

		e.Log = NewLog(nil, &file)

		LogSummary(e, results, tc.err, 71*time.Second)

		assert.Equal(t, []string{
			"Warning: a: port 10024 in use",
			"Warning: c: zmgsautil exited 1",
			"Summary: 1 applied, 1 already done, 1 with warnings, in 1m11s",
			tc.want,
		}, logLines(&file))
	}
}
