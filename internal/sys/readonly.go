// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package sys

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// ErrReadOnly is returned, wrapped, by every operation of a ReadOnly system
// that would change the host: writing, renaming or removing a file, changing
// a mode or owner, running a command, signalling a process.
var ErrReadOnly = errors.New("read-only system: operation refused")

// ReadOnly returns a System that can observe the host but never change it. It
// is the structural guarantee behind "configd shadow": whatever code runs on
// it, a mutating call fails with ErrReadOnly instead of reaching the host.
//
//   - FS: every read passes through to s.FS; opening a file for writing (any
//     of O_WRONLY, O_RDWR, O_APPEND, O_CREATE, O_TRUNC), creating a temporary
//     file, atomic writes, copies, renames, removals, mkdir, chmod, chown and
//     symlinks are refused.
//   - Exec: every command is refused. Even a "read-only" command cannot be
//     told from a mutating one by its argv alone, so none runs.
//   - Proc: the process table and the identity of the running process pass
//     through; signals are refused.
//   - Clock: passes through; waiting is not a host mutation.
func ReadOnly(s System) System {
	return System{
		Exec:  readOnlyExec{},
		FS:    readOnlyFS{FS: s.FS},
		Proc:  readOnlyProc{Proc: s.Proc},
		Clock: s.Clock,
	}
}

// refused builds the error of a refused operation. The message names the
// operation and its target only: command arguments may carry secrets.
func refused(op, target string) error {
	return fmt.Errorf("%w: %s %s", ErrReadOnly, op, target)
}

// readOnlyExec refuses every command.
type readOnlyExec struct{}

var _ Exec = readOnlyExec{}

// Run implements Exec.
func (readOnlyExec) Run(_ context.Context, c *Cmd) (Result, error) {
	return Result{ExitCode: -1}, refused("exec", c.Path)
}

// Start implements Exec.
func (readOnlyExec) Start(_ context.Context, c *Cmd) (Process, error) {
	return nil, refused("exec", c.Path)
}

// writeFlags are the os.OpenFile flags that make an open a mutation.
const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC

// readOnlyFS forwards the read methods to the wrapped FS and refuses the rest.
// It forwards explicitly instead of embedding the interface: a method added to
// FS later does not compile here until it is classified as a read or a write,
// so it can never pass through by accident.
type readOnlyFS struct{ FS FS }

var _ FS = readOnlyFS{}

// ReadFile implements FS.
func (r readOnlyFS) ReadFile(name string) ([]byte, error) { return r.FS.ReadFile(name) }

// Open implements FS.
func (r readOnlyFS) Open(name string) (File, error) { return r.FS.Open(name) }

// Stat implements FS.
func (r readOnlyFS) Stat(name string) (fs.FileInfo, error) { return r.FS.Stat(name) }

// Lstat implements FS.
func (r readOnlyFS) Lstat(name string) (fs.FileInfo, error) { return r.FS.Lstat(name) }

// ReadDir implements FS.
func (r readOnlyFS) ReadDir(name string) ([]fs.DirEntry, error) { return r.FS.ReadDir(name) }

// WalkDir implements FS.
func (r readOnlyFS) WalkDir(root string, fn fs.WalkDirFunc) error { return r.FS.WalkDir(root, fn) }

// Glob implements FS.
func (r readOnlyFS) Glob(pattern string) ([]string, error) { return r.FS.Glob(pattern) }

// Readlink implements FS.
func (r readOnlyFS) Readlink(name string) (string, error) { return r.FS.Readlink(name) }

// AvailBytes implements FS.
func (r readOnlyFS) AvailBytes(path string) (uint64, error) { return r.FS.AvailBytes(path) }

// OpenFile implements FS: only read-only opens pass.
func (r readOnlyFS) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	if flag&writeFlags != 0 {
		return nil, refused("open for write", name)
	}

	return r.FS.OpenFile(name, flag, perm)
}

// CreateTemp implements FS.
func (readOnlyFS) CreateTemp(dir, pattern string) (File, error) {
	return nil, refused("create temp", dir+"/"+pattern)
}

// WriteFileAtomic implements FS.
func (readOnlyFS) WriteFileAtomic(name string, _ []byte, _ fs.FileMode) error {
	return refused("write", name)
}

// CopyFile implements FS.
func (readOnlyFS) CopyFile(_ context.Context, _, dst string) error { return refused("copy to", dst) }

// Rename implements FS.
func (readOnlyFS) Rename(_, newpath string) error { return refused("rename to", newpath) }

// Remove implements FS.
func (readOnlyFS) Remove(name string) error { return refused("remove", name) }

// RemoveAll implements FS.
func (readOnlyFS) RemoveAll(name string) error { return refused("remove", name) }

// MkdirAll implements FS.
func (readOnlyFS) MkdirAll(path string, _ fs.FileMode) error { return refused("mkdir", path) }

// Chmod implements FS.
func (readOnlyFS) Chmod(name string, _ fs.FileMode) error { return refused("chmod", name) }

// Chown implements FS.
func (readOnlyFS) Chown(name string, _, _ int) error { return refused("chown", name) }

// Symlink implements FS.
func (readOnlyFS) Symlink(_, newname string) error { return refused("symlink", newname) }

// readOnlyProc forwards the process-table reads and refuses signals; like
// readOnlyFS it forwards explicitly so a new Proc method must be classified.
type readOnlyProc struct{ Proc Proc }

var _ Proc = readOnlyProc{}

// Table implements Proc.
func (r readOnlyProc) Table() ([]ProcEntry, error) { return r.Proc.Table() }

// Entry implements Proc.
func (r readOnlyProc) Entry(pid int) (ProcEntry, bool) { return r.Proc.Entry(pid) }

// Alive implements Proc.
func (r readOnlyProc) Alive(pid int) bool { return r.Proc.Alive(pid) }

// Zombie implements Proc.
func (r readOnlyProc) Zombie(pid int) bool { return r.Proc.Zombie(pid) }

// Pid implements Proc.
func (r readOnlyProc) Pid() int { return r.Proc.Pid() }

// Ppid implements Proc.
func (r readOnlyProc) Ppid() int { return r.Proc.Ppid() }

// UID implements Proc.
func (r readOnlyProc) UID() int { return r.Proc.UID() }

// Umask implements Proc.
func (r readOnlyProc) Umask() os.FileMode { return r.Proc.Umask() }

// LookupGID implements Proc.
func (r readOnlyProc) LookupGID(group string) (int, error) { return r.Proc.LookupGID(group) }

// Signal implements Proc.
func (readOnlyProc) Signal(pid int, sig syscall.Signal) error {
	return refused("signal", fmt.Sprintf("pid %d (%d)", pid, sig))
}

// SignalGroup implements Proc.
func (readOnlyProc) SignalGroup(pgid int, sig syscall.Signal) error {
	return refused("signal", fmt.Sprintf("pgid %d (%d)", pgid, sig))
}
