// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package network provides TCP server functionality for configd's control interface.
// It implements a threaded stream server that handles STATUS and REWRITE commands
// from zmcontrol and other Carbonio management tools.
package network

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	errUnknownCommand    = "ERROR UNKNOWN COMMAND"
	respSuccessActive    = "SUCCESS ACTIVE"
	respRewritesComplete = "SUCCESS REWRITES COMPLETE"
	errNoServicesListed  = "ERROR NO SERVICES LISTED"
	acceptErrorBackoff   = 100 * time.Millisecond

	// defaultConnReadTimeout bounds how long a single connection may take to
	// send its command line. Without it, a stalled client holds a goroutine
	// open and blocks graceful shutdown (which waits on all connection goroutines).
	defaultConnReadTimeout = 120 * time.Second

	// maxRequestLineBytes is the size of the single read that carries a request.
	// The legacy listener (jylibs/listener.py) reads a single recv(2048), so every
	// request it could ever act on fits in this bound with room to spare.
	maxRequestLineBytes = 4096

	// maxRequestArgs caps the number of arguments after the command. A REWRITE
	// lists configd service names; legitimate callers send a handful.
	maxRequestArgs = 256

	// maxConcurrentConns bounds simultaneously handled connections. The legacy
	// ThreadingMixIn server is unbounded; the control interface is loopback-only
	// and requests are tiny, so a small fixed bound is ample. Excess clients
	// wait in the kernel accept backlog.
	maxConcurrentConns = 64

	// acceptFailuresBeforeError is the number of consecutive accept failures
	// (~5s at acceptErrorBackoff) after which the listener is considered
	// persistently broken and an ERROR is logged once.
	acceptFailuresBeforeError = 50

	// acceptWarnEvery rate-limits the per-failure WARN to the first failure
	// and then every Nth consecutive one.
	acceptWarnEvery = 10
)

var (
	errServerClosed   = errors.New("server is shut down")
	errAlreadyServing = errors.New("server is already serving")
)

// ActionTrigger defines the interface for triggering actions in the main application logic.
type ActionTrigger interface {
	TriggerRewrite(ctx context.Context, configs []string)
}

// RequestHandler defines the interface for handling incoming requests.
type RequestHandler interface {
	HandleRequest(ctx context.Context, command string, args []string) string
}

// ThreadedStreamServer represents a TCP server that handles connections in separate goroutines.
type ThreadedStreamServer struct {
	handler     RequestHandler
	addr        string
	port        int
	ipv6        bool
	readTimeout time.Duration
	// acceptBackoff is the pause after a failed Accept.
	acceptBackoff time.Duration
	shutdownCh    chan struct{}
	// slots is a counting semaphore bounding concurrently handled connections.
	slots chan struct{}
	// wg tracks the accept loop and every connection goroutine. Connection
	// goroutines are only started from the accept loop, which itself holds a
	// count, so wg.Go never races with the wg.Wait in Shutdown.
	wg sync.WaitGroup

	// mu guards listener and closed, ordering ServeForever against Shutdown.
	mu       sync.Mutex
	listener net.Listener
	closed   bool
}

// NewThreadedStreamServer creates a new ThreadedStreamServer.
func NewThreadedStreamServer(addr string, port int, ipv6 bool, handler RequestHandler) *ThreadedStreamServer {
	return &ThreadedStreamServer{
		handler:       handler,
		addr:          addr,
		port:          port,
		ipv6:          ipv6,
		readTimeout:   defaultConnReadTimeout,
		acceptBackoff: acceptErrorBackoff,
		shutdownCh:    make(chan struct{}),
		slots:         make(chan struct{}, maxConcurrentConns),
	}
}

// ServeForever starts the TCP server and listens for incoming connections.
// The listener is bound before it returns; connections are accepted in the
// background until Shutdown is called or ctx is cancelled.
func (s *ThreadedStreamServer) ServeForever(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "network")

	protocol := "tcp4"
	if s.ipv6 {
		protocol = "tcp6"
	}

	listenAddr := net.JoinHostPort(s.addr, fmt.Sprintf("%d", s.port))

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errServerClosed
	}

	if s.listener != nil {
		return errAlreadyServing
	}

	logger.InfoContext(ctx, "Starting listener",
		"protocol", protocol,
		"address", listenAddr)

	listenConfig := &net.ListenConfig{}

	listener, err := listenConfig.Listen(ctx, protocol, listenAddr)
	if err != nil {
		logger.ErrorContext(ctx, "Error creating listener socket",
			"port", s.port,
			"error", err)

		return fmt.Errorf("listen on %s: %w", listenAddr, err)
	}

	s.listener = listener

	logger.InfoContext(ctx, "Socket listener running",
		"address", listenAddr)

	s.wg.Go(func() {
		s.acceptLoop(ctx, listener)
	})

	return nil
}

// acceptLoop accepts connections until shutdown, ctx cancellation, or a
// permanent listener failure.
func (s *ThreadedStreamServer) acceptLoop(ctx context.Context, listener net.Listener) {
	failures := 0

	for {
		if !s.acquireSlot(ctx) {
			logger.InfoContext(ctx, "Listener shutting down")

			return
		}

		conn, err := listener.Accept()
		if err != nil {
			s.releaseSlot()

			failures++

			if !s.handleAcceptError(ctx, err, failures) {
				return
			}

			continue
		}

		failures = 0

		s.wg.Go(func() {
			defer s.releaseSlot()

			s.handleConnection(ctx, conn)
		})
	}
}

// acquireSlot blocks until a connection slot is free. It reports false when
// the server is shutting down or ctx is done.
func (s *ThreadedStreamServer) acquireSlot(ctx context.Context) bool {
	select {
	case s.slots <- struct{}{}:
		return true
	case <-s.shutdownCh:
		return false
	case <-ctx.Done():
		return false
	}
}

func (s *ThreadedStreamServer) releaseSlot() {
	<-s.slots
}

// handleAcceptError logs an Accept failure and backs off. It reports whether
// the accept loop should keep going. The backoff is interrupted by shutdown
// or ctx cancellation.
func (s *ThreadedStreamServer) handleAcceptError(ctx context.Context, err error, failures int) bool {
	select {
	case <-s.shutdownCh:
		logger.InfoContext(ctx, "Listener shutting down")

		return false
	default:
	}

	if errors.Is(err, net.ErrClosed) {
		logger.ErrorContext(ctx, "Listener closed unexpectedly, no longer accepting connections",
			"error", err)

		return false
	}

	switch {
	case failures == acceptFailuresBeforeError:
		logger.ErrorContext(ctx, "Listener persistently failing to accept connections",
			"consecutive_failures", failures,
			"error", err)
	case failures == 1 || failures%acceptWarnEvery == 0:
		logger.WarnContext(ctx, "Error accepting connection",
			"consecutive_failures", failures,
			"error", err)
	}

	timer := time.NewTimer(s.acceptBackoff)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-s.shutdownCh:
		return false
	case <-ctx.Done():
		return false
	}
}

// Shutdown closes the listener and waits for all active connections to finish.
// It is safe to call more than once and before ServeForever.
func (s *ThreadedStreamServer) Shutdown(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "network")
	logger.InfoContext(ctx, "Shutting down listener",
		"address", s.addr,
		"port", s.port)

	s.mu.Lock()

	if !s.closed {
		s.closed = true

		close(s.shutdownCh)

		if s.listener != nil {
			if err := s.listener.Close(); err != nil {
				logger.ErrorContext(ctx, "Error closing listener",
					"error", err)
			}
		}
	}

	s.mu.Unlock()

	s.wg.Wait() // Wait for the accept loop and all connection goroutines
	logger.InfoContext(ctx, "Listener shut down")
}

// logReadError reports a failure to read a request. A client that goes away or
// stalls is routine and logged at DEBUG; anything else is unexpected.
func logReadError(ctx context.Context, err error) {
	var netErr net.Error

	if errors.Is(err, io.EOF) || (errors.As(err, &netErr) && netErr.Timeout()) {
		logger.DebugContext(ctx, "Connection ended before a request was read",
			"error", err)

		return
	}

	logger.WarnContext(ctx, "Error reading from connection",
		"error", err)
}

// readRequest mirrors the legacy listener's single recv(2048) (jylibs/listener.py):
// one read decides the request. The request is the bytes up to the first '\n',
// or the whole chunk when the client sent no newline (the legacy configd.py
// client sends none and does not half-close). A chunk that fills the buffer
// without a newline is oversized and rejected.
func readRequest(ctx context.Context, conn net.Conn) (string, bool) {
	buf := make([]byte, maxRequestLineBytes)

	n, err := conn.Read(buf)
	if n == 0 {
		if err == nil {
			err = io.ErrNoProgress
		}

		logReadError(ctx, err)

		return "", false
	}

	chunk := buf[:n]

	if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
		chunk = chunk[:i]
	} else if n == len(buf) {
		logger.WarnContext(ctx, "Dropping connection: request too long",
			"max_bytes", maxRequestLineBytes)

		return "", false
	}

	return string(chunk), true
}

func (s *ThreadedStreamServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			logger.ErrorContext(ctx, "Error closing connection",
				"error", err)
		}
	}()

	if err := conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
		logger.ErrorContext(ctx, "Error setting read deadline",
			"error", err)

		return
	}

	request, ok := readRequest(ctx, conn)
	if !ok {
		return
	}

	message := strings.TrimSpace(request)
	logger.DebugContext(ctx, "Received message",
		"message", message)

	parts := strings.Fields(message)
	if len(parts) == 0 {
		s.writeResponse(ctx, conn, errUnknownCommand)

		return
	}

	if len(parts)-1 > maxRequestArgs {
		logger.WarnContext(ctx, "Dropping connection: too many request arguments",
			"args", len(parts)-1,
			"max_args", maxRequestArgs)

		return
	}

	response := s.handler.HandleRequest(ctx, parts[0], parts[1:])
	s.writeResponse(ctx, conn, response)
}

func (s *ThreadedStreamServer) writeResponse(ctx context.Context, conn net.Conn, response string) {
	if _, err := conn.Write([]byte(response + "\n")); err != nil {
		logger.WarnContext(ctx, "Error writing response to connection",
			"error", err)
	}
}

// ConfigdRequestHandler implements the RequestHandler interface for configd.
type ConfigdRequestHandler struct {
	ActionTrigger ActionTrigger
}

// HandleRequest processes incoming commands.
func (h *ConfigdRequestHandler) HandleRequest(ctx context.Context, command string, args []string) string {
	ctx = logger.ContextWithComponentOnce(ctx, "network")
	logger.DebugContext(ctx, "ConfigdRequestHandler received command",
		"command", command,
		"args", args)

	switch command {
	case "STATUS":
		return respSuccessActive
	case "REWRITE":
		// Mirror jylibs/listener.py:31-32: REWRITE with no service arguments is
		// rejected rather than treated as a no-op success.
		if len(args) == 0 {
			return errNoServicesListed
		}

		if h.ActionTrigger != nil {
			h.ActionTrigger.TriggerRewrite(ctx, args)
			logger.DebugContext(ctx, "Triggered REWRITE command",
				"args", args)

			return respRewritesComplete
		}

		logger.ErrorContext(ctx, "ActionTrigger not set for REWRITE command")

		return "ERROR INTERNAL ERROR"
	default:
		return errUnknownCommand
	}
}
