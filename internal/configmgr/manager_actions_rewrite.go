// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// runStep launches step in a goroutine and forwards any error to errChan.
func (cm *ConfigManager) runStep(
	ctx context.Context,
	wg *sync.WaitGroup,
	errChan chan<- error,
	step func(context.Context) error,
) {
	wg.Go(func() {
		if err := step(ctx); err != nil {
			errChan <- err
		}
	})
}

// joinRewriteErrors drains errChan, logs each error, and joins them into one.
func (cm *ConfigManager) joinRewriteErrors(ctx context.Context, errChan <-chan error) error {
	var errs []error

	for err := range errChan {
		logger.ErrorContext(ctx, "Error during config rewrite",
			"error", err)
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	logger.DebugContext(ctx, "Config rewrites complete")

	return nil
}

// rewriteWorker performs a single file rewrite while reporting timing under
// debug. It exists to keep doRewrites' cognitive complexity below the lint
// threshold by extracting the per-file goroutine body.
func (cm *ConfigManager) rewriteWorker(
	ctx context.Context,
	filePath string,
	entry config.RewriteEntry,
	fileNum, totalFiles int,
) error {
	var fileStartTime time.Time
	if logger.IsDebug(ctx) {
		fileStartTime = time.Now()

		logger.DebugContext(ctx, "Rewriting file",
			"file_number", fileNum,
			"total_files", totalFiles,
			"source", filePath,
			"target", entry.Value)
	}

	err := cm.processRewrite(ctx, filePath, entry)

	if logger.IsDebug(ctx) {
		logger.DebugContext(ctx, "Completed file rewrite",
			"file_number", fileNum,
			"total_files", totalFiles,
			"target", entry.Value,
			"duration_seconds", time.Since(fileStartTime).Seconds())
	}

	return err
}

// doRewrites renders the template files of the plan with a bounded worker pool
// and returns the ones that did not complete (failed, or not started because
// ctx was cancelled), which the next cycle retries.
func (cm *ConfigManager) doRewrites(ctx context.Context, entries []plan.Rewrite) ([]plan.Rewrite, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	startTime := time.Now()
	totalFiles := len(entries)
	logger.DebugContext(ctx, "Starting configuration file rewrites",
		"total_files", totalFiles)

	const maxConcurrent = 4 // Tuned for balance between parallelism and I/O contention

	semaphore := make(chan struct{}, maxConcurrent)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		done = make(map[string]bool, totalFiles)
	)

	record := func(source string, err error) {
		mu.Lock()
		defer mu.Unlock()

		if err != nil {
			errs = append(errs, err)

			return
		}

		done[source] = true
	}

	for i, entry := range entries {
		if cancelled(ctx) {
			logger.InfoContext(ctx, "File rewrites cancelled by shutdown signal")

			break
		}

		wg.Add(1)

		semaphore <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()

			re := config.RewriteEntry{Value: entry.Target, Mode: entry.Mode}
			record(entry.Source, cm.rewriteWorker(ctx, entry.Source, re, i+1, totalFiles))
		}()
	}

	wg.Wait()

	logger.DebugContext(ctx, "All configuration file rewrites completed",
		"duration_seconds", time.Since(startTime).Seconds())

	var pending []plan.Rewrite

	for _, entry := range entries {
		if !done[entry.Source] {
			pending = append(pending, entry)
		}
	}

	return pending, errors.Join(errs...)
}

// cleanupRewriteFiles cleans up temporary and source files
func (cm *ConfigManager) cleanupRewriteFiles(ctx context.Context, srcFile, tmpFile sys.File, tmpFileName string) {
	if srcFile != nil {
		if err := srcFile.Close(); err != nil && !isAlreadyClosedError(err) {
			logger.ErrorContext(ctx, "Error closing source file",
				"error", err)
		}
	}

	if tmpFile != nil {
		if err := tmpFile.Close(); err != nil && !isAlreadyClosedError(err) {
			logger.ErrorContext(ctx, "Error closing temporary file",
				"error", err)
		}
	}

	if tmpFileName != "" {
		if _, err := cm.env.FS.Stat(tmpFileName); err == nil {
			if err := cm.env.FS.Remove(tmpFileName); err != nil {
				logger.WarnContext(ctx, "Failed to remove temporary file",
					"file", tmpFileName,
					"error", err)
			}
		}
	}
}

// isAlreadyClosedError checks if an error is due to an already closed file
func isAlreadyClosedError(err error) bool {
	return errors.Is(err, os.ErrClosed)
}

// rewriteTransform opens srcPath, applies transformer to every line, and writes
// the result to out. It owns the srcFile lifecycle.
func (cm *ConfigManager) rewriteTransform(ctx context.Context, srcPath string, out io.StringWriter) (int, error) {
	srcFile, err := cm.env.FS.Open(srcPath)
	if err != nil {
		return 0, fmt.Errorf("open source file %s: %w", srcPath, err)
	}

	defer func() {
		if cerr := srcFile.Close(); cerr != nil && !isAlreadyClosedError(cerr) {
			logger.ErrorContext(ctx, "Error closing source file", "error", cerr)
		}
	}()

	lineCount := 0
	scanner := bufio.NewScanner(srcFile)

	for scanner.Scan() {
		lineCount++

		transformed, err := cm.Transformer.Transform(ctx, scanner.Text())
		if err != nil {
			return lineCount, fmt.Errorf("transform %s line %d: %w", srcPath, lineCount, err)
		}

		// Transform ends a line holding a marker with "\n" already; a line it
		// returned untouched has lost its newline to the scanner.
		if !strings.HasSuffix(transformed, "\n") {
			transformed += "\n"
		}

		if _, err := out.WriteString(transformed); err != nil {
			return lineCount, fmt.Errorf("write to temp file: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return lineCount, fmt.Errorf("read source file %s: %w", srcPath, err)
	}

	return lineCount, nil
}

// rewriteFileMode parses the octal mode of a REWRITE directive; legacy
// state.py:775 defaults to 0440.
func rewriteFileMode(modeStr string) (os.FileMode, error) {
	if modeStr == "" {
		return 0o440, nil
	}

	modeInt, err := strconv.ParseInt(modeStr, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid file mode %s: %w", modeStr, err)
	}

	//nolint:gosec // G115: modeInt is validated by ParseInt with base 8 and 32-bit size
	return os.FileMode(modeInt), nil
}

// rewriteAtomicCommit sets permissions on tmpFileName and atomically replaces
// destPath, falling back to copy+delete when rename crosses filesystems.
func (cm *ConfigManager) rewriteAtomicCommit(ctx context.Context, tmpFileName, destPath, modeStr string) error {
	fileMode, err := rewriteFileMode(modeStr)
	if err != nil {
		return err
	}

	if err := cm.env.FS.Chmod(tmpFileName, fileMode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err := cm.env.FS.Rename(tmpFileName, destPath); err == nil {
		return nil
	}

	// Cross-device fallback: copy then delete.
	if err := cm.env.FS.CopyFile(ctx, tmpFileName, destPath); err != nil {
		return fmt.Errorf("copy temp to dest %s: %w", destPath, err)
	}

	if err := cm.env.FS.Chmod(destPath, fileMode); err != nil {
		return fmt.Errorf("chmod dest file: %w", err)
	}

	if err := cm.env.FS.Remove(tmpFileName); err != nil && !os.IsNotExist(err) {
		logger.WarnContext(ctx, "Failed to remove temp file", "temp_file", tmpFileName, "error", err)
	}

	return nil
}

// processRewrite processes a single file rewrite.
func (cm *ConfigManager) processRewrite(ctx context.Context, filePath string, rewriteEntry config.RewriteEntry) error {
	srcPath := cm.mainConfig.BaseDir + "/" + filePath
	destPath := cm.mainConfig.BaseDir + "/" + rewriteEntry.Value

	tmpFile, err := cm.env.FS.CreateTemp("", "zmconfigd-rewrite-")
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create temporary file for rewrite", "error", err)

		return fmt.Errorf("create temp file: %w", err)
	}

	tmpFileName := tmpFile.Name()
	defer cm.cleanupRewriteFiles(ctx, nil, tmpFile, tmpFileName)

	lineCount, err := cm.rewriteTransform(ctx, srcPath, tmpFile)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to transform source file", "source_path", srcPath, "error", err)

		return fmt.Errorf("transform source file %s: %w", srcPath, err)
	}

	if err := tmpFile.Close(); err != nil {
		logger.ErrorContext(ctx, "Error closing temporary file", "error", err)
	}

	if err := cm.rewriteAtomicCommit(ctx, tmpFileName, destPath, rewriteEntry.Mode); err != nil {
		logger.ErrorContext(ctx, "Failed to commit rewrite", "dest_path", destPath, "error", err)

		return fmt.Errorf("commit rewrite to %s: %w", destPath, err)
	}

	modeStr := rewriteEntry.Mode
	if modeStr == "" {
		modeStr = "0440"
	}

	logger.DebugContext(ctx, "File rewrite completed",
		"dest_path", destPath, "mode", modeStr, "lines_processed", lineCount)

	return nil
}

// resolveValueSpec parses a value specification and resolves it to a concrete value.
// It handles LITERAL values directly and delegates other types to the MTA resolver.
func (cm *ConfigManager) resolveValueSpec(ctx context.Context, key, valueSpec string) (string, error) {
	valueType, valueKey := parseValueSpec(valueSpec)

	switch valueType {
	case configTypeLITERAL:
		return valueKey, nil
	case configTypeFILE:
		// FILE values are config fragments containing %%...%% directives; they
		// must be expanded by the transformer before reaching postconf, exactly
		// as the legacy Jython zmconfigd did in jylibs/state.py getFileConfig().
		return cm.lookupFileKey(ctx, valueKey)
	}

	resolvedValue, err := cm.mtaResolver.ResolveValue(ctx, valueType, valueKey, cm.State.Snapshot())
	if err != nil {
		return "", fmt.Errorf("failed to resolve value for key %s: %w", key, err)
	}

	return resolvedValue, nil
}

// parseValueSpec parses a valueSpec string and returns the value type and key.
// Parser stores formats like: "VAR:key", "LOCAL:key", "MAPLOCAL:key", "FILE /path", or literal values.
func parseValueSpec(valueSpec string) (valueType, valueKey string) {
	switch {
	case strings.Contains(valueSpec, ":"):
		// Check for colon-separated type:key format (VAR:, LOCAL:, MAPLOCAL:)
		before, after, _ := strings.Cut(valueSpec, ":")

		prefix := before
		switch prefix {
		case configTypeVAR, configTypeLOCAL, configTypeMAPLOCAL:
			valueType = prefix
			valueKey = after
		default:
			// Not a recognized type prefix, treat as literal
			valueType = configTypeLITERAL
			valueKey = valueSpec
		}
	case strings.HasPrefix(valueSpec, configTypeFILE+" "):
		// FILE is space-separated: "FILE /path/to/file"
		valueType = configTypeFILE
		valueKey = strings.TrimPrefix(valueSpec, configTypeFILE+" ")
	case valueSpec == "":
		// Empty value
		valueType = configTypeLITERAL
		valueKey = ""
	default:
		// Literal value
		valueType = configTypeLITERAL
		valueKey = valueSpec
	}

	return valueType, valueKey
}
