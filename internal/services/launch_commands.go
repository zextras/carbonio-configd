// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// controlCommand runs one verb of a CommandSpec service: its binary, through
// sudo -n when the service needs root (zextras has NOPASSWD sudo rights for the
// postfix binary; -n makes a missing grant fail at once instead of waiting on a
// password prompt no one will answer). It returns the combined output next to the error so a
// failing verb can be reported with what the binary said.
func (c *Controller) controlCommand(ctx context.Context, def *ServiceDef, verb []string) ([]byte, error) {
	binary, args := def.BinaryPath, verb

	if def.NeedsRoot {
		binary, args = sudoBin, append([]string{"-n", def.BinaryPath}, verb...)
	}

	// #nosec G204 — binary is a registry constant and the verb a registry literal
	return sys.CombinedOutput(ctx, c.env.Exec, binary, args...)
}

// commandRunning reports whether the CommandSpec's Status verb exits 0. A spec
// without a Status verb is never "running": every start proceeds.
func (c *Controller) commandRunning(ctx context.Context, def *ServiceDef) bool {
	if def.Commands.Status == nil {
		return false
	}

	_, err := c.controlCommand(ctx, def, def.Commands.Status)

	return err == nil
}

// startCommands starts a CommandSpec service: skip when Status says it runs
// already, run the Prepare steps, then the Start verb. The service
// self-daemonizes, so the verb returns once its master has forked.
func (c *Controller) startCommands(ctx context.Context, def *ServiceDef) error {
	spec := def.Commands

	if c.commandRunning(ctx, def) {
		logger.InfoContext(ctx, "Service already running; skipping start", "service", def.Name)

		return nil
	}

	for _, prepare := range spec.Prepare {
		if err := prepare(ctx); err != nil {
			return err
		}
	}

	out, err := c.controlCommand(ctx, def, spec.Start)
	if err != nil {
		return fmt.Errorf("%s start: %s: %w", def.BinaryPath, strings.TrimSpace(string(out)), err)
	}

	return nil
}

// stopCommands runs the Stop verb. A stop that fails is still a successful
// stop when the service is not running: the Status verb exits non-zero and no
// process of its identity is left. The verdict never depends on what the verb
// printed, which is locale-dependent; requiring the identity scan as well keeps
// a Status that failed for another reason (sudo refusing the call) from hiding
// a running service.
func (c *Controller) stopCommands(ctx context.Context, def *ServiceDef) error {
	out, err := c.controlCommand(ctx, def, def.Commands.Stop)
	if err == nil {
		return nil
	}

	if def.Commands.Status != nil && !c.commandRunning(ctx, def) && c.runningPID(ctx, def) == 0 {
		return nil
	}

	return fmt.Errorf("%s stop: %s: %w", def.BinaryPath, strings.TrimSpace(string(out)), err)
}

// reloadCommands runs the Reload verb, after restarting the enabled
// Dependencies when the spec says so: legacy zmmtactl reload runs `reload` on
// each START_SCRIPTS entry — zmmilterctl and zmsaslauthdctl implement it as
// stop+start — and then postfix reload (carbonio-core 4.6.7 src/bin/mtactl.sh).
func (c *Controller) reloadCommands(ctx context.Context, def *ServiceDef) error {
	if def.Commands.ReloadRestartsDependencies {
		for _, dep := range def.Dependencies {
			if !c.isDepEnabled(ctx, dep) {
				continue
			}

			if err := c.ServiceRestart(ctx, dep); err != nil {
				logger.WarnContext(ctx, "Dependency restart failed during reload",
					"service", def.Name, "dependency", dep, "error", err)
			}
		}
	}

	logger.InfoContext(ctx, "Reloading service natively (legacy mode)", "service", def.Name)

	out, err := c.controlCommand(ctx, def, def.Commands.Reload)
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", def.BinaryPath,
			strings.Join(def.Commands.Reload, " "), strings.TrimSpace(string(out)), err)
	}

	return nil
}
