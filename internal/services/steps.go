// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// runSteps runs the steps in order and stops at the first that fails.
func (c *Controller) runSteps(ctx context.Context, steps []Step) error {
	for i := range steps {
		if err := c.runStep(ctx, &steps[i]); err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) runStep(ctx context.Context, step *Step) error {
	if step.UnlessExists != "" {
		if _, err := c.env.FS.Stat(step.UnlessExists); err == nil {
			return nil
		}
	}

	switch {
	case len(step.Mkdir) > 0:
		for _, dir := range step.Mkdir {
			if err := c.env.FS.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", dir, err)
			}
		}

		return nil
	case step.RemoveStale != nil:
		c.removeStaleDirs(ctx, step.RemoveStale)

		return nil
	case step.Run != nil:
		return c.runCommand(ctx, step.Run)
	case step.Hook != nil:
		return step.Hook(ctx)
	}

	return nil
}

// removeStaleDirs removes the directories of stale.Dir whose name starts with
// stale.Prefix. A directory that cannot be read or removed is logged, never
// fatal: the service starts with the leftovers.
func (c *Controller) removeStaleDirs(ctx context.Context, stale *StaleDirs) {
	entries, err := c.env.FS.ReadDir(stale.Dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), stale.Prefix) {
			continue
		}

		path := filepath.Join(stale.Dir, e.Name())

		if rmErr := c.env.FS.RemoveAll(path); rmErr != nil {
			logger.WarnContext(ctx, "Failed to remove stale directory", "path", path, "error", rmErr)
		}
	}
}

// runCommand runs a RunStep: skipped when its IfExists path is absent or, for
// SkipOnNoRewrite, when the start must not regenerate configuration; a failure
// is returned with the command line and what it printed, or logged for a
// BestEffort one.
func (c *Controller) runCommand(ctx context.Context, run *RunStep) error {
	if run.SkipOnNoRewrite && c.rewriteSkipped(ctx) {
		return nil
	}

	if run.IfExists != "" {
		if _, err := c.env.FS.Stat(run.IfExists); err != nil {
			logger.DebugContext(ctx, "Optional input not present; skipping command",
				"path", run.IfExists, "command", run.Path, "error", err)

			return nil
		}
	}

	binary, args := run.Path, run.Args
	if run.Sudo {
		binary, args = sudoBin, append([]string{"-n", run.Path}, run.Args...)
	}

	// #nosec G204 — the binary is a registry constant and its arguments literals or registry paths
	out, err := sys.CombinedOutput(ctx, c.env.Exec, binary, args...)
	if err == nil {
		return nil
	}

	if run.BestEffort {
		logger.WarnContext(ctx, "Best-effort command failed; continuing",
			"path", run.Path, "error", err, "output", strings.TrimSpace(string(out)))

		return nil
	}

	return fmt.Errorf("%s %s: %s: %w", run.Path, strings.Join(run.Args, " "), strings.TrimSpace(string(out)), err)
}
