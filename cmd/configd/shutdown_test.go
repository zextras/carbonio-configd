// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"
	"time"
)

// TestSleepWithContext verifies that sleep can be interrupted by context cancellation
func TestSleepWithContext(t *testing.T) {
	t.Run("context cancellation interrupts sleep", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		reloadChan := make(chan struct{})

		// Cancel context after 100ms
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		interrupted := SleepWithContext(ctx, 5*time.Second, reloadChan)
		elapsed := time.Since(start)

		if !interrupted {
			t.Error("Expected sleep to be interrupted, but it wasn't")
		}

		if elapsed > 500*time.Millisecond {
			t.Errorf("Expected sleep to be interrupted quickly (~100ms), but took %v", elapsed)
		}
	})

	t.Run("reload channel interrupts sleep", func(t *testing.T) {
		ctx := context.Background()
		reloadChan := make(chan struct{})

		// Send reload signal after 100ms
		go func() {
			time.Sleep(100 * time.Millisecond)
			reloadChan <- struct{}{}
		}()

		start := time.Now()
		interrupted := SleepWithContext(ctx, 5*time.Second, reloadChan)
		elapsed := time.Since(start)

		if !interrupted {
			t.Error("Expected sleep to be interrupted, but it wasn't")
		}

		if elapsed > 500*time.Millisecond {
			t.Errorf("Expected sleep to be interrupted quickly (~100ms), but took %v", elapsed)
		}
	})

	t.Run("sleep completes naturally", func(t *testing.T) {
		ctx := context.Background()
		reloadChan := make(chan struct{})

		start := time.Now()
		interrupted := SleepWithContext(ctx, 100*time.Millisecond, reloadChan)
		elapsed := time.Since(start)

		if interrupted {
			t.Error("Expected sleep to complete naturally, but it was interrupted")
		}

		if elapsed < 100*time.Millisecond || elapsed > 200*time.Millisecond {
			t.Errorf("Expected sleep to take ~100ms, but took %v", elapsed)
		}
	})
}
