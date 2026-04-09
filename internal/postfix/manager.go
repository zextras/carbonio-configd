// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfix

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// PostfixManager implements the Manager interface for Postfix configuration.
//
//nolint:revive // PostfixManager name is intentional for clarity
type PostfixManager struct {
	postconfCmd        string            // Path to postconf command
	postconfChanges    map[string]string // Pending postconf changes (key -> value)
	postconfdDeletions []string          // Pending postconfd deletions (keys)
}

// NewPostfixManager creates a new PostfixManager instance.
// The postconfCmd parameter specifies the path to the postconf binary (default: "postconf").
func NewPostfixManager(postconfCmd string) *PostfixManager {
	if postconfCmd == "" {
		postconfCmd = "postconf"
	}

	return &PostfixManager{
		postconfCmd:        postconfCmd,
		postconfChanges:    make(map[string]string),
		postconfdDeletions: make([]string, 0),
	}
}

// AddPostconf adds a postconf directive (postconf -e key=value).
// The change is queued and will be executed when FlushPostconf is called.
func (pm *PostfixManager) AddPostconf(ctx context.Context, key, value string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "postfix")

	if key == "" {
		return fmt.Errorf("postconf key cannot be empty")
	}

	logger.DebugContext(ctx, "Adding postconf",
		"key", key,
		"value", value)
	pm.postconfChanges[key] = value

	return nil
}

// AddPostconfd adds a postconfd directive (postconf -X key for deletion).
// The deletion is queued and will be executed when FlushPostconfd is called.
func (pm *PostfixManager) AddPostconfd(ctx context.Context, key string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "postfix")

	if key == "" {
		return fmt.Errorf("postconfd key cannot be empty")
	}

	logger.DebugContext(ctx, "Adding postconfd",
		"key", key)
	pm.postconfdDeletions = append(pm.postconfdDeletions, key)

	return nil
}

// FlushPostconf executes all accumulated postconf changes in a single batch.
// It runs postconf -e with all queued changes and clears the queue on success.
// Returns error if the postconf command fails.
func (pm *PostfixManager) FlushPostconf(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "postfix")
	if len(pm.postconfChanges) == 0 {
		logger.DebugContext(ctx, "No postconf changes to flush")

		return nil
	}

	logger.DebugContext(ctx, "Flushing postconf changes",
		"change_count", len(pm.postconfChanges))

	// Build arguments for batched postconf execution
	args := make([]string, 0, len(pm.postconfChanges)*2+1)
	args = append(args, "-e")

	for key, value := range pm.postconfChanges {
		// Sanitize value: replace newlines with spaces (Jython behavior)
		value = strings.ReplaceAll(value, "\n", " ")

		// Build argument: key=value
		arg := fmt.Sprintf("%s=%s", key, value)
		args = append(args, arg)

		logger.DebugContext(ctx, "Queued postconf",
			"arg", arg)
	}

	// Execute batched postconf command
	logger.DebugContext(ctx, "Executing batched postconf",
		"command", pm.postconfCmd,
		"parameter_count", len(pm.postconfChanges))

	cmd := exec.CommandContext(ctx, pm.postconfCmd, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.ErrorContext(ctx, "Batched postconf failed",
			"error", err,
			"output", string(output))

		return fmt.Errorf("postconf -e batch failed: %w (output: %s)", err, string(output))
	}

	// Clear pending changes on success
	pm.postconfChanges = make(map[string]string)

	logger.DebugContext(ctx, "Postconf flush complete")

	return nil
}

// FlushPostconfd executes all accumulated postconfd deletions in a single batch.
// It runs postconf -X with all queued deletions and clears the queue on success.
// Deduplicates keys to avoid redundant deletions.
// Returns error if the postconf command fails.
func (pm *PostfixManager) FlushPostconfd(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "postfix")
	if len(pm.postconfdDeletions) == 0 {
		logger.DebugContext(ctx, "No postconfd deletions to flush")

		return nil
	}

	logger.DebugContext(ctx, "Flushing postconfd deletions",
		"deletion_count", len(pm.postconfdDeletions))

	// Deduplicate keys (map-based deduplication)
	uniqueKeys := make(map[string]bool)
	for _, key := range pm.postconfdDeletions {
		uniqueKeys[key] = true
	}

	// Build arguments for batched postconf -X execution
	args := make([]string, 0, len(uniqueKeys)+1)
	args = append(args, "-X")

	for key := range uniqueKeys {
		args = append(args, key)
		logger.DebugContext(ctx, "Queued postconfd",
			"key", key)
	}

	// Execute batched postconf -X command
	logger.DebugContext(ctx, "Executing batched postconfd",
		"command", pm.postconfCmd,
		"unique_key_count", len(uniqueKeys))

	cmd := exec.CommandContext(ctx, pm.postconfCmd, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.ErrorContext(ctx, "Batched postconfd failed",
			"error", err,
			"output", string(output))

		return fmt.Errorf("postconf -X batch failed: %w (output: %s)", err, string(output))
	}

	// Clear pending deletions on success
	pm.postconfdDeletions = make([]string, 0)

	logger.DebugContext(ctx, "Postconfd flush complete")

	return nil
}

// GetPendingChanges returns the current pending postconf and postconfd changes.
// Returns a copy of the changes map and deletions slice.
func (pm *PostfixManager) GetPendingChanges() (postconf map[string]string, postconfd []string) {
	// Return copies to prevent external modification
	postconfCopy := make(map[string]string, len(pm.postconfChanges))
	maps.Copy(postconfCopy, pm.postconfChanges)

	postconfdCopy := make([]string, len(pm.postconfdDeletions))
	copy(postconfdCopy, pm.postconfdDeletions)

	return postconfCopy, postconfdCopy
}

// ClearPending clears all pending postconf and postconfd changes without executing them.
func (pm *PostfixManager) ClearPending(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "postfix")
	logger.DebugContext(ctx, "Clearing pending changes",
		"postconf_change_count", len(pm.postconfChanges),
		"postconfd_deletion_count", len(pm.postconfdDeletions))
	pm.postconfChanges = make(map[string]string)
	pm.postconfdDeletions = make([]string, 0)
}
