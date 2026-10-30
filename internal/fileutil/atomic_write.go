// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWrite writes data to path without ever exposing a partially written
// file to readers: it creates a temp file in filepath.Dir(path), writes data,
// fsyncs, chmods to perm, closes, renames the temp file onto path, then fsyncs
// the parent directory so the rename is durable. The
// temp file is removed on any failure path.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".configd-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return syncDir(filepath.Dir(path))
}

// syncDir fsyncs a directory so a preceding rename survives a crash.
func syncDir(dir string) error {
	//nolint:gosec // G304: directory derived from the caller's target path
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open parent directory: %w", err)
	}

	if err := d.Sync(); err != nil {
		_ = d.Close()

		return fmt.Errorf("failed to sync parent directory: %w", err)
	}

	if err := d.Close(); err != nil {
		return fmt.Errorf("failed to close parent directory: %w", err)
	}

	return nil
}
