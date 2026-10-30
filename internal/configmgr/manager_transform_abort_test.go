// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

// Legacy jylibs/state.py rewriteConfig renders into a tmp file and only moves it
// over the target after the last line, so a transform() raise leaves the target
// untouched. doConfigRewrites then carries on with the other steps; Go must also
// keep rewriting the other files of the cycle.
func TestDoRewrites_TransformErrorAbortsOnlyThatFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	cm, _ := newTestCMWithExecutor(t)
	baseDir := cm.mainConfig.BaseDir

	files := map[string]string{
		"bad.in":  "first line\nx=@@SPLIT unset_key@@\nlast line\n",
		"good.in": "ok=@@SPLIT set_key@@\n",
		"bad.out": "previous content\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, name), []byte(content), 0o644))
	}

	setLocal(cm, map[string]string{"set_key": "head tail"})

	entries := []plan.Rewrite{
		{Source: "bad.in", Target: "bad.out"},
		{Source: "good.in", Target: "good.out"},
	}

	pending, err := cm.doRewrites(context.Background(), entries)
	require.ErrorIs(t, err, transformer.ErrTransform)

	bad, readErr := os.ReadFile(filepath.Join(baseDir, "bad.out"))
	require.NoError(t, readErr)
	assert.Equal(t, "previous content\n", string(bad), "a failed rewrite must not touch its target")

	good, readErr := os.ReadFile(filepath.Join(baseDir, "good.out"))
	require.NoError(t, readErr)
	assert.Equal(t, "ok=head\n", string(good), "the other rewrites of the cycle still run")

	require.Len(t, pending, 1, "only the failed rewrite stays pending for the next cycle")
	assert.Equal(t, "bad.in", pending[0].Source)

	leftovers, readErr := os.ReadDir(tmpDir)
	require.NoError(t, readErr)
	assert.Empty(t, leftovers, "the temporary file must be removed")
}

// Legacy lookUpConfig has no try/except around transform() for FILE values: the
// raise propagates before the value reaches the cache, so the next lookup retries.
func TestLookupFileKey_TransformErrorIsNotCached(t *testing.T) {
	cm, _ := newTestCMWithExecutor(t)
	confDir := filepath.Join(cm.mainConfig.BaseDir, "conf")
	require.NoError(t, os.MkdirAll(confDir, 0o755))

	path := filepath.Join(confDir, "frag.cf")
	require.NoError(t, os.WriteFile(path, []byte("a=@@SPLIT unset_key@@\n"), 0o644))

	_, err := cm.lookupFileKey(context.Background(), "frag.cf")
	require.ErrorIs(t, err, transformer.ErrTransform)

	_, cached := cm.State.LookupFile("frag.cf")
	assert.False(t, cached, "a failed transform must not be cached")

	require.NoError(t, os.WriteFile(path, []byte("a=@@SPLIT set_key@@\n"), 0o644))
	setLocal(cm, map[string]string{"set_key": "head tail"})

	got, err := cm.lookupFileKey(context.Background(), "frag.cf")
	require.NoError(t, err)
	assert.Equal(t, "a=head", got)
}
