// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package fileutil provides shared file utility functions.
package fileutil

import (
	"context"
	"fmt"
	"io"
	"os"
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
// The destination file is removed before creating, so this works even when the
// existing file is read-only (e.g. mode 0440). The caller is responsible for
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

	// Remove the destination file first. This is necessary because the existing
	// file may be read-only (e.g. mode 0440), which would cause os.Create to
	// fail with "permission denied". Removing and recreating only requires write
	// permission on the parent directory, which the zextras user has.
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing destination: %w", err)
	}

	//nolint:gosec // G304: File path comes from trusted configuration
	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination: %w", err)
	}

	defer func() {
		if cerr := destFile.Close(); cerr != nil {
			logger.WarnContext(ctx, "Failed to close dest file",
				"path", dst,
				"error", cerr)
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

	return nil
}
