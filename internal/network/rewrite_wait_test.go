// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripAsync runs roundTrip in the background and delivers its reply.
func roundTripAsync(addr, raw string) <-chan string {
	out := make(chan string, 1)

	go func() {
		resp, err := roundTrip(addr, raw)
		if err != nil {
			resp = "client error: " + err.Error()
		}

		out <- resp
	}()

	return out
}

// TestRewriteWaitsForMainLoop pins the legacy listener contract (listener.py:
// REWRITE blocks on lAction): callers run "configrewrite X && start X", so the
// reply must not be sent before the main loop processed the sections.
func TestRewriteWaitsForMainLoop(t *testing.T) {
	done := make(chan struct{})
	trigger := &MockActionTrigger{wait: &RewriteWait{Done: done}}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{ActionTrigger: trigger})
	startServer(context.Background(), t, server)

	reply := roundTripAsync(serverAddr(server), "REWRITE mta")

	select {
	case got := <-reply:
		t.Fatalf("got reply %q before the main loop processed the request", got)
	case <-time.After(200 * time.Millisecond):
	}

	if n := trigger.GetRewriteCallCount(); n != 1 {
		t.Fatalf("TriggerRewrite called %d times, want 1", n)
	}

	close(done)

	select {
	case got := <-reply:
		if got != respRewritesComplete {
			t.Errorf("reply = %q, want %q", got, respRewritesComplete)
		}
	case <-time.After(testDeadline):
		t.Fatal("no reply after the main loop processed the request")
	}
}

// TestRewriteTimeout pins ERROR REWRITE TIMEOUT: a request the main loop does
// not process in time is answered with the error, and released through Cancel
// (the request itself stays queued in the main loop).
func TestRewriteTimeout(t *testing.T) {
	var canceled atomic.Bool

	trigger := &MockActionTrigger{wait: &RewriteWait{
		Done:   make(chan struct{}),
		Cancel: func() { canceled.Store(true) },
	}}
	handler := &ConfigdRequestHandler{ActionTrigger: trigger, RewriteTimeout: 100 * time.Millisecond}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
	startServer(context.Background(), t, server)

	start := time.Now()

	got, err := roundTrip(serverAddr(server), "REWRITE mta proxy")
	if err != nil {
		t.Fatal(err)
	}

	if got != errRewriteTimeout {
		t.Errorf("reply = %q, want %q", got, errRewriteTimeout)
	}

	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("replied after %v, before the timeout", elapsed)
	}

	if !canceled.Load() {
		t.Error("timed-out request was not canceled")
	}
}

func TestRewriteDefaultTimeoutMatchesLegacy(t *testing.T) {
	if DefaultRewriteTimeout != 60*time.Second {
		t.Errorf("DefaultRewriteTimeout = %v, want 60s (listener.py REWRITE_TIMEOUT)", DefaultRewriteTimeout)
	}
}

// TestRewriteReleasedOnShutdown: a client still waiting when the server shuts
// down is answered instead of holding the shutdown for the whole timeout.
func TestRewriteReleasedOnShutdown(t *testing.T) {
	trigger := &MockActionTrigger{wait: &RewriteWait{Done: make(chan struct{})}}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{ActionTrigger: trigger})
	startServer(context.Background(), t, server)

	reply := roundTripAsync(serverAddr(server), "REWRITE mta")

	deadline := time.Now().Add(testDeadline)
	for trigger.GetRewriteCallCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("request never reached the trigger")
		}

		time.Sleep(5 * time.Millisecond)
	}

	shut := make(chan struct{})

	go func() {
		server.Shutdown(context.Background())
		close(shut)
	}()

	select {
	case <-shut:
	case <-time.After(testDeadline):
		t.Fatal("Shutdown blocked on a client waiting for the main loop")
	}

	if got := <-reply; got != errShuttingDown {
		t.Errorf("reply = %q, want %q", got, errShuttingDown)
	}
}
