// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systest_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestFS_SetupHelpersAndInspection(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/etc/a.conf", []byte("a"), 0o644)
	f.FS.PutDir("/etc", 0o755)
	f.FS.PutLink("/etc/cur", "a.conf")

	assert.Equal(t, []string{"/etc", "/etc/a.conf", "/etc/cur"}, f.FS.Paths())
	assert.Empty(t, f.Rec.Strings(), "setup helpers are not recorded")

	data, ok := f.FS.Data("/etc/cur")
	require.True(t, ok)
	assert.Equal(t, "a", string(data), "Data follows symlinks")

	_, ok = f.FS.Data("/etc")
	assert.False(t, ok, "a directory has no data")

	mode, ok := f.FS.Mode("/etc/cur")
	require.True(t, ok)
	assert.NotZero(t, mode&fs.ModeSymlink, "Mode does not follow the final link")

	_, ok = f.FS.Mode("/missing")
	assert.False(t, ok)
	assert.True(t, f.FS.Exists("/etc/cur"))
	assert.False(t, f.FS.Exists("/missing"))

	target, err := f.FS.Readlink("/etc/cur")
	require.NoError(t, err)
	assert.Equal(t, "a.conf", target)

	_, err = f.FS.Readlink("/etc/a.conf")
	require.ErrorIs(t, err, fs.ErrNotExist, "a regular file is not a link")
}

func TestFS_SymlinkLoopDoesNotResolve(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.PutLink("/a", "/b")
	f.FS.PutLink("/b", "/a")

	_, err := f.FS.ReadFile("/a")
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = f.FS.Stat("/a")
	require.ErrorIs(t, err, fs.ErrNotExist)

	st, err := f.FS.Lstat("/a")
	require.NoError(t, err)
	assert.Equal(t, "a", st.Name())
}

func TestFS_RecordReads(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/d/x", []byte("x"), 0o600)
	f.FS.PutDir("/d", 0o755)

	_, _ = f.FS.ReadFile("/d/x")
	_, _ = f.FS.Stat("/d/x")
	_, _ = f.FS.Lstat("/d/x")
	_, _ = f.FS.ReadDir("/d")
	assert.Empty(t, f.Rec.Of("fs.read"), "reads are silent by default")

	f.FS.RecordReads = true
	rd, err := f.FS.Open("/d/x")
	require.NoError(t, err)
	require.NoError(t, rd.Close())

	_, _ = f.FS.ReadFile("/d/x")
	_, _ = f.FS.Stat("/d/x")
	_, _ = f.FS.Lstat("/d/x")
	_, _ = f.FS.ReadDir("/d")

	assert.Equal(t, []string{"/d/x", "/d/x", "/d/x", "/d/x", "/d"}, f.Rec.Of("fs.read"))
}

func TestFS_FailHookInterceptsEveryOperation(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	f := systest.New()
	f.FS.Put("/f", []byte("x"), 0o600)
	f.FS.Fail = func(string, string) error { return boom }

	ctx := t.Context()
	fsys := f.FS

	_, err := fsys.ReadFile("/f")
	require.ErrorIs(t, err, boom)

	_, err = fsys.Open("/f")
	require.ErrorIs(t, err, boom)

	_, err = fsys.OpenFile("/f", os.O_RDWR, 0)
	require.ErrorIs(t, err, boom)

	_, err = fsys.CreateTemp("/tmp", "x*")
	require.ErrorIs(t, err, boom)

	_, err = fsys.Stat("/f")
	require.ErrorIs(t, err, boom)

	_, err = fsys.Lstat("/f")
	require.ErrorIs(t, err, boom)

	_, err = fsys.ReadDir("/")
	require.ErrorIs(t, err, boom)

	_, err = fsys.Glob("/*")
	require.ErrorIs(t, err, boom)

	_, err = fsys.Readlink("/f")
	require.ErrorIs(t, err, boom)

	_, err = fsys.AvailBytes("/")
	require.ErrorIs(t, err, boom)

	require.ErrorIs(t, fsys.WalkDir("/f", func(_ string, _ fs.DirEntry, err error) error { return err }), boom)
	require.ErrorIs(t, fsys.WriteFileAtomic("/g", nil, 0o600), boom)
	require.ErrorIs(t, fsys.CopyFile(ctx, "/f", "/g"), boom)
	require.ErrorIs(t, fsys.Rename("/f", "/g"), boom)
	require.ErrorIs(t, fsys.Remove("/f"), boom)
	require.ErrorIs(t, fsys.RemoveAll("/f"), boom)
	require.ErrorIs(t, fsys.MkdirAll("/m", 0o755), boom)
	require.ErrorIs(t, fsys.Chmod("/f", 0o600), boom)
	require.ErrorIs(t, fsys.Chown("/f", 1, 1), boom)
	require.ErrorIs(t, fsys.Symlink("/f", "/l"), boom)

	assert.Equal(t, []string{"/f"}, fsys.Paths(), "failed operations must not change anything")
}

func TestFS_FailHookIsSelectiveByOpAndPath(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Fail = func(op, p string) error {
		if op == "WriteFileAtomic" && p == "/ro" {
			return fs.ErrPermission
		}

		return nil
	}

	require.ErrorIs(t, f.FS.WriteFileAtomic("/ro", []byte("x"), 0o600), fs.ErrPermission)
	require.NoError(t, f.FS.WriteFileAtomic("/rw", []byte("x"), 0o600))
	assert.True(t, f.FS.Exists("/rw"))
	assert.False(t, f.FS.Exists("/ro"))
	assert.Equal(t, []string{"/ro mode=0600", "/rw mode=0600"}, f.Rec.Of("fs.write-atomic"),
		"the attempt is recorded even when it fails")
}

func TestFS_OpenFileFlagSemantics(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/f", []byte("old"), 0o600)
	f.FS.PutDir("/dir", 0o755)

	_, err := f.FS.OpenFile("/f", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	require.ErrorIs(t, err, fs.ErrExist)

	_, err = f.FS.OpenFile("/nope", os.O_RDONLY, 0)
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = f.FS.OpenFile("/dir", os.O_WRONLY, 0)
	require.Error(t, err, "a directory cannot be opened for writing")

	w, err := f.FS.OpenFile("/f", os.O_WRONLY|os.O_TRUNC, 0)
	require.NoError(t, err)
	_, err = w.Write([]byte("n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	data, _ := f.FS.Data("/f")
	assert.Equal(t, "n", string(data))

	c, err := f.FS.OpenFile("/new", os.O_CREATE|os.O_WRONLY, 0o640)
	require.NoError(t, err)
	require.NoError(t, c.Close())

	mode, _ := f.FS.Mode("/new")
	assert.Equal(t, fs.FileMode(0o640), mode)

	assert.Equal(t, []string{
		"/f flag=0xc1 perm=0600",
		"/dir flag=0x1 perm=0000",
		"/f flag=0x201 perm=0000",
		"/new flag=0x41 perm=0640",
	}, f.Rec.Of("fs.open"), "only mutating opens are recorded; the read-only open is not")
}

func TestFS_FileReadWriteSemantics(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/f", []byte("hello"), 0o600)

	ro, err := f.FS.Open("/f")
	require.NoError(t, err)

	assert.Equal(t, "/f", ro.Name())

	_, err = ro.Write([]byte("x"))
	require.Error(t, err, "write on a read-only handle fails")

	buf := make([]byte, 3)
	n, err := ro.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hel", string(buf[:n]))

	rest, err := io.ReadAll(ro)
	require.NoError(t, err)
	assert.Equal(t, "lo", string(rest))

	require.NoError(t, ro.Close())
	require.ErrorIs(t, ro.Close(), os.ErrClosed)

	_, err = ro.Read(buf)
	require.ErrorIs(t, err, os.ErrClosed)

	_, err = ro.Write(buf)
	require.ErrorIs(t, err, os.ErrClosed)

	ap, err := f.FS.OpenFile("/f", os.O_WRONLY|os.O_APPEND, 0)
	require.NoError(t, err)
	_, err = ap.WriteString("!!")
	require.NoError(t, err)
	require.NoError(t, ap.Close())

	data, _ := f.FS.Data("/f")
	assert.Equal(t, "hello!!", string(data))

	rw, err := f.FS.OpenFile("/f", os.O_RDWR, 0)
	require.NoError(t, err)
	_, err = rw.Write([]byte("HE"))
	require.NoError(t, err)
	require.NoError(t, rw.Close())

	data, _ = f.FS.Data("/f")
	assert.Equal(t, "HEllo!!", string(data), "overwrite in place from offset 0")
}

func TestFS_CreateTempIsUniqueAndExclusive(t *testing.T) {
	t.Parallel()

	f := systest.New()

	a, err := f.FS.CreateTemp("/tmp", "pre-*.suf")
	require.NoError(t, err)

	b, err := f.FS.CreateTemp("/tmp", "pre-*.suf")
	require.NoError(t, err)

	assert.Equal(t, "/tmp/pre-000001.suf", a.Name())
	assert.NotEqual(t, a.Name(), b.Name())

	plain, err := f.FS.CreateTemp("/tmp", "plain")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/plain000003", plain.Name())

	def, err := f.FS.CreateTemp("", "d")
	require.NoError(t, err)
	assert.Equal(t, os.TempDir(), pathDir(def.Name()))

	mode, _ := f.FS.Mode(a.Name())
	assert.Equal(t, fs.FileMode(0o600), mode)
	assert.Equal(t, []string{a.Name(), b.Name(), plain.Name(), def.Name()}, f.Rec.Of("fs.create-temp"))
}

func pathDir(p string) string {
	for i := len(p) - 1; i > 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}

	return "/"
}

func TestFS_StatAndLstatFollowLinksDifferently(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/real", []byte("12345"), 0o600)
	f.FS.PutLink("/lnk", "/real")

	st, err := f.FS.Stat("/lnk")
	require.NoError(t, err)
	assert.Equal(t, int64(5), st.Size())
	assert.False(t, st.IsDir())
	assert.Equal(t, systest.Epoch, st.ModTime())
	assert.Nil(t, st.Sys())
	assert.Equal(t, "lnk", st.Name())
	assert.Zero(t, st.Mode()&fs.ModeSymlink)

	lst, err := f.FS.Lstat("/lnk")
	require.NoError(t, err)
	assert.NotZero(t, lst.Mode()&fs.ModeSymlink)

	_, err = f.FS.Stat("/gone")
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = f.FS.Lstat("/gone")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFS_ReadDirListsDirectChildrenSorted(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.PutDir("/d", 0o755)
	f.FS.Put("/d/b", nil, 0o600)
	f.FS.Put("/d/a", nil, 0o600)
	f.FS.PutDir("/d/sub", 0o755)
	f.FS.Put("/d/sub/deep", nil, 0o600)
	f.FS.Put("/other", nil, 0o600)

	ents, err := f.FS.ReadDir("/d")
	require.NoError(t, err)

	var names []string

	for _, e := range ents {
		names = append(names, e.Name())
	}

	assert.Equal(t, []string{"a", "b", "sub"}, names)

	_, err = f.FS.ReadDir("/d/a")
	require.ErrorIs(t, err, fs.ErrNotExist, "a file is not a directory")

	_, err = f.FS.ReadDir("/missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFS_WalkDir(t *testing.T) {
	t.Parallel()

	newTree := func() *systest.FS {
		f := systest.New()
		f.FS.PutDir("/r", 0o755)
		f.FS.PutDir("/r/a", 0o755)
		f.FS.Put("/r/a/1", nil, 0o600)
		f.FS.Put("/r/a/2", nil, 0o600)
		f.FS.Put("/r/b", nil, 0o600)

		return f.FS
	}

	t.Run("lexical order", func(t *testing.T) {
		t.Parallel()

		var seen []string

		require.NoError(t, newTree().WalkDir("/r", func(p string, _ fs.DirEntry, err error) error {
			require.NoError(t, err)

			seen = append(seen, p)

			return nil
		}))
		assert.Equal(t, []string{"/r", "/r/a", "/r/a/1", "/r/a/2", "/r/b"}, seen)
	})

	t.Run("SkipDir skips that directory only", func(t *testing.T) {
		t.Parallel()

		var seen []string

		require.NoError(t, newTree().WalkDir("/r", func(p string, d fs.DirEntry, _ error) error {
			seen = append(seen, p)

			if d.IsDir() && p == "/r/a" {
				return fs.SkipDir
			}

			return nil
		}))
		assert.Equal(t, []string{"/r", "/r/a", "/r/b"}, seen)
	})

	t.Run("SkipDir on a file skips the remaining siblings", func(t *testing.T) {
		t.Parallel()

		var seen []string

		require.NoError(t, newTree().WalkDir("/r", func(p string, d fs.DirEntry, _ error) error {
			seen = append(seen, p)

			if !d.IsDir() && p == "/r/a/1" {
				return fs.SkipDir
			}

			return nil
		}))
		assert.Equal(t, []string{"/r", "/r/a", "/r/a/1", "/r/b"}, seen)
	})

	t.Run("SkipAll on root ends quietly", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, newTree().WalkDir("/r", func(string, fs.DirEntry, error) error { return fs.SkipAll }))
	})

	t.Run("callback error aborts the walk", func(t *testing.T) {
		t.Parallel()

		stop := errors.New("stop")
		calls := 0

		err := newTree().WalkDir("/r", func(p string, _ fs.DirEntry, _ error) error {
			calls++

			if p == "/r/a/1" {
				return stop
			}

			return nil
		})
		require.ErrorIs(t, err, stop)
		assert.Equal(t, 3, calls)
	})

	t.Run("missing root is passed to the callback", func(t *testing.T) {
		t.Parallel()

		var got error

		err := newTree().WalkDir("/nope", func(_ string, d fs.DirEntry, err error) error {
			got = err

			assert.Nil(t, d)

			return err
		})
		require.ErrorIs(t, err, fs.ErrNotExist)
		require.ErrorIs(t, got, fs.ErrNotExist)
	})

	t.Run("ReadDir failure is reported then walking continues", func(t *testing.T) {
		t.Parallel()

		tree := newTree()
		tree.Fail = func(op, _ string) error {
			if op == "ReadDir" {
				return fs.ErrPermission
			}

			return nil
		}

		var errs []error

		require.NoError(t, tree.WalkDir("/r", func(_ string, _ fs.DirEntry, err error) error {
			if err != nil {
				errs = append(errs, err)
			}

			return nil
		}))
		require.Len(t, errs, 1)
		assert.ErrorIs(t, errs[0], fs.ErrPermission)
	})

	t.Run("ReadDir failure aborts when the callback returns it", func(t *testing.T) {
		t.Parallel()

		tree := newTree()
		tree.Fail = func(op, _ string) error {
			if op == "ReadDir" {
				return fs.ErrPermission
			}

			return nil
		}

		err := tree.WalkDir("/r", func(_ string, _ fs.DirEntry, err error) error { return err })
		require.ErrorIs(t, err, fs.ErrPermission)
	})
}

func TestFS_Glob(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/etc/b.conf", nil, 0o600)
	f.FS.Put("/etc/a.conf", nil, 0o600)
	f.FS.Put("/etc/a.txt", nil, 0o600)
	f.FS.Put("/etc/sub/c.conf", nil, 0o600)

	got, err := f.FS.Glob("/etc/*.conf")
	require.NoError(t, err)
	assert.Equal(t, []string{"/etc/a.conf", "/etc/b.conf"}, got)

	got, err = f.FS.Glob("/nothing/*")
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = f.FS.Glob("/etc/[")
	require.Error(t, err, "malformed pattern")
}

func TestFS_CopyFile(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/src", []byte("data"), 0o640)
	f.FS.PutLink("/lnk", "/src")

	require.NoError(t, f.FS.CopyFile(t.Context(), "/lnk", "/dst"))

	data, _ := f.FS.Data("/dst")
	assert.Equal(t, "data", string(data))

	mode, _ := f.FS.Mode("/dst")
	assert.Equal(t, fs.FileMode(0o640), mode, "copies the permission bits and not the link")

	data[0] = 'X'
	again, _ := f.FS.Data("/dst")
	assert.Equal(t, "data", string(again), "Data returns a copy")

	err := f.FS.CopyFile(t.Context(), "/nope", "/dst2")
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.False(t, f.FS.Exists("/dst2"))
	assert.Equal(t, []string{"/lnk -> /dst", "/nope -> /dst2"}, f.Rec.Of("fs.copy"))
}

func TestFS_RenameMovesNodeAndReportsMissing(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/a", []byte("x"), 0o600)
	f.FS.Put("/b", []byte("old"), 0o600)

	require.NoError(t, f.FS.Rename("/a", "/b"))
	assert.False(t, f.FS.Exists("/a"))

	data, _ := f.FS.Data("/b")
	assert.Equal(t, "x", string(data), "rename replaces the destination")

	err := f.FS.Rename("/a", "/c")

	var le *os.LinkError

	require.ErrorAs(t, err, &le)
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Equal(t, "rename", le.Op)
}

func TestFS_RemoveAndRemoveAll(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.PutDir("/d", 0o755)
	f.FS.Put("/d/x", nil, 0o600)
	f.FS.Put("/d/sub/y", nil, 0o600)
	f.FS.Put("/dd", nil, 0o600)

	require.ErrorIs(t, f.FS.Remove("/gone"), fs.ErrNotExist)
	require.NoError(t, f.FS.Remove("/d/x"))
	require.NoError(t, f.FS.RemoveAll("/d"))
	assert.Equal(t, []string{"/dd"}, f.FS.Paths(), "RemoveAll does not match a sibling sharing the prefix")

	require.NoError(t, f.FS.RemoveAll("/never-existed"), "RemoveAll of a missing path is not an error")
}

func TestFS_MkdirAllCreatesMissingAncestorsOnly(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.PutDir("/a", 0o700)

	require.NoError(t, f.FS.MkdirAll("/a/b/c", 0o755))

	assert.Equal(t, []string{"/a", "/a/b", "/a/b/c"}, f.FS.Paths())

	mode, _ := f.FS.Mode("/a")
	assert.Equal(t, fs.ModeDir|0o700, mode, "existing ancestors keep their mode")

	mode, _ = f.FS.Mode("/a/b/c")
	assert.Equal(t, fs.ModeDir|0o755, mode)

	require.NoError(t, f.FS.MkdirAll("rel/x", 0o755))
	assert.True(t, f.FS.Exists("rel/x"))
	assert.True(t, f.FS.Exists("rel"))
}

func TestFS_ChmodKeepsTypeBits(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.PutDir("/d", 0o755)
	f.FS.Put("/f", nil, 0o600)
	f.FS.PutLink("/l", "/f")

	require.NoError(t, f.FS.Chmod("/d", 0o700))

	mode, _ := f.FS.Mode("/d")
	assert.Equal(t, fs.ModeDir|0o700, mode)

	require.NoError(t, f.FS.Chmod("/l", 0o444), "chmod follows the link")

	mode, _ = f.FS.Mode("/f")
	assert.Equal(t, fs.FileMode(0o444), mode)

	require.ErrorIs(t, f.FS.Chmod("/missing", 0o600), fs.ErrNotExist)
}

func TestFS_ChownNegativeIdsLeaveFieldUnchanged(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/f", nil, 0o600)

	require.NoError(t, f.FS.Chown("/f", 10, 20))
	require.NoError(t, f.FS.Chown("/f", -1, 30))

	uid, gid, ok := f.FS.Owner("/f")
	require.True(t, ok)
	assert.Equal(t, 10, uid)
	assert.Equal(t, 30, gid)

	require.NoError(t, f.FS.Chown("/f", 11, -1))

	uid, gid, _ = f.FS.Owner("/f")
	assert.Equal(t, 11, uid)
	assert.Equal(t, 30, gid)

	require.ErrorIs(t, f.FS.Chown("/missing", 1, 1), fs.ErrNotExist)

	_, _, ok = f.FS.Owner("/missing")
	assert.False(t, ok)
}

func TestFS_SymlinkRefusesExistingAndResolvesRelative(t *testing.T) {
	t.Parallel()

	f := systest.New()
	f.FS.Put("/etc/real", []byte("r"), 0o600)

	require.NoError(t, f.FS.Symlink("real", "/etc/rel"))

	data, err := f.FS.ReadFile("/etc/rel")
	require.NoError(t, err)
	assert.Equal(t, "r", string(data), "relative targets resolve against the link directory")

	err = f.FS.Symlink("real", "/etc/rel")

	var le *os.LinkError

	require.ErrorAs(t, err, &le)
	require.ErrorIs(t, err, fs.ErrExist)
}

func TestFS_AvailBytes(t *testing.T) {
	t.Parallel()

	f := systest.New()

	n, err := f.FS.AvailBytes("/")
	require.NoError(t, err)
	assert.Equal(t, uint64(1<<40), n)

	f.FS.Avail = 42
	n, _ = f.FS.AvailBytes("/")
	assert.Equal(t, uint64(42), n)
}
