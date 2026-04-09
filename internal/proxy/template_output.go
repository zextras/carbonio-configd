// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// WriteOutput writes processed template to output file
func (tp *TemplateProcessor) WriteOutput(ctx context.Context, name string, content string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	// Determine output file path (remove .template extension)
	outputName := strings.TrimSuffix(name, ".template")
	outputPath := filepath.Join(tp.outputDir, outputName)

	// Check if generator is available for mode checking
	dryRun := false
	verbose := false

	if tp.generator != nil {
		dryRun = tp.generator.DryRun
		verbose = tp.generator.Verbose
	}

	// In dry-run mode, just log what would be written
	if dryRun {
		logger.DebugContext(ctx, "[DRY-RUN] Would write file",
			"path", outputPath,
			"byte_count", len(content))

		if verbose {
			logger.DebugContext(ctx, "[DRY-RUN] Content preview",
				"content", truncateString(content, 500))
		}

		return nil
	}

	// Ensure output directory exists
	if err := os.MkdirAll(tp.outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write atomically using temp file + rename.
	// Use os.CreateTemp in the output directory to avoid predictable temp paths
	// and ensure the temp file is on the same filesystem for atomic rename.
	tmpFile, err := os.CreateTemp(filepath.Dir(outputPath), ".configd-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tmpPath := tmpFile.Name()

	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		if rerr := os.Remove(tmpPath); rerr != nil {
			logger.WarnContext(ctx, "Failed to remove temp file",
				"path", tmpPath,
				"error", rerr)
		}

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	if verbose {
		logger.DebugContext(ctx, "Wrote file",
			"path", outputPath,
			"byte_count", len(content))
	}

	return nil
}

// atomicCopyFile copies src to dst atomically via a temp file + rename.
func atomicCopyFile(src, dst string) error {
	//nolint:gosec // G304: File path comes from trusted configuration
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}

	defer func() { _ = source.Close() }()

	tmpFile, err := os.CreateTemp(filepath.Dir(dst), ".configd-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, source); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to copy to temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)

		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// Backup creates a backup of an existing configuration file
func (tp *TemplateProcessor) Backup(ctx context.Context, configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// File doesn't exist, no backup needed
		return nil
	}

	if err := atomicCopyFile(configPath, configPath+".backup"); err != nil {
		return fmt.Errorf("failed to backup %s: %w", configPath, err)
	}

	return nil
}

// Rollback restores a configuration from backup
func (tp *TemplateProcessor) Rollback(configPath string) error {
	backupPath := configPath + ".backup"

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("no backup file found: %s", backupPath)
	}

	if err := os.Rename(backupPath, configPath); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	return nil
}

// ValidateNginxConfig runs nginx -t to validate the configuration
// Returns nil if validation succeeds, error with nginx output if it fails
func (tp *TemplateProcessor) ValidateNginxConfig(ctx context.Context, configPath string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	// Look for nginx binary in common locations
	nginxPaths := []string{
		"/opt/zextras/common/sbin/nginx",
		"/usr/bin/nginx",
		"/usr/sbin/nginx",
		"nginx", // Try PATH
	}

	var nginxBinary string

	for _, path := range nginxPaths {
		if _, err := exec.LookPath(path); err == nil {
			nginxBinary = path
			break
		}
	}

	if nginxBinary == "" {
		logger.WarnContext(ctx, "Nginx binary not found, skipping validation")

		return nil // Don't fail if nginx isn't available
	}

	// Run nginx -t with the specific config file
	cmd := exec.CommandContext(ctx, nginxBinary, "-t", "-c", configPath)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	// Check if syntax is OK (nginx prints this to stderr even on success)
	if strings.Contains(outputStr, "syntax is ok") {
		// Syntax validation passed - ignore PID file errors or other runtime issues
		if tp.generator != nil && tp.generator.Verbose {
			logger.DebugContext(ctx, "Nginx -t validation passed",
				"config_path", configPath)
		}

		return nil
	}

	// If there's an error and syntax is NOT ok, it's a real validation failure
	if err != nil {
		return fmt.Errorf("nginx validation failed: %w\nOutput: %s", err, outputStr)
	}

	return nil
}
