// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	advancedPollInterval = 2 * time.Second
	advancedPollAttempts = 10
)

var (
	carbonioCLI    = basePath + "/bin/carbonio"
	advancedJARDir = basePath + "/lib/ext/carbonio"
)

// MailboxAdvancedStatusHook is a PostStart hook for mailbox that polls
// Carbonio Advanced modules until they are ready, mirroring the legacy
// `advanced_status 2` call in mailboxdctl.sh.
func MailboxAdvancedStatusHook(ctx context.Context, _ *ServiceManager) error {
	if !advancedInstalled() {
		return nil
	}

	if _, statErr := os.Stat(carbonioCLI); statErr != nil {
		return nil //nolint:nilerr // carbonio CLI is optional; absence is not an error
	}

	logger.InfoContext(ctx, "Waiting for Carbonio Advanced modules to become ready")

	for range advancedPollAttempts {
		if advancedRunning(ctx) {
			logger.InfoContext(ctx, "Carbonio Advanced modules are ready")

			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(advancedPollInterval):
		}
	}

	logger.WarnContext(ctx, "Carbonio Advanced modules did not become ready in time")

	return nil
}

// advancedInstalled returns true if at least one carbonio-advanced-*.jar is present.
func advancedInstalled() bool {
	entries, err := os.ReadDir(advancedJARDir)
	if err != nil {
		return false
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "carbonio-advanced-") && strings.HasSuffix(e.Name(), ".jar") {
			return true
		}
	}

	return false
}

// advancedRunning calls `carbonio core getAllServicesStatus` and returns true when the
// server responds without an "Unable to communicate" error.
func advancedRunning(ctx context.Context) bool {
	//nolint:gosec // fixed internal path
	out, err := exec.CommandContext(ctx, carbonioCLI, "core", "getAllServicesStatus").CombinedOutput()
	if err != nil {
		return false
	}

	return !strings.Contains(string(out), "Unable to communicate with server")
}
