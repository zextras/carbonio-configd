// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// runRecovered runs fn and converts a panic into an error (logged with its
// stack), so a crash in one worker goroutine fails that unit of work instead of
// taking down the daemon.
func runRecovered(ctx context.Context, name string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(ctx, "recovered from panic in goroutine",
				"goroutine", name,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()))

			err = fmt.Errorf("panic in %s: %v", name, r)
		}
	}()

	return fn()
}
