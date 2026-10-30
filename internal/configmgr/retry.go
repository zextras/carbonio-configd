// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// sleepCtx waits for d or until ctx is cancelled, whichever comes first.
// It returns ctx.Err() when cancelled so callers abort instead of sleeping on.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryWithBackoff executes fn up to maxRetries times with linear backoff.
// On each failed attempt it logs the error and sleeps for attempt * 1s (aborting early if ctx is cancelled).
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

			if err := sleepCtx(ctx, time.Duration(attempt)*time.Second); err != nil {
				return zero, err
			}
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
