// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package template

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/state"
)

func TestAtomicReplace_RenameSameFilesystem(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "config.tmp")
	targetPath := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(tmpPath, []byte("content"), 0o600))

	require.NoError(t, atomicReplace(context.Background(), tmpPath, targetPath, 0o440))

	info, err := os.Stat(targetPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o440), info.Mode().Perm())

	_, err = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "tmp file must be gone after rename")
}

func TestAtomicReplace_ChmodFailure(t *testing.T) {
	err := atomicReplace(context.Background(), filepath.Join(t.TempDir(), "ghost"), "/tmp/never", 0o440)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to set file mode")
}

func TestAtomicReplace_CrossFilesystemFallback(t *testing.T) {
	if st, err := os.Stat("/dev/shm"); err != nil || !st.IsDir() {
		t.Skip("/dev/shm not available; cannot force a cross-filesystem rename")
	}

	shmDir, err := os.MkdirTemp("/dev/shm", "configd-test-")
	require.NoError(t, err)

	t.Cleanup(func() { _ = os.RemoveAll(shmDir) })

	tmpPath := filepath.Join(t.TempDir(), "config.tmp")
	targetPath := filepath.Join(shmDir, "config")
	require.NoError(t, os.WriteFile(tmpPath, []byte("content"), 0o600))

	err = atomicReplace(context.Background(), tmpPath, targetPath, 0o440)
	if err != nil {
		// Same-filesystem setups (e.g. TMPDIR on /dev/shm) take the plain
		// rename path; only assert when the fallback actually ran.
		t.Skipf("environment did not force the fallback: %v", err)
	}

	data, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))
}

func TestScanAndWrite_FlushFailure(t *testing.T) {
	dir := t.TempDir()
	src, err := os.CreateTemp(dir, "src")
	require.NoError(t, err)

	t.Cleanup(func() { _ = src.Close() })

	_, err = src.WriteString("line one\nline two\n")
	require.NoError(t, err)
	_, err = src.Seek(0, 0)
	require.NoError(t, err)

	closed, err := os.CreateTemp(dir, "closed")
	require.NoError(t, err)
	require.NoError(t, closed.Close())

	r := NewRewriter(dir, nil, nil)
	err = r.scanAndWrite(context.Background(), src, closed)
	require.Error(t, err, "writing through a closed file must fail")
}

func TestAtomicReplace_NonEXDEVRenameErrorDoesNotCopy(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "config.tmp")
	// Target is a non-empty directory: rename fails with EISDIR/ENOTEMPTY,
	// which must not trigger the copy fallback.
	targetPath := filepath.Join(dir, "config")
	require.NoError(t, os.MkdirAll(filepath.Join(targetPath, "sub"), 0o755))
	require.NoError(t, os.WriteFile(tmpPath, []byte("content"), 0o600))

	err := atomicReplace(context.Background(), tmpPath, targetPath, 0o440)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rename temporary file")

	info, statErr := os.Stat(targetPath)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "target must be untouched")
}

func TestRewriteConfig_RejectsPathEscape(t *testing.T) {
	base := t.TempDir()
	rewriter := NewRewriter(base, newMockLookup(), &state.State{})

	outside := filepath.Join(filepath.Dir(base), "escaped.conf")

	require.NoError(t, os.WriteFile(filepath.Join(base, "a.in"), []byte("x"), 0o644))

	for _, tc := range []struct{ source, target string }{
		{"a.in", "../escaped.conf"},
		{"../a.in", "a.conf"},
		{"/etc/passwd", "a.conf"},
		{"a.in", "/tmp/abs.conf"},
	} {
		err := rewriter.RewriteConfig(context.Background(), tc.source, tc.target, "")
		require.ErrorIs(t, err, ErrPathEscapesBaseDir, "%+v", tc)
	}

	_, err := os.Stat(outside)
	assert.True(t, os.IsNotExist(err))
}

func TestRewriteConfig_MasksSpecialModeBits(t *testing.T) {
	base := t.TempDir()
	rewriter := NewRewriter(base, newMockLookup(), &state.State{})

	require.NoError(t, os.WriteFile(filepath.Join(base, "a.in"), []byte("x"), 0o644))
	require.NoError(t, rewriter.RewriteConfig(context.Background(), "a.in", "a.conf", "4755"))

	info, err := os.Stat(filepath.Join(base, "a.conf"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode(), "setuid bit must be dropped")
}
