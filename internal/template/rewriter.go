// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package template

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/fileutil"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/lookup"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/transformer"
)

const warnCloseTempFile = "Failed to close temp file"

// Rewriter handles template file generation from .in files using REWRITE directives.
type Rewriter struct {
	BaseDir      string
	ConfigLookup lookup.ConfigLookup
	State        *state.State
	Transformer  *transformer.Transformer
}

// NewRewriter creates a new Rewriter instance.
func NewRewriter(baseDir string, cl lookup.ConfigLookup, st *state.State) *Rewriter {
	return &Rewriter{
		BaseDir:      baseDir,
		ConfigLookup: cl,
		State:        st,
		Transformer:  transformer.NewTransformer(cl, st),
	}
}

// scanAndWrite reads sourceFile line by line, transforms each line, and writes to tmpFile.
func (r *Rewriter) scanAndWrite(ctx context.Context, sourceFile, tmpFile *os.File) error {
	scanner := bufio.NewScanner(sourceFile)
	writer := bufio.NewWriter(tmpFile)

	for scanner.Scan() {
		transformed := r.Transformer.Transform(ctx, scanner.Text())
		if !strings.HasSuffix(transformed, "\n") {
			transformed += "\n"
		}

		if _, err := writer.WriteString(transformed); err != nil {
			return fmt.Errorf("failed to write to temporary file: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	return nil
}

// atomicReplace chmod-s tmpPath then renames it to targetPath.
// Falls back to copy+delete when rename crosses filesystems.
func atomicReplace(ctx context.Context, tmpPath, targetPath string, fileMode os.FileMode) error {
	if err := os.Chmod(tmpPath, fileMode); err != nil {
		return fmt.Errorf("failed to set file mode %o: %w", fileMode, err)
	}

	if err := os.Rename(tmpPath, targetPath); err == nil {
		return nil
	}

	logger.DebugContext(ctx, "Rename failed, falling back to copy")

	if err := fileutil.CopyFile(ctx, tmpPath, targetPath); err != nil {
		return fmt.Errorf("failed to copy temporary file to target: %w", err)
	}

	if err := os.Chmod(targetPath, fileMode); err != nil {
		return fmt.Errorf("failed to set file mode %o on copied file: %w", fileMode, err)
	}

	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		logger.WarnContext(ctx, "Failed to remove temp file", "path", tmpPath, "error", err)
	}

	return nil
}

// RewriteConfig generates a target file from a source template (.in file).
// It reads the source file line by line, applies transformations, and writes to the target.
// The operation is atomic: writes to a temporary file first, then renames.
//
// Parameters:
//   - source: Path to the source template file (relative to baseDir, e.g., "conf/nginx.conf.in")
//   - target: Path to the target output file (relative to baseDir, e.g., "conf/nginx.conf")
//   - mode: File permissions as octal string (e.g., "0600", "0440"). Default is "0440".
//
// Returns:
//   - error: nil on success, error if rewrite fails
func (r *Rewriter) RewriteConfig(ctx context.Context, source, target, mode string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "template")
	logger.DebugContext(ctx, "Rewriting template", "source", source, "target", target)

	if mode == "" {
		mode = "0440"
	}

	fileMode, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return fmt.Errorf("invalid file mode %s: %w", mode, err)
	}

	sourcePath := filepath.Join(r.BaseDir, source)
	targetPath := filepath.Join(r.BaseDir, target)

	logger.DebugContext(ctx, "Template rewrite paths",
		"source_path", sourcePath,
		"target_path", targetPath,
		"file_mode", fmt.Sprintf("%o", fileMode))

	//nolint:gosec // G304: File path comes from trusted configuration
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", sourcePath, err)
	}

	defer func() {
		if cerr := sourceFile.Close(); cerr != nil {
			logger.WarnContext(ctx, "Failed to close source file", "path", sourcePath, "error", cerr)
		}
	}()

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	tmpFile, err := os.CreateTemp(targetDir, ".configd-tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file in %s: %w", targetDir, err)
	}

	tmpPath := tmpFile.Name()

	defer func() {
		if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
			logger.WarnContext(ctx, "Failed to remove temp file", "path", tmpPath, "error", err)
		}
	}()

	scanErr := r.scanAndWrite(ctx, sourceFile, tmpFile)
	if cerr := tmpFile.Close(); cerr != nil {
		logger.WarnContext(ctx, warnCloseTempFile, "path", tmpPath, "error", cerr)
	}

	if scanErr != nil {
		return scanErr
	}

	if err := atomicReplace(ctx, tmpPath, targetPath, os.FileMode(fileMode)); err != nil {
		return err
	}

	logger.DebugContext(ctx, "Template rewrite completed",
		"target_path", targetPath,
		"file_mode", fmt.Sprintf("%o", fileMode))

	return nil
}

// RewriteReader generates output by reading from an io.Reader instead of a file.
// This is useful for testing or when the source content is in memory.
//
// Parameters:
//   - reader: Source content reader
//   - writer: Target output writer
//
// Returns:
//   - error: nil on success, error if transformation fails
func (r *Rewriter) RewriteReader(ctx context.Context, reader io.Reader, writer io.Writer) error {
	ctx = logger.ContextWithComponentOnce(ctx, "template")
	scanner := bufio.NewScanner(reader)
	bufWriter := bufio.NewWriter(writer)

	for scanner.Scan() {
		line := scanner.Text()
		transformed := r.Transformer.Transform(ctx, line)
		// Ensure newline is always added (Transform may or may not add it depending on content)
		if !strings.HasSuffix(transformed, "\n") {
			transformed += "\n"
		}

		if _, err := bufWriter.WriteString(transformed); err != nil {
			return fmt.Errorf("failed to write transformed line: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	if err := bufWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush output: %w", err)
	}

	return nil
}
