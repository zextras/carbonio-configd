// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//nolint:wrapcheck // RealFS is a thin boundary: os errors are returned verbatim, callers add context.
package sys

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/fileutil"
)

// File is an open file. *os.File satisfies it.
type File interface {
	io.Reader
	io.Writer
	io.StringWriter
	io.Closer
	// Name returns the path the file was opened or created with.
	Name() string
}

// FS is the filesystem boundary. Paths are absolute (or relative to the
// process working directory), exactly as with package os; errors are the
// os-level errors, so os.IsNotExist and errors.Is(err, fs.ErrNotExist) work.
type FS interface {
	ReadFile(name string) ([]byte, error)
	Open(name string) (File, error)
	OpenFile(name string, flag int, perm fs.FileMode) (File, error)
	// CreateTemp creates a new temporary file like os.CreateTemp.
	CreateTemp(dir, pattern string) (File, error)

	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
	Glob(pattern string) ([]string, error)
	Readlink(name string) (string, error)

	// WriteFileAtomic replaces name with data without ever exposing a
	// partial file: temp file in the same directory, fsync, chmod to perm,
	// rename, fsync of the directory. perm is enforced regardless of umask.
	WriteFileAtomic(name string, data []byte, perm fs.FileMode) error
	// CopyFile copies src over dst (the cross-device rename fallback).
	CopyFile(ctx context.Context, src, dst string) error

	Rename(oldpath, newpath string) error
	Remove(name string) error
	RemoveAll(name string) error
	MkdirAll(path string, perm fs.FileMode) error
	Chmod(name string, mode fs.FileMode) error
	Chown(name string, uid, gid int) error
	Symlink(oldname, newname string) error

	// AvailBytes reports the bytes available to unprivileged users on the
	// filesystem holding path (statfs f_bavail * f_bsize).
	AvailBytes(path string) (uint64, error)
}

// RealFS is the os filesystem.
type RealFS struct{}

// ReadFile implements FS.
//
//nolint:gosec // G304: paths are vetted by callers
func (RealFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// Open implements FS.
func (RealFS) Open(name string) (File, error) { return openFile(os.Open(name)) } //nolint:gosec // G304: see ReadFile

// OpenFile implements FS.
func (RealFS) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	return openFile(os.OpenFile(name, flag, perm)) //nolint:gosec // thin boundary
}

// CreateTemp implements FS.
func (RealFS) CreateTemp(dir, pattern string) (File, error) {
	return openFile(os.CreateTemp(dir, pattern))
}

// openFile keeps a nil *os.File from becoming a non-nil File interface.
func openFile(f *os.File, err error) (File, error) {
	if err != nil {
		return nil, err
	}

	return f, nil
}

// Stat implements FS.
func (RealFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// Lstat implements FS.
func (RealFS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

// ReadDir implements FS.
func (RealFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }

// WalkDir implements FS.
func (RealFS) WalkDir(root string, fn fs.WalkDirFunc) error { return filepath.WalkDir(root, fn) }

// Glob implements FS.
func (RealFS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }

// Readlink implements FS.
func (RealFS) Readlink(name string) (string, error) { return os.Readlink(name) }

// WriteFileAtomic implements FS via fileutil.AtomicWrite.
func (RealFS) WriteFileAtomic(name string, data []byte, perm fs.FileMode) error {
	return fileutil.AtomicWrite(name, data, perm)
}

// CopyFile implements FS via fileutil.CopyFile.
func (RealFS) CopyFile(ctx context.Context, src, dst string) error {
	return fileutil.CopyFile(ctx, src, dst)
}

// Rename implements FS.
func (RealFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove implements FS.
func (RealFS) Remove(name string) error { return os.Remove(name) }

// RemoveAll implements FS.
func (RealFS) RemoveAll(name string) error { return os.RemoveAll(name) }

// MkdirAll implements FS.
func (RealFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

// Chmod implements FS.
func (RealFS) Chmod(name string, mode fs.FileMode) error { return os.Chmod(name, mode) }

// Chown implements FS.
func (RealFS) Chown(name string, uid, gid int) error { return os.Chown(name, uid, gid) }

// Symlink implements FS.
func (RealFS) Symlink(oldname, newname string) error { return os.Symlink(oldname, newname) }

// AvailBytes implements FS.
func (RealFS) AvailBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}

	// #nosec G115 - block size and available blocks are always positive on real filesystems
	return st.Bavail * uint64(st.Bsize), nil //nolint:gosec
}
