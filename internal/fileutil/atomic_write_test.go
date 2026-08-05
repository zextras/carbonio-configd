// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package fileutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/fileutil"
)

func TestAtomicWrite(t *testing.T) {
	t.Run("writes new file with requested permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "out.conf")

		if err := fileutil.AtomicWrite(path, []byte("hello"), 0o640); err != nil {
			t.Fatalf("AtomicWrite failed: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read written file: %v", err)
		}

		if string(data) != "hello" {
			t.Errorf("content mismatch: got %q, want %q", string(data), "hello")
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("failed to stat written file: %v", err)
		}

		if info.Mode().Perm() != 0o640 {
			t.Errorf("expected mode 0640, got %o", info.Mode().Perm())
		}
	})

	t.Run("leaves no temp file behind in the target directory", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "out.conf")

		if err := fileutil.AtomicWrite(path, []byte("data"), 0o644); err != nil {
			t.Fatalf("AtomicWrite failed: %v", err)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("failed to read dir: %v", err)
		}

		if len(entries) != 1 || entries[0].Name() != "out.conf" {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}

			t.Errorf("expected only out.conf in %s, got %v", dir, names)
		}
	})

	t.Run("replaces an existing file's content and permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "out.conf")

		if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
			t.Fatalf("failed to seed existing file: %v", err)
		}

		if err := fileutil.AtomicWrite(path, []byte("new"), 0o440); err != nil {
			t.Fatalf("AtomicWrite failed: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read replaced file: %v", err)
		}

		if string(data) != "new" {
			t.Errorf("content mismatch: got %q, want %q", string(data), "new")
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("failed to stat replaced file: %v", err)
		}

		if info.Mode().Perm() != 0o440 {
			t.Errorf("expected mode 0440, got %o", info.Mode().Perm())
		}
	})

	t.Run("create temp file failure returns error and leaves no litter", func(t *testing.T) {
		err := fileutil.AtomicWrite(filepath.Join(t.TempDir(), "missing-dir", "out.conf"), []byte("x"), 0o644)
		if err == nil {
			t.Error("expected error when parent directory does not exist")
		}
	})

	t.Run("rename failure removes the temp file", func(t *testing.T) {
		dir := t.TempDir()
		// A directory at the target path makes os.Rename fail with EISDIR/ENOTEMPTY.
		target := filepath.Join(dir, "out.conf")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatalf("failed to seed directory at target path: %v", err)
		}

		if err := fileutil.AtomicWrite(target, []byte("x"), 0o644); err == nil {
			t.Error("expected error when target path is a directory")
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("failed to read dir: %v", err)
		}

		for _, e := range entries {
			if e.Name() != "out.conf" {
				t.Errorf("expected temp file to be removed, found %q", e.Name())
			}
		}
	})
}
