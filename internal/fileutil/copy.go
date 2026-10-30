// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package fileutil provides shared file utility functions.
package fileutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// copyBufPool reuses 32 KiB scratch buffers for io.CopyBuffer. The default
// io.Copy path on regular files allocates a 32 KiB buffer per call because the
// ReaderFrom/WriterTo fast paths fall back to a generic userspace copy when
// copy_file_range is unavailable (bind mounts, overlayfs, cross-fs).
var copyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)

		return &b
	},
}

// readerOnly / writerOnly hide the ReaderFrom / WriterTo interfaces so that
// io.CopyBuffer uses the explicit scratch buffer instead of dispatching into
// the stdlib fast paths (which allocate their own 32 KiB buffer on fallback).
type readerOnly struct{ io.Reader }
type writerOnly struct{ io.Writer }

// CopyFile copies a file from src to dst, preserving content but not metadata.
// The content is written to a temp file in dst's directory and renamed over
// dst, so this works even when the existing file is read-only (e.g. mode 0440)
// and is not subject to a remove/create race. The caller is responsible for
// setting the desired permissions on the new file after this function returns.
func CopyFile(ctx context.Context, src, dst string) error {
	//nolint:gosec // G304: File path comes from trusted configuration
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source: %w", err)
	}

	defer func() {
		if cerr := sourceFile.Close(); cerr != nil {
			logger.WarnContext(ctx, "Failed to close source file",
				"path", src,
				"error", cerr)
		}
	}()

	// Write to a fresh O_EXCL temp file in the destination directory and rename
	// it over dst. This works when dst is read-only (rename only needs write
	// permission on the directory) and never follows a symlink or a file
	// planted at dst between check and create: the rename replaces the
	// directory entry itself.
	destFile, tmpPath, err := createExclusiveTemp(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			discardTemp(ctx, destFile, tmpPath)
		}
	}()

	bufPtr := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufPtr)

	// Wrap src and dst to hide the ReaderFrom/WriterTo fast paths. On this
	// storage stack those paths fall back to a 32 KiB buffer allocation each
	// call (see profile). With both interfaces hidden, io.CopyBuffer uses our
	// pooled scratch buffer directly.
	if _, err := io.CopyBuffer(writerOnly{destFile}, readerOnly{sourceFile}, *bufPtr); err != nil {
		return fmt.Errorf("failed to copy content: %w", err)
	}

	if err := destFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync destination: %w", err)
	}

	if err := destFile.Close(); err != nil {
		return fmt.Errorf("failed to close destination: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("failed to rename temp file to destination: %w", err)
	}

	committed = true

	return nil
}

// discardTemp closes and removes an uncommitted temp file, logging failures.
func discardTemp(ctx context.Context, f *os.File, path string) {
	if cerr := f.Close(); cerr != nil && !errors.Is(cerr, os.ErrClosed) {
		logger.WarnContext(ctx, "Failed to close dest file",
			"path", path,
			"error", cerr)
	}

	if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
		logger.WarnContext(ctx, "Failed to remove temp file",
			"path", path,
			"error", rerr)
	}
}

// createExclusiveTemp creates a new file next to dst with O_EXCL (so it can
// never be a pre-existing file or symlink), mode 0666 before umask like
// os.Create.
func createExclusiveTemp(dst string) (*os.File, string, error) {
	dir := filepath.Dir(dst)

	for range 10 {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, "", fmt.Errorf("failed to generate temp name: %w", err)
		}

		tmpPath := filepath.Join(dir, ".configd-copy-"+hex.EncodeToString(suffix[:])+".tmp")

		//nolint:gosec // G304: File path comes from trusted configuration
		f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err == nil {
			return f, tmpPath, nil
		}

		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}

	return nil, "", fmt.Errorf("no unused temp name in %s: %w", dir, os.ErrExist)
}
