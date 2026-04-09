// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// retryWithBackoff executes fn up to maxRetries times with linear backoff.
// On each failed attempt it logs the error and sleeps for attempt * 1s.
// If all attempts fail, it logs a warning and returns the last error.
func retryWithBackoff[T any](ctx context.Context, name string, maxRetries int, fn func() (T, error)) (T, error) {
	var (
		lastErr error
		zero    T
	)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}

		if attempt > 1 {
			logger.DebugContext(ctx, "Retry attempt",
				"operation", name,
				"attempt", attempt,
				"max_retries", maxRetries)
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		result, err := fn()
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				logger.DebugContext(ctx, "Attempt failed",
					"operation", name,
					"attempt", attempt,
					"error", err)

				continue
			}

			logger.WarnContext(ctx, "Skipping update after retries",
				"operation", name,
				"max_retries", maxRetries)

			return zero, lastErr
		}

		return result, nil
	}

	return zero, lastErr
}
