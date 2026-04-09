// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// executor implements the Executor interface.
type executor struct {
	baseDir      string
	postconfPath string
	ldapManager  ldap.Manager
	mappedFiles  map[string]string // key -> file path mapping
}

// NewExecutor creates a new MTA operations executor.
func NewExecutor(baseDir string, ldapManager ldap.Manager) Executor {
	return &executor{
		baseDir:      baseDir,
		postconfPath: filepath.Join(baseDir, "common", "sbin", "postconf"),
		ldapManager:  ldapManager,
		mappedFiles: map[string]string{
			"zimbraSSLDHParam": filepath.Join(baseDir, "conf", "dhparam.pem"),
		},
	}
}

// ExecutePostconf executes a postconf -e operation.
func (e *executor) ExecutePostconf(ctx context.Context, op PostconfOperation) error {
	ctx = logger.ContextWithComponent(ctx, "mtaops")
	return e.ExecutePostconfBatch(ctx, []PostconfOperation{op})
}

// ExecutePostconfBatch executes multiple postconf -e operations in a single call.
// This is much more efficient than calling postconf individually for each setting.
// Values with newlines are executed separately as postconf doesn't support multi-line
// values in batch mode.
func (e *executor) ExecutePostconfBatch(ctx context.Context, ops []PostconfOperation) error {
	ctx = logger.ContextWithComponent(ctx, "mtaops")

	if len(ops) == 0 {
		return nil
	}

	span := tracing.StartSpan("MTA.PostconfBatch")
	if span != nil {
		span.AddMetadata("count", fmt.Sprintf("%d", len(ops)))
	}
	defer tracing.EndSpan(span)

	logger.DebugContext(ctx, "Executing postconf batch",
		"operation_count", len(ops))

	// Normalize all operations: convert newlines to comma-separated format
	// LDAP multi-value attributes may come as newline-separated
	for i := range ops {
		if !strings.Contains(ops[i].Value, "\n") {
			continue
		}

		// Convert newlines to comma-separated format
		lines := strings.Split(ops[i].Value, "\n")

		var cleanLines []string

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				cleanLines = append(cleanLines, line)
			}
		}

		ops[i].Value = strings.Join(cleanLines, ", ")
		logger.DebugContext(ctx, "Normalized multi-line value",
			"key", ops[i].Key,
			"value", ops[i].Value)
	}

	// Build arguments: postconf -e "key1=value1" "key2=value2" ...
	args := make([]string, 0, len(ops)+1)
	args = append(args, "-e")

	for _, op := range ops {
		arg := fmt.Sprintf("%s=%s", op.Key, op.Value)
		args = append(args, arg)

		logger.DebugContext(ctx, "Queued postconf operation",
			"key", op.Key,
			"value", op.Value)
	}

	//nolint:gosec,noctx // G204: Command from trusted data; noctx: Postconf is fast and doesn't need cancellation
	cmd := exec.Command(e.postconfPath, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.ErrorContext(ctx, "Postconf batch failed",
			"error", err,
			"output", string(output))

		return fmt.Errorf("postconf batch failed: %w, output: %s", err, string(output))
	}

	logger.DebugContext(ctx, "Postconf batch completed",
		"operations_set", len(ops))

	return nil
}

// ExecutePostconfd executes a postconf -X operation (delete parameter).
func (e *executor) ExecutePostconfd(ctx context.Context, op PostconfdOperation) error {
	ctx = logger.ContextWithComponent(ctx, "mtaops")
	return e.ExecutePostconfdBatch(ctx, []PostconfdOperation{op})
}

// ExecutePostconfdBatch executes multiple postconf -X operations in a single call.
func (e *executor) ExecutePostconfdBatch(ctx context.Context, ops []PostconfdOperation) error {
	ctx = logger.ContextWithComponent(ctx, "mtaops")

	if len(ops) == 0 {
		return nil
	}

	span := tracing.StartSpan("MTA.PostconfdBatch")
	if span != nil {
		span.AddMetadata("count", fmt.Sprintf("%d", len(ops)))
	}
	defer tracing.EndSpan(span)

	logger.DebugContext(ctx, "Executing postconf -X batch",
		"operation_count", len(ops))

	// Build arguments: postconf -X key1 key2 key3 ...
	args := make([]string, 0, len(ops)+1)
	args = append(args, "-X")

	for _, op := range ops {
		args = append(args, op.Key)
		logger.DebugContext(ctx, "Queued postconf -X",
			"key", op.Key)
	}

	//nolint:gosec,noctx // G204: Command from trusted data; noctx: Postconf is fast and doesn't need cancellation
	cmd := exec.Command(e.postconfPath, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// postconf -X might fail if keys don't exist, which is acceptable
		logger.DebugContext(ctx, "Postconf -X batch completed with error (may be expected if keys don't exist)",
			"error", err)

		return nil // Don't treat as error
	}

	logger.DebugContext(ctx, "Postconf -X batch completed",
		"operations_deleted", len(ops))

	if len(output) > 0 {
		logger.DebugContext(ctx, "Postconf -X output",
			"output", string(output))
	}

	return nil
}

// handleEmptyMapfileData handles the case when MAPFILE has no data in LDAP.
// It tries to restore from .crb backup or leaves existing file untouched.
func (e *executor) handleEmptyMapfileData(ctx context.Context, op MapfileOperation, filePath string) error {
	// Try to restore from .crb backup instead of deleting
	backupPath := filePath + ".crb"

	//nolint:gosec // G304: File path comes from trusted MAPPEDFILES map
	backupData, err := os.ReadFile(backupPath)
	if err == nil && len(backupData) > 0 {
		// Restore from backup
		//nolint:gosec // G306: File permissions 0o600 are intentionally restrictive for security
		if err := os.WriteFile(filePath, backupData, 0o600); err != nil {
			return fmt.Errorf("MAPFILE %s: failed to restore from backup %s: %w", op.Key, backupPath, err)
		}

		logger.InfoContext(ctx, "MAPFILE restored from backup (no data in LDAP)",
			"key", op.Key,
			"file_path", filePath,
			"backup_path", backupPath,
			"bytes_written", len(backupData))

		return nil
	}

	// No backup available, check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		// File exists, leave it untouched rather than deleting it
		logger.InfoContext(ctx, "MAPFILE no data in LDAP, leaving existing file untouched",
			"key", op.Key,
			"file_path", filePath)

		return nil
	}

	// No data in LDAP, no backup, and no existing file - this is expected
	logger.DebugContext(ctx, "MAPFILE no data in LDAP and no existing file",
		"key", op.Key,
		"file_path", filePath)

	return nil
}

// ExecuteMapfile executes a MAPFILE/MAPLOCAL operation.
func (e *executor) ExecuteMapfile(ctx context.Context, op MapfileOperation) error {
	ctx = logger.ContextWithComponent(ctx, "mtaops")

	span := tracing.StartSpan("MTA.Mapfile")
	if span != nil {
		span.AddMetadata("key", op.Key)

		if op.IsLocal {
			span.AddMetadata("type", "MAPLOCAL")
		} else {
			span.AddMetadata("type", "MAPFILE")
		}
	}
	defer tracing.EndSpan(span)

	// Get the mapped file path
	filePath, exists := e.mappedFiles[op.Key]
	if !exists {
		return fmt.Errorf("unmapped MAPFILE key: %s", op.Key)
	}

	if op.IsLocal {
		return e.executeMapLocal(ctx, op, filePath)
	}

	return e.executeMapFile(ctx, op, filePath)
}

// executeMapLocal handles MAPLOCAL operations: checks whether the mapped file exists.
func (e *executor) executeMapLocal(ctx context.Context, op MapfileOperation, filePath string) error {
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			logger.DebugContext(ctx, "MAPLOCAL file does not exist",
				"key", op.Key,
				"file_path", filePath)

			return nil
		}

		return fmt.Errorf("MAPLOCAL %s: error checking file %s: %w", op.Key, filePath, err)
	}

	logger.DebugContext(ctx, "MAPLOCAL file exists",
		"key", op.Key,
		"file_path", filePath)

	return nil
}

// executeMapFile handles MAPFILE operations: base64-decodes and writes to file when content changed.
func (e *executor) executeMapFile(ctx context.Context, op MapfileOperation, filePath string) error {
	if op.Base64Data == "" {
		return e.handleEmptyMapfileData(ctx, op, filePath)
	}

	data, err := base64.StdEncoding.DecodeString(op.Base64Data)
	if err != nil {
		return fmt.Errorf("MAPFILE %s: failed to decode base64 data: %w", op.Key, err)
	}

	// Skip write when content is unchanged.
	//nolint:gosec // G304: File path comes from trusted MAPPEDFILES map
	existingData, err := os.ReadFile(filePath)
	if err == nil && bytes.Equal(existingData, data) {
		logger.DebugContext(ctx, "MAPFILE already has correct content",
			"key", op.Key,
			"file_path", filePath)

		return nil
	}

	//nolint:gosec // G306: File permissions 0o600 are intentionally restrictive for security
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return fmt.Errorf("MAPFILE %s: failed to write file %s: %w", op.Key, filePath, err)
	}

	logger.InfoContext(ctx, "MAPFILE wrote data to file",
		"key", op.Key,
		"bytes_written", len(data),
		"file_path", filePath)

	return nil
}

// ExecuteLdapWrite executes an LDAP cn=config write operation.
func (e *executor) ExecuteLdapWrite(ctx context.Context, op LdapOperation) error {
	ctx = logger.ContextWithComponent(ctx, "mtaops")

	span := tracing.StartSpan("MTA.LdapWrite")
	if span != nil {
		span.AddMetadata("key", op.Key)
	}
	defer tracing.EndSpan(span)

	logger.DebugContext(ctx, "Executing LDAP write",
		"key", op.Key,
		"value", op.Value)

	// Use the existing LDAP manager's ModifyAttribute method
	// The ldap package already has the keymap and handles DN/attr mapping internally
	if err := e.ldapManager.ModifyAttribute(ctx, op.Key, op.Value); err != nil {
		return fmt.Errorf("LDAP write failed for %s: %w", op.Key, err)
	}

	logger.InfoContext(ctx, "LDAP attribute set",
		"key", op.Key,
		"value", op.Value)

	return nil
}
