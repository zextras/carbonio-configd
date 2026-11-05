// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// recordingFS wraps the real FS to record where the rewrite temp is created
// and to inject a rename failure.
type recordingFS struct {
	sys.FS
	tempDir   string
	renameErr error
}

func (r *recordingFS) CreateTemp(dir, pattern string) (sys.File, error) {
	r.tempDir = dir

	return r.FS.CreateTemp(dir, pattern)
}

func (r *recordingFS) Rename(oldpath, newpath string) error {
	if r.renameErr != nil {
		return r.renameErr
	}

	return r.FS.Rename(oldpath, newpath)
}

func newRewriteCM(t *testing.T, rfs *recordingFS) (*ConfigManager, string) {
	t.Helper()

	cm, _ := newTestCMWithExecutor(t)
	rfs.FS = cm.env.FS
	cm.env.FS = rfs

	baseDir := cm.mainConfig.BaseDir
	if err := os.WriteFile(filepath.Join(baseDir, "a.cf.in"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return cm, baseDir
}

func TestProcessRewrite_TempIsCreatedInDestinationDir(t *testing.T) {
	t.Parallel()

	rfs := &recordingFS{}
	cm, baseDir := newRewriteCM(t, rfs)

	err := cm.processRewrite(context.Background(), "a.cf.in", config.RewriteEntry{Value: "a.cf"})
	if err != nil {
		t.Fatal(err)
	}

	if rfs.tempDir != baseDir {
		t.Errorf("temp dir = %q, want destination dir %q", rfs.tempDir, baseDir)
	}
}

func TestProcessRewrite_RenameFailureIsNotMaskedByCopy(t *testing.T) {
	t.Parallel()

	rfs := &recordingFS{renameErr: &os.LinkError{Op: "rename", Err: syscall.EACCES}}
	cm, baseDir := newRewriteCM(t, rfs)

	err := cm.processRewrite(context.Background(), "a.cf.in", config.RewriteEntry{Value: "a.cf"})
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("err = %v, want EACCES", err)
	}

	if _, statErr := os.Stat(filepath.Join(baseDir, "a.cf")); statErr == nil {
		t.Error("destination must not be written after a non-EXDEV rename failure")
	}
}

func TestProcessRewrite_CrossDeviceFallsBackToCopy(t *testing.T) {
	t.Parallel()

	rfs := &recordingFS{renameErr: &os.LinkError{Op: "rename", Err: syscall.EXDEV}}
	cm, baseDir := newRewriteCM(t, rfs)

	err := cm.processRewrite(context.Background(), "a.cf.in", config.RewriteEntry{Value: "a.cf", Mode: "0640"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(baseDir, "a.cf"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
}
