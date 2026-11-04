// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapwatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// stream runs sync mode until ctx is cancelled (it returns false) or the
// directory turns out not to support streaming (it returns true, after logging
// the fallback once). A stream that is lost is re-opened with exponential
// back-off.
func (w *Watcher) stream(ctx context.Context, st *runState) (unsupported bool) {
	backoff := w.backoffMin
	losses := 0 // consecutive losses; a stream that stayed up starts the count over

	for ctx.Err() == nil {
		lived, err := w.streamOnce(ctx, st)

		switch {
		case ctx.Err() != nil:
			return false
		case errors.Is(err, ErrSyncUnsupported):
			w.logFallback(ctx, err)

			return true
		}

		if w.onStreamLost != nil {
			w.onStreamLost()
		}

		// A stream that stayed up is a one-off loss; one that dies at once
		// keeps the back-off growing, so a flapping server is not hammered.
		if lived >= w.backoffMax {
			backoff = w.backoffMin
			losses = 0
		}

		losses++

		w.logStreamLost(ctx, err, losses, backoff)

		if w.clock.Sleep(ctx, backoff) != nil {
			return false
		}

		backoff = min(backoff*backoffFactor, w.backoffMax)
	}

	return false
}

// logStreamLost reports a lost stream: a single loss is routine (a server
// restart, an idle connection cut by a middlebox) and logs at info; losses in a
// row mean the stream does not stay up and log at warn.
func (w *Watcher) logStreamLost(ctx context.Context, err error, losses int, retryIn time.Duration) {
	if losses >= repeatedLossWarn {
		logger.WarnContext(ctx, "LDAP change stream lost, reconnecting",
			"error", err, "consecutive_losses", losses, "retry_in_seconds", retryIn.Seconds())

		return
	}

	logger.InfoContext(ctx, "LDAP change stream lost, reconnecting",
		"error", err, "consecutive_losses", losses, "retry_in_seconds", retryIn.Seconds())
}

// logFallback logs the switch to polling once per Run, at info: a directory
// without the syncprov overlay (a single-node Carbonio) is the normal case, not
// a fault. Later probes that find the same thing are only debug lines.
func (w *Watcher) logFallback(ctx context.Context, err error) {
	if w.fellBack {
		logger.DebugContext(ctx, "LDAP content sync still unavailable, polling", "reason", err)

		return
	}

	w.fellBack = true

	logger.InfoContext(ctx, "LDAP content sync is not available, falling back to polling "+
		"(the syncprov overlay is not loaded on this directory)",
		"reason", err,
		"retry_in_seconds", w.probe.Seconds())
}

// streamOnce runs one stream until it ends. lived is how long the stream was
// live (0 if it never was).
func (w *Watcher) streamOnce(ctx context.Context, st *runState) (lived time.Duration, err error) {
	sctx, cancel := context.WithCancel(ctx)

	var (
		wake    = make(chan struct{}, 1)
		liveCh  = make(chan struct{})
		errCh   = make(chan error, 1)
		done    = make(chan struct{})
		once    sync.Once
		pending atomic.Int64 // notifications since the last fingerprint read
	)

	go func() {
		defer close(done)

		errCh <- w.sub.Subscribe(sctx,
			func() { once.Do(func() { close(liveCh) }) },
			func() {
				pending.Add(1)

				select {
				case wake <- struct{}{}:
				default: // a wake-up is already queued
				}
			})
	}()

	defer func() {
		cancel()
		<-done
	}()

	select {
	case <-liveCh:
	case err := <-errCh:
		return 0, streamErr(err)
	case <-ctx.Done():
		return 0, ctx.Err() //nolint:wrapcheck // callers want the bare context error
	}

	liveAt := w.clock.Now()

	w.setMode(ModeSync)
	defer w.setMode(ModePoll)

	logger.InfoContext(ctx, "LDAP change stream established (content sync)",
		"resync_seconds", w.resync.Seconds())

	err = w.serveStream(ctx, st, &pending, wake, errCh)

	return w.clock.Now().Sub(liveAt), err
}

// streamErr turns a nil return of Subscribe, a stream the server closed, into
// an error.
func streamErr(err error) error {
	if err == nil {
		return errStreamClosed
	}

	return err
}

// serveStream handles a live stream: the catch-up read, the debounced reads
// after notifications and the periodic resync. It returns the reason the stream
// ended.
func (w *Watcher) serveStream(ctx context.Context, st *runState, pending *atomic.Int64,
	wake <-chan struct{}, errCh <-chan error,
) error {
	// Anything that changed while there was no stream (before the first one,
	// during a reconnect) shows up as a difference to the last good fingerprint.
	if err := w.readFingerprint(ctx, st, 0, time.Time{}); err != nil {
		return err
	}

	resync := w.clock.NewTimer(w.resync)

	var (
		due         sys.Timer
		dueC        <-chan time.Time
		first, last time.Time
	)

	defer func() {
		resync.Stop()

		if due != nil {
			due.Stop()
		}
	}()

	armDue := func(d time.Duration) {
		if due != nil {
			due.Stop()
		}

		due = w.clock.NewTimer(d)
		dueC = due.C()
	}

	disarmDue := func() {
		if due != nil {
			due.Stop()
		}

		due, dueC, first = nil, nil, time.Time{}
	}

	rearmResync := func() {
		resync.Stop()

		resync = w.clock.NewTimer(w.resync)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // callers want the bare context error
		case err := <-errCh:
			return streamErr(err)
		case <-wake:
			now := w.clock.Now()
			if first.IsZero() {
				first = now
			}

			last = now

			armDue(w.untilDue(first, last, now))
		case <-dueC:
			burst := first

			disarmDue()

			if err := w.readFingerprint(ctx, st, int(pending.Swap(0)), burst); err != nil {
				return err
			}

			rearmResync()
		case <-resync.C():
			if err := w.readFingerprint(ctx, st, 0, time.Time{}); err != nil {
				return err
			}

			rearmResync()
		}
	}
}

// untilDue is how long to wait before reading the fingerprint for a burst whose
// first and latest notifications arrived at first and last: a quiet window after
// the latest one, but no later than maxWait after the first.
func (w *Watcher) untilDue(first, last, now time.Time) time.Duration {
	at := last.Add(w.debounce)
	if limit := first.Add(w.maxWait); limit.Before(at) {
		at = limit
	}

	return max(at.Sub(now), 0)
}

// readFingerprint reads the fingerprint and reports the components that changed
// since the last one. notifications is how many stream notifications led to the
// read and burstStart when the first of them arrived; both are zero for the
// catch-up and the resync read.
func (w *Watcher) readFingerprint(ctx context.Context, st *runState, notifications int, burstStart time.Time) error {
	fp, err := w.source.Fingerprint(ctx)
	if err != nil {
		if ctx.Err() == nil && w.onPollErr != nil {
			w.onPollErr()
		}

		return fmt.Errorf("fingerprint after change notification: %w", err)
	}

	now := w.clock.Now()

	if st.base == nil {
		st.base = fp

		logger.DebugContext(ctx, "LDAP change baseline established", "components", len(fp))

		return nil
	}

	w.observe(ctx, st, fp, now)

	if !st.pending() {
		logger.DebugContext(ctx, "LDAP change notification without a change to a watched component",
			"notifications", notifications)

		return nil
	}

	if notifications > 0 {
		st.bursts = notifications
		st.first = burstStart
	}

	w.fire(ctx, st, now)

	return nil
}
