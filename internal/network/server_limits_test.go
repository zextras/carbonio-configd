// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const testDeadline = 5 * time.Second

// serverAddr returns the bound address of a server that has been started.
func serverAddr(s *ThreadedStreamServer) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.listener.Addr().String()
}

// logRecord is one captured log entry.
type logRecord struct {
	level slog.Level
	msg   string
}

// recorder is a slog.Handler that keeps every record for assertions.
type recorder struct {
	mu   sync.Mutex
	recs []logRecord
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recs = append(r.recs, logRecord{level: rec.Level, msg: rec.Message})

	return nil
}

func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *recorder) WithGroup(string) slog.Handler { return r }

// count returns how many records at level contain substr.
func (r *recorder) count(level slog.Level, substr string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0

	for _, rec := range r.recs {
		if rec.level == level && strings.Contains(rec.msg, substr) {
			n++
		}
	}

	return n
}

// countAtLeast returns how many records are at level or above.
func (r *recorder) countAtLeast(level slog.Level) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0

	for _, rec := range r.recs {
		if rec.level >= level {
			n++
		}
	}

	return n
}

// waitFor polls until a record at level containing substr appears.
func (r *recorder) waitFor(t *testing.T, level slog.Level, substr string) {
	t.Helper()

	waitUntil(t, func() bool { return r.count(level, substr) > 0 },
		fmt.Sprintf("log record %v %q", level, substr))
}

// waitUntil polls cond until it is true or the test deadline passes.
func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()

	deadline := time.Now().Add(testDeadline)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(time.Millisecond)
	}
}

// recordingContext returns a context whose logger feeds the returned recorder.
func recordingContext() (context.Context, *recorder) {
	rec := &recorder{}

	return logger.ContextWithLogger(context.Background(), slog.New(rec)), rec
}

// countingHandler records how many requests reached the handler.
type countingHandler struct {
	calls atomic.Int32
	args  atomic.Int32
}

func (h *countingHandler) HandleRequest(_ context.Context, _ string, args []string) string {
	h.calls.Add(1)
	h.args.Store(int32(len(args))) //nolint:gosec // bounded by maxRequestArgs

	return respSuccessActive
}

// startServer starts a server and shuts it down when the test ends.
func startServer(ctx context.Context, t *testing.T, server *ThreadedStreamServer) {
	t.Helper()

	if err := server.ServeForever(ctx); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	t.Cleanup(func() { server.Shutdown(ctx) })
}

// roundTrip sends raw to addr and returns the first response line.
func roundTrip(addr, raw string) (string, error) {
	conn, err := net.DialTimeout("tcp", addr, testDeadline)
	if err != nil {
		return "", fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(testDeadline)); err != nil {
		return "", fmt.Errorf("set deadline: %w", err)
	}

	if _, err := conn.Write([]byte(raw)); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	return strings.TrimSpace(resp), nil
}

// expectDropped asserts the server closes the connection without answering
// (as opposed to timing out), i.e. the request was rejected promptly.
func expectDropped(t *testing.T, addr, raw string) {
	t.Helper()

	_, err := roundTrip(addr, raw)
	if err == nil {
		t.Fatal("expected the request to be dropped, got a response")
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("server did not close the connection promptly: %v", err)
	}
}

func TestHandleConnectionLineLimit(t *testing.T) {
	// A request line of exactly maxRequestLineBytes (newline included).
	atLimit := "STATUS" + strings.Repeat(" ", maxRequestLineBytes-len("STATUS")-1) + "\n"

	tests := []struct {
		name    string
		raw     string
		dropped bool
	}{
		{name: "at limit", raw: atLimit},
		{name: "one byte over limit", raw: "STATUS" + strings.Repeat(" ", maxRequestLineBytes-len("STATUS")) + "\n", dropped: true},
		{name: "huge line without newline", raw: strings.Repeat("A", 4*maxRequestLineBytes), dropped: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, rec := recordingContext()
			handler := &countingHandler{}
			server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
			startServer(ctx, t, server)

			if !tt.dropped {
				resp, err := roundTrip(serverAddr(server), tt.raw)
				if err != nil || resp != respSuccessActive {
					t.Fatalf("got (%q, %v), want %q", resp, err, respSuccessActive)
				}

				return
			}

			expectDropped(t, serverAddr(server), tt.raw)
			rec.waitFor(t, slog.LevelWarn, "request too long")

			if n := handler.calls.Load(); n != 0 {
				t.Errorf("handler invoked %d times for an oversized request, want 0", n)
			}
		})
	}
}

func TestHandleConnectionArgLimit(t *testing.T) {
	build := func(n int) string {
		args := make([]string, n)
		for i := range args {
			args[i] = "s"
		}

		return "REWRITE " + strings.Join(args, " ") + "\n"
	}

	t.Run("at limit", func(t *testing.T) {
		handler := &countingHandler{}
		server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
		startServer(context.Background(), t, server)

		if _, err := roundTrip(serverAddr(server), build(maxRequestArgs)); err != nil {
			t.Fatalf("request at the arg limit failed: %v", err)
		}

		if got := handler.args.Load(); got != maxRequestArgs {
			t.Errorf("handler saw %d args, want %d", got, maxRequestArgs)
		}
	})

	t.Run("over limit", func(t *testing.T) {
		ctx, rec := recordingContext()
		handler := &countingHandler{}
		server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
		startServer(ctx, t, server)

		expectDropped(t, serverAddr(server), build(maxRequestArgs+1))
		rec.waitFor(t, slog.LevelWarn, "too many request arguments")

		if n := handler.calls.Load(); n != 0 {
			t.Errorf("handler invoked %d times for an over-limit request, want 0", n)
		}
	})
}

// TestReadFailuresAreNotErrors verifies that a client hanging up or stalling is
// logged below WARN, while unexpected read failures are WARN (never ERROR).
func TestReadFailuresAreNotErrors(t *testing.T) {
	t.Run("client EOF", func(t *testing.T) {
		ctx, rec := recordingContext()
		server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{})
		startServer(ctx, t, server)

		conn, err := net.Dial("tcp", serverAddr(server))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}

		conn.Close() // hang up without sending anything

		rec.waitFor(t, slog.LevelDebug, "Connection ended")

		if n := rec.countAtLeast(slog.LevelWarn); n != 0 {
			t.Errorf("client EOF logged %d records at WARN or above, want 0", n)
		}
	})

	t.Run("read timeout", func(t *testing.T) {
		ctx, rec := recordingContext()
		server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{})
		server.readTimeout = 50 * time.Millisecond
		startServer(ctx, t, server)

		conn, err := net.Dial("tcp", serverAddr(server))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()

		rec.waitFor(t, slog.LevelDebug, "Connection ended")

		if n := rec.countAtLeast(slog.LevelWarn); n != 0 {
			t.Errorf("read timeout logged %d records at WARN or above, want 0", n)
		}
	})

	t.Run("unexpected error", func(t *testing.T) {
		ctx, rec := recordingContext()
		server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{})

		// errorConn without data fails Read with a non-EOF, non-timeout error.
		server.handleConnection(ctx, &errorConn{})

		if n := rec.count(slog.LevelWarn, "Error reading from connection"); n != 1 {
			t.Errorf("unexpected read error logged %d WARN records, want 1", n)
		}

		if n := rec.countAtLeast(slog.LevelError); n != 0 {
			t.Errorf("unexpected read error logged %d ERROR records, want 0", n)
		}
	})
}

// gateHandler blocks every request until release is closed and tracks the
// peak number of requests in flight.
type gateHandler struct {
	release  chan struct{}
	inflight atomic.Int32
	peak     atomic.Int32
}

func (h *gateHandler) HandleRequest(ctx context.Context, _ string, _ []string) string {
	n := h.inflight.Add(1)
	defer h.inflight.Add(-1)

	for {
		p := h.peak.Load()
		if n <= p || h.peak.CompareAndSwap(p, n) {
			break
		}
	}

	select {
	case <-h.release:
	case <-ctx.Done():
	}

	return respSuccessActive
}

// TestServerBoundsConcurrentConnections verifies the semaphore: with a cap of
// two, five simultaneous clients are all eventually served but never more than
// two are handled at once.
func TestServerBoundsConcurrentConnections(t *testing.T) {
	const (
		limit   = 2
		clients = 5
	)

	handler := &gateHandler{release: make(chan struct{})}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
	server.slots = make(chan struct{}, limit)
	startServer(context.Background(), t, server)

	var releaseOnce sync.Once

	releaseAll := func() { releaseOnce.Do(func() { close(handler.release) }) }
	t.Cleanup(releaseAll) // runs before Shutdown so a failure cannot hang it

	results := make(chan error, clients)

	for range clients {
		go func() {
			resp, err := roundTrip(serverAddr(server), "STATUS\n")
			if err == nil && resp != respSuccessActive {
				err = fmt.Errorf("unexpected response %q", resp)
			}

			results <- err
		}()
	}

	waitUntil(t, func() bool { return handler.inflight.Load() == limit }, "handlers to saturate the cap")

	// The remaining clients are connected and waiting; give them a bounded
	// window to (incorrectly) exceed the cap.
	window := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(window) {
		if p := handler.peak.Load(); p > limit {
			t.Fatalf("peak concurrent handlers = %d, want <= %d", p, limit)
		}

		time.Sleep(time.Millisecond)
	}

	releaseAll()

	for range clients {
		if err := <-results; err != nil {
			t.Errorf("client failed: %v", err)
		}
	}

	if p := handler.peak.Load(); p > limit {
		t.Errorf("peak concurrent handlers = %d, want <= %d", p, limit)
	}
}

// failingListener fails every Accept with err until failures are exhausted,
// then returns net.ErrClosed.
type failingListener struct {
	net.Listener
	remaining atomic.Int32
}

func (l *failingListener) Accept() (net.Conn, error) {
	if l.remaining.Add(-1) < 0 {
		return nil, net.ErrClosed
	}

	return nil, errors.New("accept failed")
}

func (l *failingListener) Close() error { return nil }

// TestAcceptFailuresAreRateLimitedWarnings verifies that accept failures are
// WARN (rate-limited), with a single ERROR once the failure is persistent and
// another when the listener is gone.
func TestAcceptFailuresAreRateLimitedWarnings(t *testing.T) {
	const failures = acceptFailuresBeforeError + 10

	ctx, rec := recordingContext()
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &countingHandler{})
	server.acceptBackoff = time.Microsecond

	listener := &failingListener{}
	listener.remaining.Store(failures)

	done := make(chan struct{})

	go func() {
		defer close(done)

		server.acceptLoop(ctx, listener)
	}()

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatal("accept loop did not stop after the listener closed")
	}

	if n := rec.count(slog.LevelError, "persistently failing"); n != 1 {
		t.Errorf("persistent failure logged %d ERROR records, want 1", n)
	}

	if n := rec.count(slog.LevelError, "closed unexpectedly"); n != 1 {
		t.Errorf("closed listener logged %d ERROR records, want 1", n)
	}

	if n := rec.count(slog.LevelError, "Error accepting connection"); n != 0 {
		t.Errorf("transient accept failures logged %d ERROR records, want 0", n)
	}

	warns := rec.count(slog.LevelWarn, "Error accepting connection")
	if warns == 0 || warns >= failures/2 {
		t.Errorf("accept failures logged %d WARN records for %d failures, want rate-limited", warns, failures)
	}
}

// TestAcceptBackoffStopsOnContextCancel verifies the backoff after a failed
// Accept does not outlive ctx (a plain sleep would keep retrying forever).
func TestAcceptBackoffStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewThreadedStreamServer("127.0.0.1", 0, false, &countingHandler{})
	server.acceptBackoff = time.Hour

	listener := &failingListener{}
	listener.remaining.Store(1 << 30)

	done := make(chan struct{})

	go func() {
		defer close(done)

		server.acceptLoop(ctx, listener)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatal("accept loop did not stop on context cancellation during backoff")
	}
}

// TestShutdownInterruptsAcceptBackoff verifies Shutdown does not wait out a
// long backoff.
func TestShutdownInterruptsAcceptBackoff(t *testing.T) {
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &countingHandler{})
	server.acceptBackoff = time.Hour

	listener := &failingListener{}
	listener.remaining.Store(1 << 30)

	server.mu.Lock()
	server.listener = listener
	server.wg.Go(func() { server.acceptLoop(context.Background(), listener) })
	server.mu.Unlock()

	done := make(chan struct{})

	go func() {
		defer close(done)

		server.Shutdown(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(testDeadline):
		t.Fatal("Shutdown blocked on the accept backoff")
	}
}

// TestServeForeverShutdownRace exercises ServeForever against Shutdown from
// different goroutines; run with -race.
func TestServeForeverShutdownRace(t *testing.T) {
	for range 50 {
		server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{})

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			if err := server.ServeForever(context.Background()); err != nil && !errors.Is(err, errServerClosed) {
				t.Errorf("ServeForever: unexpected error %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			server.Shutdown(context.Background())
		}()

		wg.Wait()
	}
}

func TestServeForeverLifecycleErrors(t *testing.T) {
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{})
	startServer(context.Background(), t, server)

	if err := server.ServeForever(context.Background()); !errors.Is(err, errAlreadyServing) {
		t.Errorf("second ServeForever: got %v, want %v", err, errAlreadyServing)
	}

	server.Shutdown(context.Background())
	server.Shutdown(context.Background()) // idempotent

	if err := server.ServeForever(context.Background()); !errors.Is(err, errServerClosed) {
		t.Errorf("ServeForever after Shutdown: got %v, want %v", err, errServerClosed)
	}
}

// TestRequestWithoutNewline mirrors the legacy single recv(2048): the legacy
// configd.py client sends "REWRITE a b" with no newline and without
// half-closing, then waits for the reply.
func TestRequestWithoutNewline(t *testing.T) {
	trigger := &MockActionTrigger{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{ActionTrigger: trigger})
	startServer(context.Background(), t, server)

	start := time.Now()

	resp, err := roundTrip(serverAddr(server), "REWRITE mta")
	if err != nil {
		t.Fatalf("request without newline failed: %v", err)
	}

	if resp != respRewritesComplete {
		t.Errorf("response = %q, want %q", resp, respRewritesComplete)
	}

	// The default read timeout is minutes; a prompt reply proves the server
	// did not wait for a newline.
	if elapsed := time.Since(start); elapsed > testDeadline/2 {
		t.Errorf("reply took %v, want it to be prompt", elapsed)
	}

	if got := trigger.GetLastConfigs(); len(got) != 1 || got[0] != "mta" {
		t.Errorf("TriggerRewrite configs = %v, want [mta]", got)
	}
}

// TestRequestFraming covers how a request is cut out of the first read.
func TestRequestFraming(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "newline terminated", raw: "STATUS\n", want: respSuccessActive},
		{name: "CRLF terminated", raw: "STATUS\r\n", want: respSuccessActive},
		{name: "no newline", raw: "STATUS", want: respSuccessActive},
		{name: "bytes after newline ignored", raw: "STATUS\nREWRITE mta\n", want: respSuccessActive},
		{name: "empty line", raw: "\n", want: errUnknownCommand},
		{name: "whitespace only without newline", raw: "   ", want: errUnknownCommand},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger := &MockActionTrigger{}
			server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{ActionTrigger: trigger})
			startServer(context.Background(), t, server)

			resp, err := roundTrip(serverAddr(server), tt.raw)
			if err != nil || resp != tt.want {
				t.Fatalf("got (%q, %v), want %q", resp, err, tt.want)
			}

			if n := trigger.GetRewriteCallCount(); n != 0 {
				t.Errorf("TriggerRewrite called %d times, want 0", n)
			}
		})
	}
}

// TestRequestHalfClose verifies a client that sends a request without newline
// and half-closes (e.g. nc -N) is answered.
func TestRequestHalfClose(t *testing.T) {
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{})
	startServer(context.Background(), t, server)

	conn, err := net.Dial("tcp", serverAddr(server))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(testDeadline)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	if _, err := conn.Write([]byte("STATUS")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}

	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || strings.TrimSpace(resp) != respSuccessActive {
		t.Fatalf("got (%q, %v), want %q", resp, err, respSuccessActive)
	}
}

// TestHandleConnectionUnterminatedRequest checks the framing on a fake conn
// that returns the request and then fails.
func TestHandleConnectionUnterminatedRequest(t *testing.T) {
	server := NewThreadedStreamServer("127.0.0.1", 0, false, &ConfigdRequestHandler{})
	conn := &errorConn{readData: "STATUS"}

	server.handleConnection(context.Background(), conn)

	if got := conn.written.String(); got != respSuccessActive+"\n" {
		t.Errorf("response = %q, want %q", got, respSuccessActive+"\n")
	}
}
