// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// retryOutcome is what retryWithBackoff returned plus the fake time its
// back-off sleeps consumed.
type retryOutcome[T any] struct {
	result  T
	err     error
	elapsed time.Duration
}

// runRetry runs retryWithBackoff on a fake clock, advancing it one second at
// a time whenever the retry loop is parked in a back-off sleep, and reports
// how much fake time the back-off took.
func runRetry[T any](
	ctx context.Context, name string, maxRetries int, fn func() (T, error),
) retryOutcome[T] {
	clock := systest.NewClock(nil)
	start := clock.Now()
	done := make(chan retryOutcome[T], 1)

	go func() {
		r, err := retryWithBackoff(ctx, clock, name, maxRetries, fn)
		done <- retryOutcome[T]{result: r, err: err}
	}()

	for {
		select {
		case out := <-done:
			out.elapsed = clock.Now().Sub(start)

			return out
		default:
		}

		if clock.Waiters() > 0 {
			clock.Advance(time.Second)
		}
	}
}

func TestRetryWithBackoff_Success(t *testing.T) {
	t.Parallel()

	callCount := 0

	out := runRetry(context.Background(), "test-op", 3, func() (string, error) {
		callCount++

		return "ok", nil
	})

	if out.err != nil {
		t.Fatalf("expected no error, got %v", out.err)
	}

	if out.result != "ok" {
		t.Fatalf("expected 'ok', got %q", out.result)
	}

	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	if out.elapsed != 0 {
		t.Fatalf("first-attempt success must not back off, slept %v", out.elapsed)
	}
}

// Back-off before attempt n is n seconds: 2s before the 2nd, 3s before the 3rd.
func TestRetryWithBackoff_AllRetriesFail(t *testing.T) {
	t.Parallel()

	callCount := 0

	out := runRetry(context.Background(), "test-op", 3, func() (string, error) {
		callCount++

		return "", fmt.Errorf("attempt %d failed", callCount)
	})

	if out.err == nil {
		t.Fatal("expected error, got nil")
	}

	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}

	if out.elapsed != 5*time.Second {
		t.Fatalf("expected 2s+3s of back-off, slept %v", out.elapsed)
	}
}

func TestRetryWithBackoff_SucceedsOnRetry(t *testing.T) {
	t.Parallel()

	callCount := 0

	out := runRetry(context.Background(), "test-op", 3, func() (int, error) {
		callCount++
		if callCount < 3 {
			return 0, fmt.Errorf("not yet")
		}

		return 42, nil
	})

	if out.err != nil {
		t.Fatalf("expected no error, got %v", out.err)
	}

	if out.result != 42 {
		t.Fatalf("expected 42, got %d", out.result)
	}

	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}

	if out.elapsed != 5*time.Second {
		t.Fatalf("expected 2s+3s of back-off, slept %v", out.elapsed)
	}
}

func TestRetryWithBackoff_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callCount := 0

	out := runRetry(ctx, "test-op", 5, func() (string, error) {
		callCount++
		if callCount == 1 {
			cancel()
		}

		return "", ctx.Err()
	})

	// After cancellation, ctx.Err() returns non-nil, so fn returns error
	if out.err == nil {
		t.Fatal("expected error after cancellation")
	}

	if callCount > 2 {
		t.Fatalf("expected at most 2 calls after cancellation, got %d", callCount)
	}
}
