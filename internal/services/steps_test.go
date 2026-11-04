// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// failMkdir makes creating dir fail, as a read-only parent does.
func failMkdir(f *systest.Fakes, dir string) {
	f.FS.Fail = func(op, path string) error {
		if op == "MkdirAll" && path == dir {
			return &fs.PathError{Op: "mkdir", Path: path, Err: fs.ErrPermission}
		}

		return nil
	}
}

func TestRunSteps_RunInOrderAndStopAtTheFirstFailure(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	var order []string

	record := func(name string, err error) Step {
		return Step{Hook: func(context.Context) error {
			order = append(order, name)

			return err
		}}
	}

	boom := errors.New("boom")

	err := ctl.runSteps(context.Background(), []Step{record("a", nil), record("b", boom), record("c", nil)})

	require.ErrorIs(t, err, boom)
	assert.Equal(t, []string{"a", "b"}, order)
	assert.Empty(t, f.Exec.Lines())
}

func TestRunStep_Mkdir(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	require.NoError(t, ctl.runStep(context.Background(), &Step{Mkdir: []string{"/data/a/b", "/data/c"}}))
	assert.True(t, f.FS.Exists("/data/a/b"))
	assert.True(t, f.FS.Exists("/data/c"))

	failMkdir(f, "/blocked/sub")

	err := ctl.runStep(context.Background(), &Step{Mkdir: []string{"/blocked/sub"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create /blocked/sub")
}

func TestRunStep_UnlessExistsSkipsTheStep(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.Put("/data/db", []byte("x"), 0o644)

	require.NoError(t, ctl.runStep(context.Background(), &Step{
		UnlessExists: "/data/db",
		Mkdir:        []string{"/data/never"},
	}))
	assert.False(t, f.FS.Exists("/data/never"))

	require.NoError(t, ctl.runStep(context.Background(), &Step{
		UnlessExists: "/data/missing",
		Mkdir:        []string{"/data/created"},
	}))
	assert.True(t, f.FS.Exists("/data/created"))
}

func TestRunStep_RemoveStaleKeepsWhatDoesNotMatch(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.FS.PutDir("/tmp-root", 0o755)
	f.FS.PutDir("/tmp-root/job-1", 0o755)
	f.FS.Put("/tmp-root/job-1/f", []byte("x"), 0o644)
	f.FS.PutDir("/tmp-root/keep", 0o755)
	f.FS.Put("/tmp-root/job-file", []byte("x"), 0o644)

	require.NoError(t, ctl.runStep(context.Background(), &Step{RemoveStale: &StaleDirs{Dir: "/tmp-root", Prefix: "job-"}}))

	assert.False(t, f.FS.Exists("/tmp-root/job-1"), "matching directory removed with its content")
	assert.True(t, f.FS.Exists("/tmp-root/keep"))
	assert.True(t, f.FS.Exists("/tmp-root/job-file"), "a file is not a leftover directory")

	require.NoError(t, ctl.runStep(context.Background(),
		&Step{RemoveStale: &StaleDirs{Dir: "/absent", Prefix: "job-"}}), "an unreadable directory is not an error")
}

func TestRunStep_RunSudoAndPlain(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	require.NoError(t, ctl.runStep(context.Background(), &Step{Run: &RunStep{Path: "/bin/plain", Args: []string{"a", "b"}}}))
	require.NoError(t, ctl.runStep(context.Background(), &Step{Run: &RunStep{Path: "/bin/rooted", Args: []string{"c"}, Sudo: true}}))

	assert.Equal(t, []string{"/bin/plain a b", sudoBin + " -n /bin/rooted c"}, f.Exec.Lines())
}

func TestRunStep_RunFailureCarriesCommandAndOutput(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath("/bin/bad", systest.Fail(3, "it broke\n"))

	err := ctl.runStep(context.Background(), &Step{Run: &RunStep{Path: "/bin/bad", Args: []string{"x"}}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "/bin/bad x: it broke")
}

func TestRunStep_RunBestEffortSwallowsTheFailure(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	f.Exec.OnPath("/bin/bad", systest.Fail(3, "it broke"))

	require.NoError(t, ctl.runStep(context.Background(), &Step{Run: &RunStep{Path: "/bin/bad", BestEffort: true}}))
	assert.Equal(t, []string{"/bin/bad"}, f.Exec.Lines(), "it did run")
}

func TestRunStep_RunIfExists(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	step := &Step{Run: &RunStep{Path: "/bin/tool", IfExists: "/etc/input"}}

	require.NoError(t, ctl.runStep(context.Background(), step))
	assert.Empty(t, f.Exec.Lines(), "input absent: nothing runs")

	f.FS.Put("/etc/input", []byte("x"), 0o644)

	require.NoError(t, ctl.runStep(context.Background(), step))
	assert.Equal(t, []string{"/bin/tool"}, f.Exec.Lines())
}

func TestRunStep_RunSkipOnNoRewrite(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	step := &Step{Run: &RunStep{Path: "/bin/regen", SkipOnNoRewrite: true}}

	require.NoError(t, ctl.runStep(WithNoRewriteContext(context.Background()), step))
	assert.Empty(t, f.Exec.Lines())

	require.NoError(t, ctl.runStep(context.Background(), step))
	assert.Equal(t, []string{"/bin/regen"}, f.Exec.Lines())
}

func TestRunPreStart_SetupRunsBeforeTheHooksAndFailsTheStart(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	var order []string

	def := &ServiceDef{
		Name:  "svc",
		Setup: []Step{{Hook: func(context.Context) error { order = append(order, "setup"); return nil }}},
		PreStart: []Hook{func(context.Context, *ServiceManager) error {
			order = append(order, "hook")

			return nil
		}},
	}

	require.NoError(t, ctl.runPreStart(context.Background(), "svc", NewServiceManager(ctl), def))
	assert.Equal(t, []string{"setup", "hook"}, order)

	failMkdir(f, "/blocked/sub")

	def.Setup = []Step{{Mkdir: []string{"/blocked/sub"}}}
	order = nil

	err := ctl.runPreStart(context.Background(), "svc", NewServiceManager(ctl), def)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pre-start hook failed for svc")
	assert.Empty(t, order, "a failed Setup stops the start before the hooks")
}
