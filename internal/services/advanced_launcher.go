// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

const (
	advancedPollInterval = 2 * time.Second
	advancedPollAttempts = 10
)

var advancedJARDir = basePath + "/lib/ext/carbonio"

// MailboxAdvancedStatusHook is a PostStart hook for mailbox that polls
// Carbonio Advanced modules until they are ready, mirroring the legacy
// `advanced_status 2` call in mailboxdctl.sh.
func MailboxAdvancedStatusHook(ctx context.Context, _ *ServiceManager) error {
	if !advancedInstalled() {
		return nil
	}

	cfg, err := localconfig.LoadLocalConfig()
	if err != nil {
		logger.DebugContext(ctx, "Advanced status hook: localconfig load failed", "error", err)

		return nil //nolint:nilerr // hook is best-effort
	}

	client := zxadmin.New(cfg)

	logger.InfoContext(ctx, "Waiting for Carbonio Advanced modules to become ready")

	for range advancedPollAttempts {
		if advancedRunning(ctx, client) {
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

// advancedRunning queries the admin endpoint and returns true when
// the call succeeds and at least one module is reported.
func advancedRunning(ctx context.Context, client *zxadmin.Client) bool {
	modules, err := client.GetAllServicesStatus(ctx)
	if err != nil {
		return false
	}

	return len(modules) > 0
}
