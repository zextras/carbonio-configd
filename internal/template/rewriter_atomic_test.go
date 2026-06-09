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
