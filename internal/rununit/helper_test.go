// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rununit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSpec writes s as the spec of kind/instance in a fresh 0700 directory.
func writeSpec(t *testing.T, kind Kind, instance string, s *spec, mode os.FileMode) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))

	data, err := json.Marshal(s)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, string(kind)+"-"+instance+".json"), data, mode))

	return dir
}

func TestLoadSpec(t *testing.T) {
	t.Parallel()

	dir := writeSpec(t, KindExec, "nginx", &spec{
		Path: "/bin/true", Args: []string{"a"}, Env: []string{"NOTIFY_SOCKET=/run/x"},
		Dir: "/", Stdout: "/tmp/out", Result: "",
	}, 0o600)

	s, err := loadSpec(dir, KindExec, "nginx")

	require.NoError(t, err)
	assert.Equal(t, "/bin/true", s.Path)
	assert.Equal(t, []string{"a"}, s.Args)
}

func TestLoadSpec_Rejects(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		s        *spec
		mode     os.FileMode
		instance string
	}{
		"relative program":   {&spec{Path: "bin/true"}, 0o600, "x"},
		"unclean program":    {&spec{Path: "/bin/../bin/true"}, 0o600, "x"},
		"LD_PRELOAD":         {&spec{Path: "/bin/true", Env: []string{"LD_PRELOAD=/evil.so"}}, 0o600, "x"},
		"PATH":               {&spec{Path: "/bin/true", Env: []string{"PATH=/evil"}}, 0o600, "x"},
		"env without value":  {&spec{Path: "/bin/true", Env: []string{"NOTIFY_SOCKET"}}, 0o600, "x"},
		"relative log":       {&spec{Path: "/bin/true", Stdout: "log"}, 0o600, "x"},
		"result elsewhere":   {&spec{Path: "/bin/true", Result: "/etc/passwd"}, 0o600, "x"},
		"group readable":     {&spec{Path: "/bin/true"}, 0o640, "x"},
		"executable":         {&spec{Path: "/bin/true"}, 0o700, "x"},
		"instance with /":    {&spec{Path: "/bin/true"}, 0o600, "../x"},
		"instance uppercase": {&spec{Path: "/bin/true"}, 0o600, "X"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			name := tc.instance
			if name == "../x" || name == "X" {
				name = "x"
			}

			dir := writeSpec(t, KindExec, name, tc.s, tc.mode)

			_, err := loadSpec(dir, KindExec, tc.instance)
			if name != tc.instance {
				require.Error(t, err)

				return
			}

			require.Error(t, err)
		})
	}
}

func TestLoadSpec_DirectoryMustBeClosed(t *testing.T) {
	t.Parallel()

	dir := writeSpec(t, KindExec, "x", &spec{Path: "/bin/true"}, 0o600)
	require.NoError(t, os.Chmod(dir, 0o770))

	_, err := loadSpec(dir, KindExec, "x")

	require.Error(t, err)
}

func TestLoadSpec_SymlinkIsRefused(t *testing.T) {
	t.Parallel()

	dir := writeSpec(t, KindExec, "real", &spec{Path: "/bin/true"}, 0o600)
	require.NoError(t, os.Symlink(filepath.Join(dir, "exec-real.json"), filepath.Join(dir, "exec-link.json")))

	_, err := loadSpec(dir, KindExec, "link")

	require.Error(t, err)
}

func TestMain_ForkingRecordsTheExitStatusAndOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))

	out := filepath.Join(dir, "forking-svc.out")
	rc := filepath.Join(dir, "forking-svc.rc")

	data, err := json.Marshal(&spec{
		Path: "/bin/sh", Args: []string{"-c", "echo started; echo oops >&2; exit 3"}, Stdout: out, Result: rc,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forking-svc.json"), data, 0o600))

	require.NoError(t, Main(dir, KindForking, "svc"), "the wrapper itself succeeds whatever the launcher did")

	var got result

	raw, err := os.ReadFile(rc)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, 3, got.Code)

	output, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "started\noops\n", string(output), "stderr follows stdout")
}

func TestMain_ForkingReportsAProgramThatCannotRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))

	rc := filepath.Join(dir, "forking-svc.rc")

	data, err := json.Marshal(&spec{Path: "/nonexistent/program", Result: rc})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forking-svc.json"), data, 0o600))

	require.NoError(t, Main(dir, KindForking, "svc"))

	raw, err := os.ReadFile(rc)
	require.NoError(t, err)

	var got result
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, -1, got.Code)
	assert.Contains(t, got.Error, "no such file")
}

func TestMain_RefusesWithoutASpec(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))

	require.Error(t, Main(dir, KindExec, "nothing"))
}

func TestMergeEnv(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"A=1", "NOTIFY_SOCKET=/new"},
		mergeEnv([]string{"A=1", "NOTIFY_SOCKET=/old"}, []string{"NOTIFY_SOCKET=/new"}))
}
