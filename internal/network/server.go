// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package network provides TCP server functionality for configd's control interface.
// It implements a threaded stream server that handles STATUS and REWRITE commands
// from zmcontrol and other Carbonio management tools.
package network

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const errUnknownCommand = "ERROR UNKNOWN COMMAND"

// ActionTrigger defines the interface for triggering actions in the main application logic.
type ActionTrigger interface {
	TriggerRewrite(configs []string)
}

// RequestHandler defines the interface for handling incoming requests.
type RequestHandler interface {
	HandleRequest(ctx context.Context, command string, args []string) string
}

// ThreadedStreamServer represents a TCP server that handles connections in separate goroutines.
type ThreadedStreamServer struct {
	listener   net.Listener
	handler    RequestHandler
	addr       string
	port       int
	ipv6       bool
	shutdownCh chan struct{}
	wg         sync.WaitGroup
}

// NewThreadedStreamServer creates a new ThreadedStreamServer.
func NewThreadedStreamServer(addr string, port int, ipv6 bool, handler RequestHandler) *ThreadedStreamServer {
	return &ThreadedStreamServer{
		handler:    handler,
		addr:       addr,
		port:       port,
		ipv6:       ipv6,
		shutdownCh: make(chan struct{}),
	}
}

// ServeForever starts the TCP server and listens for incoming connections.
func (s *ThreadedStreamServer) ServeForever(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "network")

	var err error

	protocol := "tcp4"
	if s.ipv6 {
		protocol = "tcp6"
	}

	listenAddr := net.JoinHostPort(s.addr, fmt.Sprintf("%d", s.port))
	logger.InfoContext(ctx, "Starting listener",
		"protocol", protocol,
		"address", listenAddr)

	listenConfig := &net.ListenConfig{}

	s.listener, err = listenConfig.Listen(ctx, protocol, listenAddr)
	if err != nil {
		logger.ErrorContext(ctx, "Error creating listener socket",
			"port", s.port,
			"error", err)

		return err
	}

	logger.InfoContext(ctx, "Socket listener running",
		"address", listenAddr)

	go func() {
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.shutdownCh:
					logger.InfoContext(ctx, "Listener shutting down")

					return
				default:
					logger.ErrorContext(ctx, "Error accepting connection",
						"error", err)
				}

				continue
			}

			s.wg.Add(1)

			go s.handleConnection(ctx, conn)
		}
	}()

	return nil
}

// Shutdown closes the listener and waits for all active connections to finish.
func (s *ThreadedStreamServer) Shutdown(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "network")
	logger.InfoContext(ctx, "Shutting down listener",
		"address", s.addr,
		"port", s.port)
	close(s.shutdownCh)

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			logger.ErrorContext(ctx, "Error closing listener",
				"error", err)
		}
	}

	s.wg.Wait() // Wait for all goroutines to finish
	logger.InfoContext(ctx, "Listener shut down")
}

func (s *ThreadedStreamServer) handleConnection(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		if err := conn.Close(); err != nil {
			logger.ErrorContext(ctx, "Error closing connection",
				"error", err)
		}
	}()

	reader := bufio.NewReader(conn)

	message, err := reader.ReadString('\n')
	if err != nil {
		logger.ErrorContext(ctx, "Error reading from connection",
			"error", err)

		return
	}

	message = strings.TrimSpace(message)
	logger.DebugContext(ctx, "Received message",
		"message", message)

	parts := strings.Fields(message)
	if len(parts) == 0 {
		if _, err := conn.Write([]byte("ERROR UNKNOWN COMMAND\n")); err != nil {
			logger.ErrorContext(ctx, "Error writing to connection",
				"error", err)
		}

		return
	}

	command := parts[0]

	args := []string{}
	if len(parts) > 1 {
		args = parts[1:]
	}

	response := s.handler.HandleRequest(ctx, command, args)
	if _, err := conn.Write([]byte(response + "\n")); err != nil {
		logger.ErrorContext(ctx, "Error writing response to connection",
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
		return "SUCCESS ACTIVE"
	case "REWRITE":
		if h.ActionTrigger != nil {
			h.ActionTrigger.TriggerRewrite(args)
			logger.DebugContext(ctx, "Triggered REWRITE command",
				"args", args)

			return "SUCCESS REWRITES COMPLETE"
		}

		logger.ErrorContext(ctx, "ActionTrigger not set for REWRITE command")

		return "ERROR INTERNAL ERROR"
	default:
		return errUnknownCommand
	}
}
