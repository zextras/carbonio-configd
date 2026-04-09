// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

var postfixBin = commonPath + "/sbin/postfix"

// mtaCustomStop runs "sudo postfix stop" to gracefully shut down postfix.
// zextras has NOPASSWD sudo rights for postfix binary.
func mtaCustomStop(ctx context.Context, _ *ServiceDef) error {
	// #nosec G204 — fixed binary path from internal registry
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", postfixBin, "stop")

	out, err := cmd.CombinedOutput()
	if err != nil {
		// "not running" means postfix is already stopped — treat as success.
		if strings.Contains(string(out), "is not running") {
			return nil
		}

		return fmt.Errorf("postfix stop: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}
