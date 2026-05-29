// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// recoverGoroutine logs a panic with stack trace so a crash in one
// goroutine does not silently take down the daemon. Call as:
//
//	defer recoverGoroutine(ctx, "name")
func recoverGoroutine(ctx context.Context, name string) {
	if r := recover(); r != nil {
		logger.ErrorContext(ctx, "recovered from panic in goroutine",
			"goroutine", name,
			"panic", fmt.Sprintf("%v", r),
			"stack", string(debug.Stack()))
	}
}
