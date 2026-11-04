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

// startDaemon launches a BinaryPath service that declares a DirectSpec, in the
// sequence of legacy amavisdctl start: nothing at all when the daemon is not
// installed (OptionalBinary), the Companions, a running daemon left alone
// (SkipIfRunning), the Prepare steps, the spawn, then the wait for the process
// to appear.
func (c *Controller) startDaemon(ctx context.Context, name string, def *ServiceDef) error {
	spec := def.Direct
	daemon := filepath.Base(def.BinaryPath)

	if spec.OptionalBinary && !c.binaryInstalled(def.BinaryPath) {
		logger.InfoContext(ctx, daemon+" is not installed; nothing to start")

		return nil
	}

	for _, companion := range def.Companions {
		if err := c.startCompanion(ctx, companion, spec.Wait); err != nil {
			return err
		}
	}

	if spec.SkipIfRunning && c.pidFromProcessName(ctx, def.ProcessName) > 0 {
		logger.InfoContext(ctx, daemon+" is already running")

		return nil
	}

	if err := c.runSteps(ctx, spec.Prepare); err != nil {
		return err
	}

	if err := c.startDirect(ctx, name, def); err != nil {
		return err
	}

	if spec.Wait.Attempts > 0 && !c.waitForIdentity(ctx, def.ProcessName, spec.Wait) {
		return fmt.Errorf("%s did not start within %d seconds", daemon, c.waitSeconds(spec.Wait))
	}

	return nil
}

// binaryInstalled reports whether the binary exists: legacy amavisdctl and
// antivirusctl are no-ops (exit 0) on hosts without amavisd.
func (c *Controller) binaryInstalled(path string) bool {
	_, err := c.env.FS.Stat(path)

	return err == nil
}

// startCompanion launches a companion's Binary through sudo unless a process
// of its identity runs already, then waits for it to appear. A companion
// without a Binary is only observed (status), never started.
func (c *Controller) startCompanion(ctx context.Context, companion Companion, wait ProcessWait) error {
	if companion.Binary == "" {
		return nil
	}

	if c.pidFromProcessName(ctx, companion.Identity) > 0 {
		logger.InfoContext(ctx, companion.Identity+" is already running")

		return nil
	}

	if out, err := c.runLaunch(ctx, companion.Identity, sudoBin, "-n", companion.Binary); err != nil {
		return fmt.Errorf("start %s: %s: %w", companion.Identity, strings.TrimSpace(string(out)), err)
	}

	if wait.Attempts > 0 && !c.waitForIdentity(ctx, companion.Identity, wait) {
		return fmt.Errorf("%s did not start within %d seconds", companion.Identity, c.waitSeconds(wait))
	}

	return nil
}

// waitSeconds is the length of wait, in whole seconds, for the timeout errors.
func (c *Controller) waitSeconds(wait ProcessWait) int {
	return int(time.Duration(wait.Attempts) * wait.Interval / time.Second)
}

// waitForIdentity polls the process table until a process of the identity
// appears, up to wait.Attempts probes spaced by wait.Interval (the clock is
// injected, so tests do not sleep). It reports whether the process showed up.
func (c *Controller) waitForIdentity(ctx context.Context, identity string, wait ProcessWait) bool {
	for i := range wait.Attempts {
		if c.pidFromProcessName(ctx, identity) > 0 {
			return true
		}

		if i == wait.Attempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			return false
		case <-c.env.Clock.After(wait.Interval):
		}
	}

	return false
}

// killAll terminates every process of def: the ProcessName identity, then
// each companion. Neither being up is not an error; the first failure is
// returned after all have been tried.
func (c *Controller) killAll(ctx context.Context, def *ServiceDef) error {
	firstErr := c.killProcess(ctx, def.ProcessName)

	for _, companion := range def.Companions {
		if err := c.killProcess(ctx, companion.Identity); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// companionsRunning reports whether every companion of def has a live process.
func (c *Controller) companionsRunning(ctx context.Context, def *ServiceDef) bool {
	for _, companion := range def.Companions {
		if c.pidFromProcessName(ctx, companion.Identity) == 0 {
			return false
		}
	}

	return true
}
