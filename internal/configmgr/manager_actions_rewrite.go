// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/fileutil"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// DoConfigRewrites executes configuration rewrites, postconf, postconfd, and LDAP changes.
func (cm *ConfigManager) DoConfigRewrites(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Executing config rewrites")

	var wg sync.WaitGroup

	errChan := make(chan error, 6) // Buffer matches goroutine count to prevent blocking

	// Proxygen takes longest, do it first
	wg.Go(func() {
		if cm.State.CurrentActions.Proxygen {
			logger.DebugContext(ctx, "Running proxygen")
			// Use the new method that passes loaded configs
			if err := cm.RunProxygenWithConfigs(ctx); err != nil {
				errChan <- fmt.Errorf("proxygen failed: %w", err)
			} else {
				logger.DebugContext(ctx, "Proxygen executed successfully")
				cm.State.Proxygen(false)
			}
		}
	})

	wg.Go(func() {
		cm.doRewrites(ctx)
	})

	wg.Go(func() {
		cm.doPostconf(ctx)
	})

	wg.Go(func() {
		cm.doPostconfd(ctx)
	})

	wg.Go(func() {
		cm.doLdap(ctx)
	})

	wg.Go(func() {
		cm.doMapfile(ctx)
	})

	wg.Wait()
	close(errChan)

	// Collect all errors from concurrent goroutines
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

func (cm *ConfigManager) doRewrites(ctx context.Context) {
	if len(cm.State.CurrentActions.Rewrites) == 0 {
		return
	}

	// Snapshot rewrites to avoid concurrent map read/write with DelRewrite
	// called from processRewrite goroutines.
	rewrites := make(map[string]config.RewriteEntry, len(cm.State.CurrentActions.Rewrites))
	maps.Copy(rewrites, cm.State.CurrentActions.Rewrites)

	startTime := time.Now()
	totalFiles := len(rewrites)
	logger.DebugContext(ctx, "Starting configuration file rewrites",
		"total_files", totalFiles)

	// Use a semaphore to limit concurrent file operations
	// This prevents overwhelming the disk I/O system
	maxConcurrent := 4 // Tuned for balance between parallelism and I/O contention
	semaphore := make(chan struct{}, maxConcurrent)

	var wg sync.WaitGroup

	fileCount := 0

	var mu sync.Mutex // Protect fileCount for logging

	for filePath, rewriteEntry := range rewrites {
		// Check for cancellation before starting new goroutine
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "File rewrites cancelled by shutdown signal")
			wg.Wait() // Wait for ongoing rewrites to complete

			return
		default:
		}

		wg.Add(1)

		semaphore <- struct{}{} // Acquire semaphore slot

		// Increment file count under mutex
		mu.Lock()

		fileCount++
		currentFileNum := fileCount

		mu.Unlock()

		// Process file in parallel goroutine
		go func(fp string, re config.RewriteEntry, fileNum int) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore slot

			var fileStartTime time.Time
			if logger.IsDebug(ctx) {
				fileStartTime = time.Now()

				logger.DebugContext(ctx, "Rewriting file",
					"file_number", fileNum,
					"total_files", totalFiles,
					"source", fp,
					"target", re.Value)
			}

			cm.processRewrite(ctx, fp, re)

			if logger.IsDebug(ctx) {
				elapsed := time.Since(fileStartTime)
				logger.DebugContext(ctx, "Completed file rewrite",
					"file_number", fileNum,
					"total_files", totalFiles,
					"target", re.Value,
					"duration_seconds", elapsed.Seconds())
			}
		}(filePath, rewriteEntry, currentFileNum)
	}

	// Wait for all rewrites to complete
	wg.Wait()

	totalElapsed := time.Since(startTime)
	logger.DebugContext(ctx, "All configuration file rewrites completed",
		"duration_seconds", totalElapsed.Seconds())
}

// cleanupRewriteFiles cleans up temporary and source files
func cleanupRewriteFiles(ctx context.Context, srcFile, tmpFile *os.File, tmpFileName string) {
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
		if _, err := os.Stat(tmpFileName); err == nil {
			if err := os.Remove(tmpFileName); err != nil {
				logger.WarnContext(ctx, "Failed to remove temporary file",
					"file", tmpFileName,
					"error", err)
			}
		}
	}
}

// isAlreadyClosedError checks if an error is due to an already closed file
func isAlreadyClosedError(err error) bool {
	return err != nil && (err.Error() == "file already closed" || strings.Contains(err.Error(), "already closed"))
}

// rewriteTransform opens srcPath, applies transformer to every line, and writes
// the result to tmpFile. It owns the srcFile lifecycle.
func (cm *ConfigManager) rewriteTransform(ctx context.Context, srcPath string, tmpFile *os.File) (int, error) {
	//nolint:gosec // G304: File path comes from trusted configuration
	srcFile, err := os.Open(srcPath)
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

		if _, err := tmpFile.WriteString(cm.Transformer.Transform(ctx, scanner.Text()) + "\n"); err != nil {
			return lineCount, fmt.Errorf("write to temp file: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return lineCount, fmt.Errorf("read source file %s: %w", srcPath, err)
	}

	return lineCount, nil
}

// rewriteAtomicCommit sets permissions on tmpFileName and atomically replaces
// destPath, falling back to copy+delete when rename crosses filesystems.
func rewriteAtomicCommit(ctx context.Context, tmpFileName, destPath, modeStr string) error {
	var fileMode os.FileMode = 0o644

	if modeStr != "" {
		modeInt, err := strconv.ParseInt(modeStr, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid file mode %s: %w", modeStr, err)
		}

		//nolint:gosec // G115: modeInt is validated by ParseInt with base 8 and 32-bit size
		fileMode = os.FileMode(modeInt)
	}

	if err := os.Chmod(tmpFileName, fileMode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err := os.Rename(tmpFileName, destPath); err == nil {
		return nil
	}

	// Cross-device fallback: copy then delete.
	if err := fileutil.CopyFile(ctx, tmpFileName, destPath); err != nil {
		return fmt.Errorf("copy temp to dest %s: %w", destPath, err)
	}

	if err := os.Chmod(destPath, fileMode); err != nil {
		return fmt.Errorf("chmod dest file: %w", err)
	}

	if err := os.Remove(tmpFileName); err != nil && !os.IsNotExist(err) {
		logger.WarnContext(ctx, "Failed to remove temp file", "temp_file", tmpFileName, "error", err)
	}

	return nil
}

// processRewrite processes a single file rewrite.
func (cm *ConfigManager) processRewrite(ctx context.Context, filePath string, rewriteEntry config.RewriteEntry) {
	srcPath := cm.mainConfig.BaseDir + "/" + filePath
	destPath := cm.mainConfig.BaseDir + "/" + rewriteEntry.Value

	tmpFile, err := os.CreateTemp("", "zmconfigd-rewrite-")
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create temporary file for rewrite", "error", err)

		return
	}

	tmpFileName := tmpFile.Name()
	defer cleanupRewriteFiles(ctx, nil, tmpFile, tmpFileName)

	lineCount, err := cm.rewriteTransform(ctx, srcPath, tmpFile)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to transform source file", "source_path", srcPath, "error", err)

		return
	}

	if err := tmpFile.Close(); err != nil {
		logger.ErrorContext(ctx, "Error closing temporary file", "error", err)
	}

	if err := rewriteAtomicCommit(ctx, tmpFileName, destPath, rewriteEntry.Mode); err != nil {
		logger.ErrorContext(ctx, "Failed to commit rewrite", "dest_path", destPath, "error", err)

		return
	}

	modeStr := rewriteEntry.Mode
	if modeStr == "" {
		modeStr = "0644"
	}

	logger.DebugContext(ctx, "File rewrite completed",
		"dest_path", destPath, "mode", modeStr, "lines_processed", lineCount)
	cm.State.DelRewrite(filePath)
}

// resolveValueSpec parses a value specification and resolves it to a concrete value.
// It handles LITERAL values directly and delegates other types to the MTA resolver.
func (cm *ConfigManager) resolveValueSpec(ctx context.Context, key, valueSpec string) (string, error) {
	valueType, valueKey := parseValueSpec(valueSpec)

	if valueType == configTypeLITERAL {
		return valueKey, nil
	}

	resolvedValue, err := cm.mtaResolver.ResolveValue(ctx, valueType, valueKey, cm.State)
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
