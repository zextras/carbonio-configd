// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"errors"
	"testing"
	"time"
)

const loadTestDeadline = 10 * time.Second

// runLoadAttemptAsync runs runLoadAttempt in a goroutine so a regression that
// blocks on a hung loader fails the test instead of hanging it.
func runLoadAttemptAsync(
	ctx context.Context, loadFuncs []loadFunc, timeout time.Duration,
) (timedOut bool, errs []error, ctxErr error) {
	type result struct {
		timedOut bool
		errs     []error
		ctxErr   error
	}

	ch := make(chan result, 1)

	go func() {
		to, e, c := runLoadAttempt(ctx, loadFuncs, timeout)
		ch <- result{to, e, c}
	}()

	select {
	case r := <-ch:
		return r.timedOut, r.errs, r.ctxErr
	case <-time.After(loadTestDeadline):
		panic("runLoadAttempt blocked on a hung loader")
	}
}

// TestRunLoadAttempt_HungLoaderTimesOut: a loader that ignores its context must
// neither hang the caller nor keep a live (uncancelled) context.
func TestRunLoadAttempt_HungLoaderTimesOut(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	loaderCtx := make(chan context.Context, 1)

	loadFuncs := []loadFunc{
		{"ok", func(context.Context) error { return nil }},
		{"hung", func(ctx context.Context) error {
			loaderCtx <- ctx
			<-release // ignores ctx on purpose

			return nil
		}},
	}

	timedOut, errs, ctxErr := runLoadAttemptAsync(context.Background(), loadFuncs, 20*time.Millisecond)
	if ctxErr != nil {
		t.Fatalf("ctxErr = %v, want nil", ctxErr)
	}

	if !timedOut {
		t.Error("timedOut = false, want true for a hung loader")
	}

	if len(errs) != 0 {
		t.Errorf("errs = %v, want none (hung loader results are discarded)", errs)
	}

	select {
	case ctx := <-loaderCtx:
		select {
		case <-ctx.Done():
		case <-time.After(loadTestDeadline):
			t.Error("loader context was not cancelled after the timeout")
		}
	case <-time.After(loadTestDeadline):
		t.Fatal("hung loader never started")
	}
}

// TestRunLoadAttempt_ParentCancelDoesNotWaitForLoaders: shutdown must not wait
// for a loader that is stuck.
func TestRunLoadAttempt_ParentCancelDoesNotWaitForLoaders(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	loadFuncs := []loadFunc{
		{"hung", func(context.Context) error {
			close(started)
			<-release

			return nil
		}},
	}

	go func() {
		<-started
		cancel()
	}()

	timedOut, _, ctxErr := runLoadAttemptAsync(ctx, loadFuncs, time.Hour)
	if !errors.Is(ctxErr, context.Canceled) {
		t.Errorf("ctxErr = %v, want context.Canceled", ctxErr)
	}

	if timedOut {
		t.Error("timedOut = true, want false on parent cancellation")
	}
}

// TestRunLoadAttempt_ErrorsInLoaderOrder: errors are reported in loadFuncs
// order regardless of completion order.
func TestRunLoadAttempt_ErrorsInLoaderOrder(t *testing.T) {
	slowDone := make(chan struct{})

	loadFuncs := []loadFunc{
		{"first", func(context.Context) error {
			<-slowDone // finishes after "second"

			return errors.New("first failed")
		}},
		{"second", func(context.Context) error {
			defer close(slowDone)

			return errors.New("second failed")
		}},
	}

	timedOut, errs, ctxErr := runLoadAttemptAsync(context.Background(), loadFuncs, time.Minute)
	if timedOut || ctxErr != nil {
		t.Fatalf("timedOut=%v ctxErr=%v, want false/nil", timedOut, ctxErr)
	}

	if len(errs) != 2 {
		t.Fatalf("len(errs) = %d, want 2", len(errs))
	}

	if got := errs[0].Error(); got != "thread first failed: first failed" {
		t.Errorf("errs[0] = %q, want the first loader's error", got)
	}
}

// TestSleepCtx_CancelInterruptsSleep: a cancelled context ends the backoff early.
func TestSleepCtx_CancelInterruptsSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)

	go func() { done <- sleepCtx(ctx, time.Hour) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("sleepCtx err = %v, want context.Canceled", err)
		}
	case <-time.After(loadTestDeadline):
		t.Fatal("sleepCtx did not return after cancellation")
	}
}

// TestRetryWithBackoff_CancelDuringBackoff: cancelling while waiting between
// attempts must abort without running another attempt.
func TestRetryWithBackoff_CancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	type res struct{ err error }

	done := make(chan res, 1)

	go func() {
		_, err := retryWithBackoff(ctx, "op", 3, func() (int, error) {
			calls++

			cancel() // cancelled before the first backoff sleep starts

			return 0, errors.New("fail")
		})
		done <- res{err}
	}()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", r.err)
		}

		if calls != 1 {
			t.Errorf("calls = %d, want 1", calls)
		}
	case <-time.After(loadTestDeadline):
		t.Fatal("retryWithBackoff did not abort promptly on cancellation")
	}
}
