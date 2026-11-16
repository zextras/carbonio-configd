// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// The files below live in directories the zextras user can write (conf/,
// .ssh), and root gives them to zextras. A chown or chmod by path follows a
// link that was swapped in for the file, so it is done on the open file: the
// temp file is created exclusively, owned and moded through its descriptor
// before any data is in it, and renamed over the target, which replaces a link
// instead of following it.

// zextrasOwner is the uid and gid to give a file to when root creates it; -1
// (no change) when the process is zextras, whose files already belong to it.
func zextrasOwner(e *Env) (uid, gid int, err error) {
	if e.IsZextras {
		return -1, -1, nil
	}

	return zextrasIDs(e)
}

// writeOwned replaces name with data (mode perm, owner uid:gid, a negative id
// leaves the owner as the process makes it) without a window in which the
// file has another owner, mode or content.
func writeOwned(e *Env, name string, data []byte, perm fs.FileMode, uid, gid int) error {
	f, err := e.Sys.FS.CreateTemp(filepath.Dir(name), ".configd-*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}

	tmp := f.Name()

	err = fillOwned(f, data, perm, uid, gid)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = e.Sys.FS.Rename(tmp, name)
	}

	if err != nil {
		_ = e.Sys.FS.Remove(tmp)

		return fmt.Errorf("writing %s: %w", name, err)
	}

	return nil
}

func fillOwned(f sys.File, data []byte, perm fs.FileMode, uid, gid int) error {
	if err := f.Chmod(perm); err != nil {
		return err
	}

	if uid >= 0 || gid >= 0 {
		if err := f.Chown(uid, gid); err != nil {
			return err
		}
	}

	_, err := f.Write(data)

	return err
}

// ownDir gives dir its mode and owner through a descriptor opened without
// following a link: a directory swapped for a link is refused, not chmodded.
func ownDir(e *Env, dir string, mode fs.FileMode, uid, gid int) error {
	f, err := e.Sys.FS.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("opening %s: %w", dir, err)
	}

	defer func() { _ = f.Close() }()

	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}

	if uid >= 0 || gid >= 0 {
		if err := f.Chown(uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", dir, err)
		}
	}

	return nil
}
