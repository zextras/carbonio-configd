// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package template

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

const fakeBase = "/opt/zextras"

func newFakeRewriter() (*Rewriter, *systest.Fakes) {
	fakes := systest.New()

	return NewRewriter(fakeBase, newMockLookup(), &state.State{}, fakes.System()), fakes
}

func TestAtomicReplace_RenameSameFilesystem(t *testing.T) {
	t.Parallel()

	r, fakes := newFakeRewriter()
	tmpPath := filepath.Join(fakeBase, "config.tmp")
	targetPath := filepath.Join(fakeBase, "config")
	fakes.FS.Put(tmpPath, []byte("content"), 0o600)

	require.NoError(t, r.atomicReplace(context.Background(), tmpPath, targetPath, 0o440))

	mode, ok := fakes.FS.Mode(targetPath)
	require.True(t, ok)
	assert.Equal(t, os.FileMode(0o440), mode.Perm())
	assert.False(t, fakes.FS.Exists(tmpPath), "tmp file must be gone after rename")
	assert.Equal(t, []string{
		"fs.chmod " + tmpPath + " mode=0440",
		"fs.rename " + tmpPath + " -> " + targetPath,
	}, fakes.Rec.Strings())
}

func TestAtomicReplace_ChmodFailure(t *testing.T) {
	t.Parallel()

	r, fakes := newFakeRewriter()

	err := r.atomicReplace(context.Background(), filepath.Join(fakeBase, "ghost"), "/tmp/never", 0o440)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to set file mode")
	assert.Equal(t, []string{"fs.chmod " + filepath.Join(fakeBase, "ghost") + " mode=0440"},
		fakes.Rec.Strings(), "a failed chmod must not be followed by a rename")
}

func TestAtomicReplace_CrossFilesystemFallback(t *testing.T) {
	t.Parallel()

	r, fakes := newFakeRewriter()
	tmpPath := filepath.Join(fakeBase, "config.tmp")
	targetPath := filepath.Join(fakeBase, "config")
	fakes.FS.Put(tmpPath, []byte("content"), 0o600)
	fakes.FS.Fail = func(op, path string) error {
		if op == "Rename" {
			return &os.LinkError{Op: "rename", Old: path, New: targetPath, Err: syscall.EXDEV}
		}

		return nil
	}

	require.NoError(t, r.atomicReplace(context.Background(), tmpPath, targetPath, 0o440))

	data, ok := fakes.FS.Data(targetPath)
	require.True(t, ok)
	assert.Equal(t, "content", string(data))
	assert.False(t, fakes.FS.Exists(tmpPath), "tmp file must be removed after the copy")

	mode, _ := fakes.FS.Mode(targetPath)
	assert.Equal(t, os.FileMode(0o440), mode.Perm())
	assert.Equal(t, []string{
		"fs.chmod " + tmpPath + " mode=0440",
		"fs.rename " + tmpPath + " -> " + targetPath, // attempted, fails with EXDEV
		"fs.copy " + tmpPath + " -> " + targetPath,
		"fs.chmod " + targetPath + " mode=0440",
		"fs.remove " + tmpPath,
	}, fakes.Rec.Strings())
}

func TestScanAndWrite_FlushFailure(t *testing.T) {
	t.Parallel()

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

	r := NewRewriter(dir, nil, nil, sys.Real())
	err = r.scanAndWrite(context.Background(), src, closed)
	require.Error(t, err, "writing through a closed file must fail")
}

func TestAtomicReplace_NonEXDEVRenameErrorDoesNotCopy(t *testing.T) {
	t.Parallel()

	r, fakes := newFakeRewriter()
	tmpPath := filepath.Join(fakeBase, "config.tmp")
	targetPath := filepath.Join(fakeBase, "config")
	fakes.FS.Put(tmpPath, []byte("content"), 0o600)
	fakes.FS.Fail = func(op, path string) error {
		if op == "Rename" {
			return &fs.PathError{Op: "rename", Path: path, Err: syscall.EISDIR}
		}

		return nil
	}

	err := r.atomicReplace(context.Background(), tmpPath, targetPath, 0o440)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rename temporary file")
	assert.NotContains(t, fakes.Rec.Strings(), "fs.copy "+tmpPath+" -> "+targetPath, "no copy fallback")
	assert.False(t, fakes.FS.Exists(targetPath), "target must be untouched")
}

func TestRewriteConfig_RejectsPathEscape(t *testing.T) {
	t.Parallel()

	rewriter, fakes := newFakeRewriter()
	fakes.FS.Put(filepath.Join(fakeBase, "a.in"), []byte("x"), 0o644)

	for _, tc := range []struct{ source, target string }{
		{"a.in", "../escaped.conf"},
		{"../a.in", "a.conf"},
		{"/etc/passwd", "a.conf"},
		{"a.in", "/tmp/abs.conf"},
	} {
		err := rewriter.RewriteConfig(context.Background(), tc.source, tc.target, "")
		require.ErrorIs(t, err, ErrPathEscapesBaseDir, "%+v", tc)
	}

	assert.Empty(t, fakes.Rec.Strings(), "a rejected path must not touch the filesystem")
}

func TestRewriteConfig_MasksSpecialModeBits(t *testing.T) {
	t.Parallel()

	rewriter, fakes := newFakeRewriter()
	fakes.FS.Put(filepath.Join(fakeBase, "a.in"), []byte("x"), 0o644)

	require.NoError(t, rewriter.RewriteConfig(context.Background(), "a.in", "a.conf", "4755"))

	mode, ok := fakes.FS.Mode(filepath.Join(fakeBase, "a.conf"))
	require.True(t, ok)
	assert.Equal(t, os.FileMode(0o755), mode, "setuid bit must be dropped")
}
