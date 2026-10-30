// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package watchdog

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// IsEnabled returns whether the watchdog is currently active.
func (w *Watchdog) IsEnabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.enabled
}

// SetServiceEnabled sets whether a specific service should be monitored by watchdog.
func (w *Watchdog) SetServiceEnabled(ctx context.Context, service string, enabled bool) {
	ctx = logger.ContextWithComponentOnce(ctx, "watchdog")

	w.mu.Lock()
	defer w.mu.Unlock()

	w.ServiceEnabled[service] = enabled
	logger.DebugContext(ctx, "Watchdog monitoring status changed",
		"service", service,
		"enabled", enabled)
}
