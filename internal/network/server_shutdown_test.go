// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"context"
	"net"
	"testing"
	"time"
)

type panicHandler struct{}

func (panicHandler) HandleRequest(context.Context, string, []string) string { panic("boom") }

func TestShutdownDoesNotWaitForIdleConnection(t *testing.T) {
	t.Parallel()

	server := NewThreadedStreamServer("127.0.0.1", 0, false, &countingHandler{})
	server.readTimeout = time.Hour

	if err := server.ServeForever(t.Context()); err != nil {
		t.Fatal(err)
	}

	conn, err := net.DialTimeout("tcp", serverAddr(server), testDeadline)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Let the server accept the connection and block reading it.
	time.Sleep(100 * time.Millisecond)

	done := make(chan struct{})

	go func() {
		defer close(done)

		server.Shutdown(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatal("Shutdown blocked on an idle connection")
	}
}

func TestHandlerPanicDoesNotKillServer(t *testing.T) {
	t.Parallel()

	server := NewThreadedStreamServer("127.0.0.1", 0, false, panicHandler{})
	startServer(t.Context(), t, server)

	// The panicking request just loses its connection; the process survives
	// and the server keeps accepting.
	for range 2 {
		if _, err := roundTrip(serverAddr(server), "STATUS\n"); err == nil {
			t.Fatal("expected connection closed without response")
		}
	}
}
