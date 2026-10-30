// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
)

type node struct {
	data     []byte
	mode     fs.FileMode // permission bits plus fs.ModeDir / fs.ModeSymlink
	link     string
	uid, gid int
}

// FS is an in-memory sys.FS. Parent directories are implicit: writing a file
// under a path that was never created works, as if every ancestor existed.
// Mutations are recorded (kind "fs.*"); reads are recorded only with
// RecordReads.
type FS struct {
	rec *Recorder

	mu     sync.Mutex
	nodes  map[string]*node
	tmpSeq int

	// RecordReads also records ReadFile/Open/Stat/ReadDir as "fs.read".
	RecordReads bool
	// Fail, when set, is consulted before every operation with its name
	// ("ReadFile", "WriteFileAtomic", "Rename", ...) and path; a non-nil
	// result is returned instead of performing the operation.
	Fail func(op, path string) error
	// Avail is returned by AvailBytes (default 1 TiB).
	Avail uint64
}

// NewFS returns an empty filesystem recording into rec.
func NewFS(rec *Recorder) *FS {
	return &FS{rec: rec, nodes: map[string]*node{}, Avail: 1 << 40}
}

func clean(p string) string { return path.Clean(p) }

// Put creates or replaces a regular file without recording it (test setup).
func (f *FS) Put(name string, data []byte, mode fs.FileMode) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nodes[clean(name)] = &node{data: slices.Clone(data), mode: mode.Perm()}
}

// PutDir creates a directory without recording it (test setup).
func (f *FS) PutDir(name string, mode fs.FileMode) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nodes[clean(name)] = &node{mode: mode.Perm() | fs.ModeDir}
}

// PutLink creates a symlink without recording it (test setup).
func (f *FS) PutLink(name, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nodes[clean(name)] = &node{link: target, mode: fs.ModeSymlink | 0o777}
}

// Data returns a file's content; ok is false when it does not exist.
func (f *FS) Data(name string) (data []byte, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.resolve(clean(name))
	if !ok || n.mode.IsDir() {
		return nil, false
	}

	return slices.Clone(n.data), true
}

// Mode returns a path's mode (without following a final symlink).
func (f *FS) Mode(name string) (fs.FileMode, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.nodes[clean(name)]
	if !ok {
		return 0, false
	}

	return n.mode, true
}

// Exists reports whether name exists (without following a final symlink).
func (f *FS) Exists(name string) bool {
	_, ok := f.Mode(name)

	return ok
}

// Paths lists every path in the filesystem, sorted.
func (f *FS) Paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, 0, len(f.nodes))
	for p := range f.nodes {
		out = append(out, p)
	}

	slices.Sort(out)

	return out
}

// resolve follows symlinks (callers hold f.mu).
func (f *FS) resolve(p string) (*node, bool) {
	for range 16 {
		n, ok := f.nodes[p]
		if !ok {
			return nil, false
		}

		if n.mode&fs.ModeSymlink == 0 {
			return n, true
		}

		if path.IsAbs(n.link) {
			p = clean(n.link)
		} else {
			p = clean(path.Join(path.Dir(p), n.link))
		}
	}

	return nil, false
}

func (f *FS) check(op, p string) error {
	if f.Fail != nil {
		return f.Fail(op, p)
	}

	return nil
}

func pathErr(op, p string, err error) error { return &fs.PathError{Op: op, Path: p, Err: err} }

func (f *FS) readRec(p string) {
	if f.RecordReads {
		f.rec.Record("fs.read", p)
	}
}

// ReadFile implements sys.FS.
func (f *FS) ReadFile(name string) ([]byte, error) {
	f.readRec(name)

	if err := f.check("ReadFile", name); err != nil {
		return nil, err
	}

	data, ok := f.Data(name)
	if !ok {
		return nil, pathErr("open", name, fs.ErrNotExist)
	}

	return data, nil
}

// Open implements sys.FS.
func (f *FS) Open(name string) (sys.File, error) {
	f.readRec(name)

	return f.openFile("Open", name, os.O_RDONLY, 0)
}

// OpenFile implements sys.FS.
func (f *FS) OpenFile(name string, flag int, perm fs.FileMode) (sys.File, error) {
	if flag&(os.O_CREATE|os.O_WRONLY|os.O_RDWR|os.O_TRUNC|os.O_APPEND) != 0 {
		f.rec.Recordf("fs.open", "%s flag=%#x perm=%04o", name, flag, perm.Perm())
	}

	return f.openFile("OpenFile", name, flag, perm)
}

func (f *FS) openFile(op, name string, flag int, perm fs.FileMode) (sys.File, error) {
	if err := f.check(op, name); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	p := clean(name)
	n, ok := f.resolve(p)

	switch {
	case ok && flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0:
		return nil, pathErr("open", name, fs.ErrExist)
	case !ok && flag&os.O_CREATE == 0:
		return nil, pathErr("open", name, fs.ErrNotExist)
	case !ok:
		n = &node{mode: perm.Perm()}
		f.nodes[p] = n
	case n.mode.IsDir() && flag&(os.O_WRONLY|os.O_RDWR) != 0:
		return nil, pathErr("open", name, errors.New("is a directory"))
	}

	if flag&os.O_TRUNC != 0 {
		n.data = nil
	}

	return &memFile{fs: f, name: name, n: n, flag: flag}, nil
}

// CreateTemp implements sys.FS.
func (f *FS) CreateTemp(dir, pattern string) (sys.File, error) {
	if dir == "" {
		dir = os.TempDir()
	}

	f.mu.Lock()
	f.tmpSeq++
	seq := f.tmpSeq
	f.mu.Unlock()

	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}

	name := filepath.Join(dir, fmt.Sprintf("%s%06d%s", prefix, seq, suffix))
	f.rec.Record("fs.create-temp", name)

	return f.openFile("CreateTemp", name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

type memFile struct {
	fs     *FS
	name   string
	n      *node
	flag   int
	off    int
	closed bool
}

func (m *memFile) Name() string { return m.name }

func (m *memFile) Read(b []byte) (int, error) {
	m.fs.mu.Lock()
	defer m.fs.mu.Unlock()

	if m.closed {
		return 0, os.ErrClosed
	}

	if m.off >= len(m.n.data) {
		return 0, io.EOF
	}

	n := copy(b, m.n.data[m.off:])
	m.off += n

	return n, nil
}

func (m *memFile) Write(b []byte) (int, error) {
	m.fs.mu.Lock()
	defer m.fs.mu.Unlock()

	if m.closed {
		return 0, os.ErrClosed
	}

	if m.flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		return 0, pathErr("write", m.name, errors.New("bad file descriptor"))
	}

	if m.flag&os.O_APPEND != 0 {
		m.off = len(m.n.data)
	}

	end := m.off + len(b)
	if end > len(m.n.data) {
		m.n.data = append(m.n.data, make([]byte, end-len(m.n.data))...)
	}

	copy(m.n.data[m.off:], b)
	m.off = end

	return len(b), nil
}

func (m *memFile) WriteString(s string) (int, error) { return m.Write([]byte(s)) } //nolint:gocritic // is WriteString

func (m *memFile) Close() error {
	m.fs.mu.Lock()
	defer m.fs.mu.Unlock()

	if m.closed {
		return os.ErrClosed
	}

	m.closed = true

	return nil
}

type info struct {
	name string
	n    node
}

func (i *info) Name() string       { return i.name }
func (i *info) Size() int64        { return int64(len(i.n.data)) }
func (i *info) Mode() fs.FileMode  { return i.n.mode }
func (i *info) ModTime() time.Time { return Epoch }
func (i *info) IsDir() bool        { return i.n.mode.IsDir() }
func (i *info) Sys() any           { return nil }

// Stat implements sys.FS.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	f.readRec(name)

	if err := f.check("Stat", name); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.resolve(clean(name))
	if !ok {
		return nil, pathErr("stat", name, fs.ErrNotExist)
	}

	return &info{name: path.Base(name), n: *n}, nil
}

// Lstat implements sys.FS.
func (f *FS) Lstat(name string) (fs.FileInfo, error) {
	f.readRec(name)

	if err := f.check("Lstat", name); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.nodes[clean(name)]
	if !ok {
		return nil, pathErr("lstat", name, fs.ErrNotExist)
	}

	return &info{name: path.Base(name), n: *n}, nil
}

// ReadDir implements sys.FS.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	f.readRec(name)

	if err := f.check("ReadDir", name); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	dir := clean(name)
	if n, ok := f.resolve(dir); !ok || !n.mode.IsDir() {
		return nil, pathErr("open", name, fs.ErrNotExist)
	}

	var out []fs.DirEntry

	for p, n := range f.nodes {
		if p != dir && path.Dir(p) == dir {
			out = append(out, fs.FileInfoToDirEntry(&info{name: path.Base(p), n: *n}))
		}
	}

	slices.SortFunc(out, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })

	return out, nil
}

// WalkDir implements sys.FS (lexical order, like filepath.WalkDir).
func (f *FS) WalkDir(root string, fn fs.WalkDirFunc) error {
	st, err := f.Lstat(root)
	if err != nil {
		return fn(root, nil, err)
	}

	err = f.walk(root, fs.FileInfoToDirEntry(st), fn)
	if errors.Is(err, fs.SkipDir) || errors.Is(err, fs.SkipAll) {
		return nil
	}

	return err
}

func (f *FS) walk(p string, d fs.DirEntry, fn fs.WalkDirFunc) error {
	if err := fn(p, d, nil); err != nil || !d.IsDir() {
		if errors.Is(err, fs.SkipDir) && d.IsDir() {
			return nil
		}

		return err
	}

	entries, err := f.ReadDir(p)
	if err != nil {
		if walkErr := fn(p, d, err); walkErr != nil {
			return walkErr
		}
	}

	for _, e := range entries {
		if err := f.walk(path.Join(p, e.Name()), e, fn); err != nil {
			if errors.Is(err, fs.SkipDir) {
				break
			}

			return err
		}
	}

	return nil
}

// Glob implements sys.FS.
func (f *FS) Glob(pattern string) ([]string, error) {
	if err := f.check("Glob", pattern); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for p := range f.nodes {
		ok, err := path.Match(pattern, p)
		if err != nil {
			return nil, err //nolint:wrapcheck // path.ErrBadPattern
		}

		if ok {
			out = append(out, p)
		}
	}

	slices.Sort(out)

	return out, nil
}

// Readlink implements sys.FS.
func (f *FS) Readlink(name string) (string, error) {
	if err := f.check("Readlink", name); err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.nodes[clean(name)]
	if !ok || n.mode&fs.ModeSymlink == 0 {
		return "", pathErr("readlink", name, fs.ErrNotExist)
	}

	return n.link, nil
}

// WriteFileAtomic implements sys.FS: the file ends up with exactly perm.
func (f *FS) WriteFileAtomic(name string, data []byte, perm fs.FileMode) error {
	f.rec.Recordf("fs.write-atomic", "%s mode=%04o", name, perm.Perm())

	if err := f.check("WriteFileAtomic", name); err != nil {
		return err
	}

	f.mu.Lock()
	f.nodes[clean(name)] = &node{data: slices.Clone(data), mode: perm.Perm()}
	f.mu.Unlock()

	return nil
}

// CopyFile implements sys.FS.
func (f *FS) CopyFile(_ context.Context, src, dst string) error {
	f.rec.Recordf("fs.copy", "%s -> %s", src, dst)

	if err := f.check("CopyFile", src); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.resolve(clean(src))
	if !ok {
		return pathErr("open", src, fs.ErrNotExist)
	}

	f.nodes[clean(dst)] = &node{data: slices.Clone(n.data), mode: n.mode.Perm()}

	return nil
}

// Rename implements sys.FS.
func (f *FS) Rename(oldpath, newpath string) error {
	f.rec.Recordf("fs.rename", "%s -> %s", oldpath, newpath)

	if err := f.check("Rename", oldpath); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.nodes[clean(oldpath)]
	if !ok {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrNotExist}
	}

	delete(f.nodes, clean(oldpath))
	f.nodes[clean(newpath)] = n

	return nil
}

// Remove implements sys.FS.
func (f *FS) Remove(name string) error {
	f.rec.Recordf("fs.remove", "%s", name)

	if err := f.check("Remove", name); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.nodes[clean(name)]; !ok {
		return pathErr("remove", name, fs.ErrNotExist)
	}

	delete(f.nodes, clean(name))

	return nil
}

// RemoveAll implements sys.FS.
func (f *FS) RemoveAll(name string) error {
	f.rec.Recordf("fs.remove-all", "%s", name)

	if err := f.check("RemoveAll", name); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	root := clean(name)

	for p := range f.nodes {
		if p == root || strings.HasPrefix(p, root+"/") {
			delete(f.nodes, p)
		}
	}

	return nil
}

// MkdirAll implements sys.FS.
func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	f.rec.Recordf("fs.mkdir", "%s mode=%04o", name, perm.Perm())

	if err := f.check("MkdirAll", name); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for p := clean(name); p != "/" && p != "."; p = path.Dir(p) {
		if _, ok := f.nodes[p]; !ok {
			f.nodes[p] = &node{mode: perm.Perm() | fs.ModeDir}
		}
	}

	return nil
}

// Chmod implements sys.FS.
func (f *FS) Chmod(name string, mode fs.FileMode) error {
	f.rec.Recordf("fs.chmod", "%s mode=%04o", name, mode.Perm())

	if err := f.check("Chmod", name); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.resolve(clean(name))
	if !ok {
		return pathErr("chmod", name, fs.ErrNotExist)
	}

	n.mode = n.mode&^fs.ModePerm | mode.Perm()

	return nil
}

// Chown implements sys.FS.
func (f *FS) Chown(name string, uid, gid int) error {
	f.rec.Recordf("fs.chown", "%s %d:%d", name, uid, gid)

	if err := f.check("Chown", name); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.resolve(clean(name))
	if !ok {
		return pathErr("chown", name, fs.ErrNotExist)
	}

	if uid >= 0 {
		n.uid = uid
	}

	if gid >= 0 {
		n.gid = gid
	}

	return nil
}

// Owner returns the uid/gid recorded by Chown.
func (f *FS) Owner(name string) (uid, gid int, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.resolve(clean(name))
	if !ok {
		return 0, 0, false
	}

	return n.uid, n.gid, true
}

// Symlink implements sys.FS.
func (f *FS) Symlink(oldname, newname string) error {
	f.rec.Recordf("fs.symlink", "%s -> %s", newname, oldname)

	if err := f.check("Symlink", newname); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.nodes[clean(newname)]; ok {
		return &os.LinkError{Op: "symlink", Old: oldname, New: newname, Err: fs.ErrExist}
	}

	f.nodes[clean(newname)] = &node{link: oldname, mode: fs.ModeSymlink | 0o777}

	return nil
}

// AvailBytes implements sys.FS.
func (f *FS) AvailBytes(name string) (uint64, error) {
	if err := f.check("AvailBytes", name); err != nil {
		return 0, err
	}

	return f.Avail, nil
}
