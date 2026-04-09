// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"testing"
)

func TestRetryWithBackoff_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx := context.Background()
	callCount := 0

	result, err := retryWithBackoff(ctx, "test-op", 3, func() (string, error) {
		callCount++
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected 'ok', got %q", result)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}
}

func TestRetryWithBackoff_AllRetriesFail(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: retryWithBackoff has exponential backoff sleeps")
	}
	ctx := context.Background()
	callCount := 0

	_, err := retryWithBackoff(ctx, "test-op", 3, func() (string, error) {
		callCount++
		return "", fmt.Errorf("attempt %d failed", callCount)
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
}

func TestRetryWithBackoff_SucceedsOnRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: retryWithBackoff has exponential backoff sleeps")
	}
	ctx := context.Background()
	callCount := 0

	result, err := retryWithBackoff(ctx, "test-op", 3, func() (int, error) {
		callCount++
		if callCount < 3 {
			return 0, fmt.Errorf("not yet")
		}
		return 42, nil
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
}

func TestRetryWithBackoff_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callCount := 0

	_, err := retryWithBackoff(ctx, "test-op", 5, func() (string, error) {
		callCount++
		if callCount == 1 {
			cancel()
		}
		return "", ctx.Err()
	})

	// After cancellation, ctx.Err() returns non-nil, so fn returns error
	if err == nil {
		t.Fatal("expected error after cancellation")
	}
	if callCount > 2 {
		t.Fatalf("expected at most 2 calls after cancellation, got %d", callCount)
	}
}
