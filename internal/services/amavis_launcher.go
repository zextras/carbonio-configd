// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	// amavisMCIdentity is the identity of the amavis-mc daemon (a perl script
	// that retitles itself, like amavisd): a name without a slash matches the
	// basename of an argv token, so both the script path and the retitled
	// process are found. Legacy amavisdctl: `ps | awk '$6 ~ /amavis-mc$/'`.
	amavisMCIdentity = "amavis-mc"
	// amavisReadyAttempts is how long the legacy amavisdctl waits (10 x 1s) for
	// amavis-mc and for amavisd to appear after launching them.
	amavisReadyAttempts = 10
	amavisReadyInterval = time.Second
)

// amavisPreStart prepares amavisd's data directory and, unless the start is
// `norewrite`, regenerates the altermime configuration — the pieces of legacy
// amavisdctl that sit around its `configrewrite amavis antispam` call
// (rewrite_config: configrewrite, then libexec/zmaltermimeconfig). A failing
// zmaltermimeconfig is ignored, as the legacy wrapper discards its output.
func (c *Controller) amavisPreStart(ctx context.Context, _ *ServiceManager) error {
	if err := c.env.FS.MkdirAll(c.p.amavisDir+"/.spamassassin", 0o755); err != nil {
		return fmt.Errorf("create %s/.spamassassin: %w", c.p.amavisDir, err)
	}

	if c.rewriteSkipped(ctx) {
		return nil
	}

	if _, err := c.env.FS.Stat(c.p.altermime); err != nil {
		logger.DebugContext(ctx, "zmaltermimeconfig not installed; skipping", "path", c.p.altermime, "error", err)

		return nil //nolint:nilerr // optional helper: absent means nothing to run
	}

	if out, err := sys.CombinedOutput(ctx, c.env.Exec, c.p.altermime); err != nil {
		logger.WarnContext(ctx, "zmaltermimeconfig failed", "error", err,
			"output", strings.TrimSpace(string(out)))
	}

	return nil
}

// amavisInstalled reports whether the amavisd binary is present. Legacy
// amavisdctl and antivirusctl are no-ops (exit 0) on hosts without it.
func (c *Controller) amavisInstalled() bool {
	_, err := c.env.FS.Stat(c.p.common + "/sbin/amavisd")

	return err == nil
}

// amavisCustomStart mirrors the 'start' branch of legacy amavisdctl: start
// amavis-mc (through sudo) and wait for it, prepare quarantine/tmp/var
// (clearing stale tmp/amavis-* directories), then start amavisd and wait for
// its master process. An already running daemon is left alone.
func (c *Controller) amavisCustomStart(ctx context.Context, def *ServiceDef) error {
	if !c.amavisInstalled() {
		logger.InfoContext(ctx, "amavisd is not installed; nothing to start")

		return nil
	}

	if err := c.startAmavisMC(ctx); err != nil {
		return err
	}

	if c.pidFromProcessName(ctx, def.ProcessName) > 0 {
		logger.InfoContext(ctx, "amavisd is already running")

		return nil
	}

	if err := c.prepareAmavisDirs(ctx); err != nil {
		return err
	}

	if err := c.startDirect(ctx, def.Name, def); err != nil {
		return err
	}

	if !c.waitForIdentity(ctx, def.ProcessName, amavisReadyAttempts, amavisReadyInterval) {
		return fmt.Errorf("amavisd did not start within %d seconds", amavisReadyAttempts)
	}

	return nil
}

// startAmavisMC launches amavis-mc unless it is already running.
func (c *Controller) startAmavisMC(ctx context.Context) error {
	if c.pidFromProcessName(ctx, amavisMCIdentity) > 0 {
		logger.InfoContext(ctx, "amavis-mc is already running")

		return nil
	}

	if err := c.sudoRun(ctx, c.p.amavisMC); err != nil {
		return fmt.Errorf("start amavis-mc: %w", err)
	}

	if !c.waitForIdentity(ctx, amavisMCIdentity, amavisReadyAttempts, amavisReadyInterval) {
		return fmt.Errorf("amavis-mc did not start within %d seconds", amavisReadyAttempts)
	}

	return nil
}

// prepareAmavisDirs creates quarantine, tmp and var under the amavisd data
// directory, and clears leftover tmp/amavis-* directories of a previous run.
func (c *Controller) prepareAmavisDirs(ctx context.Context) error {
	tmp := c.p.amavisDir + "/tmp"

	if entries, err := c.env.FS.ReadDir(tmp); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "amavis-") {
				continue
			}

			if rmErr := c.env.FS.RemoveAll(filepath.Join(tmp, e.Name())); rmErr != nil {
				logger.WarnContext(ctx, "Failed to remove stale amavis tmp directory",
					"path", filepath.Join(tmp, e.Name()), "error", rmErr)
			}
		}
	}

	for _, dir := range []string{"quarantine", "tmp", "var"} {
		if err := c.env.FS.MkdirAll(c.p.amavisDir+"/"+dir, 0o755); err != nil {
			return fmt.Errorf("create %s/%s: %w", c.p.amavisDir, dir, err)
		}
	}

	return nil
}

// waitForIdentity polls the process table until a process of the identity
// appears, up to attempts probes spaced by interval (the clock is injected, so
// tests do not sleep). It reports whether the process showed up.
func (c *Controller) waitForIdentity(ctx context.Context, identity string, attempts int, interval time.Duration) bool {
	for i := range attempts {
		if c.pidFromProcessName(ctx, identity) > 0 {
			return true
		}

		if i == attempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			return false
		case <-c.env.Clock.After(interval):
		}
	}

	return false
}

// amavisCustomStop mirrors the 'stop' branch of legacy amavisdctl: terminate
// amavisd, then amavis-mc. Neither being up is not an error.
func (c *Controller) amavisCustomStop(ctx context.Context, def *ServiceDef) error {
	var firstErr error

	for _, identity := range []string{def.ProcessName, amavisMCIdentity} {
		if err := c.killProcess(ctx, identity); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// companionsRunning reports whether every Companion identity of def has a
// live process.
func (c *Controller) companionsRunning(ctx context.Context, def *ServiceDef) bool {
	for _, identity := range def.Companions {
		if c.pidFromProcessName(ctx, identity) == 0 {
			return false
		}
	}

	return true
}
