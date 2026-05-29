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

// flushPostconf is a private helper that executes postconf commands with the given flag and arguments.
// It handles the common pattern of building args, executing the command, and clearing pending items on success.
// The buildArgs callback is responsible for constructing the argument list and returning the count for logging.
// The clearPending callback is called on success to clear the pending queue.
func (pm *PostfixManager) flushPostconf(
	ctx context.Context,
	flag string,
	logPrefix string,
	buildArgs func() ([]string, int),
	clearPending func(),
) error {
	ctx = logger.ContextWithComponentOnce(ctx, "postfix")

	// Build arguments using the provided callback
	args, count := buildArgs()
	if count == 0 {
		logger.DebugContext(ctx, "No "+logPrefix+" to flush")
		return nil
	}

	logger.DebugContext(ctx, "Flushing "+logPrefix,
		"count", count)

	// Execute batched postconf command
	logger.DebugContext(ctx, "Executing batched "+logPrefix,
		"command", pm.postconfCmd,
		"parameter_count", count)

	cmd := exec.CommandContext(ctx, pm.postconfCmd, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.ErrorContext(ctx, "Batched "+logPrefix+" failed",
			"error", err,
			"output", string(output))

		return fmt.Errorf("postconf %s batch failed: %w (output: %s)", flag, err, string(output))
	}

	// Clear pending items on success
	clearPending()

	logger.DebugContext(ctx, logPrefix+" flush complete")

	return nil
}

// FlushPostconf executes all accumulated postconf changes in a single batch.
// It runs postconf -e with all queued changes and clears the queue on success.
// Returns error if the postconf command fails.
func (pm *PostfixManager) FlushPostconf(ctx context.Context) error {
	logCtx := logger.ContextWithComponentOnce(ctx, "postfix")

	return pm.flushPostconf(
		ctx,
		"-e",
		"postconf changes",
		func() ([]string, int) {
			args := make([]string, 0, len(pm.postconfChanges)*2+1)
			args = append(args, "-e")

			for key, value := range pm.postconfChanges {
				// Sanitize value: replace newlines with spaces (Jython behavior)
				value = strings.ReplaceAll(value, "\n", " ")

				// Build argument: key=value
				arg := fmt.Sprintf("%s=%s", key, value)
				args = append(args, arg)

				logger.DebugContext(logCtx, "Queued postconf",
					"arg", arg)
			}

			return args, len(pm.postconfChanges)
		},
		func() {
			pm.postconfChanges = make(map[string]string)
		},
	)
}

// FlushPostconfd executes all accumulated postconfd deletions in a single batch.
// It runs postconf -X with all queued deletions and clears the queue on success.
// Deduplicates keys to avoid redundant deletions.
// Returns error if the postconf command fails.
func (pm *PostfixManager) FlushPostconfd(ctx context.Context) error {
	logCtx := logger.ContextWithComponentOnce(ctx, "postfix")

	return pm.flushPostconf(
		ctx,
		"-X",
		"postconfd deletions",
		func() ([]string, int) {
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
				logger.DebugContext(logCtx, "Queued postconfd",
					"key", key)
			}

			return args, len(uniqueKeys)
		},
		func() {
			pm.postconfdDeletions = make([]string, 0)
		},
	)
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
