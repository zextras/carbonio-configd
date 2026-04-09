// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package network

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// MockActionTrigger is a mock implementation of ActionTrigger for testing
type MockActionTrigger struct {
	mu               sync.Mutex
	rewriteCallCount int
	lastConfigs      []string
}

func (m *MockActionTrigger) TriggerRewrite(configs []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rewriteCallCount++
	m.lastConfigs = make([]string, len(configs))
	copy(m.lastConfigs, configs)
}

func (m *MockActionTrigger) GetRewriteCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rewriteCallCount
}

func (m *MockActionTrigger) GetLastConfigs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.lastConfigs))
	copy(result, m.lastConfigs)
	return result
}

// TestConfigdRequestHandlerStatus tests the STATUS command
func TestConfigdRequestHandlerStatus(t *testing.T) {
	handler := &ConfigdRequestHandler{}

	response := handler.HandleRequest(context.Background(), "STATUS", []string{})
	expected := "SUCCESS ACTIVE"

	if response != expected {
		t.Errorf("STATUS command: got %q, want %q", response, expected)
	}
}

// TestConfigdRequestHandlerRewrite tests the REWRITE command
func TestConfigdRequestHandlerRewrite(t *testing.T) {
	mockTrigger := &MockActionTrigger{}
	handler := &ConfigdRequestHandler{
		ActionTrigger: mockTrigger,
	}

	tests := []struct {
		name             string
		args             []string
		expectedResponse string
	}{
		{
			name:             "rewrite with no args",
			args:             []string{},
			expectedResponse: "SUCCESS REWRITES COMPLETE",
		},
		{
			name:             "rewrite with single config",
			args:             []string{"dhparam"},
			expectedResponse: "SUCCESS REWRITES COMPLETE",
		},
		{
			name:             "rewrite with multiple configs",
			args:             []string{"amavis", "antivirus", "mta"},
			expectedResponse: "SUCCESS REWRITES COMPLETE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock state
			mockTrigger.mu.Lock()
			mockTrigger.rewriteCallCount = 0
			mockTrigger.lastConfigs = nil
			mockTrigger.mu.Unlock()

			response := handler.HandleRequest(context.Background(), "REWRITE", tt.args)

			if response != tt.expectedResponse {
				t.Errorf("REWRITE response: got %q, want %q", response, tt.expectedResponse)
			}

			if mockTrigger.GetRewriteCallCount() != 1 {
				t.Errorf("TriggerRewrite call count: got %d, want 1", mockTrigger.GetRewriteCallCount())
			}

			lastConfigs := mockTrigger.GetLastConfigs()
			if len(lastConfigs) != len(tt.args) {
				t.Errorf("lastConfigs length: got %d, want %d", len(lastConfigs), len(tt.args))
			}

			for i, arg := range tt.args {
				if i < len(lastConfigs) && lastConfigs[i] != arg {
					t.Errorf("lastConfigs[%d]: got %q, want %q", i, lastConfigs[i], arg)
				}
			}
		})
	}
}

// TestConfigdRequestHandlerUnknownCommand tests unknown commands
func TestConfigdRequestHandlerUnknownCommand(t *testing.T) {
	handler := &ConfigdRequestHandler{}

	tests := []struct {
		name     string
		command  string
		args     []string
		expected string
	}{
		{
			name:     "unknown command",
			command:  "UNKNOWN",
			args:     []string{},
			expected: "ERROR UNKNOWN COMMAND",
		},
		{
			name:     "invalid command",
			command:  "INVALID",
			args:     []string{"arg1", "arg2"},
			expected: "ERROR UNKNOWN COMMAND",
		},
		{
			name:     "empty command",
			command:  "",
			args:     []string{},
			expected: "ERROR UNKNOWN COMMAND",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.HandleRequest(context.Background(), tt.command, tt.args)
			if response != tt.expected {
				t.Errorf("response: got %q, want %q", response, tt.expected)
			}
		})
	}
}

// TestThreadedStreamServerStartStop tests server lifecycle
func TestThreadedStreamServerStartStop(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler) // port 0 = random available port

	// Start server
	err := server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is listening
	if server.listener == nil {
		t.Fatal("Server listener is nil after ServeForever")
	}

	// Shutdown server
	server.Shutdown(context.Background())

	// Give server time to shutdown
	time.Sleep(100 * time.Millisecond)
}

// TestThreadedStreamServerStatusCommand tests STATUS command over network
func TestThreadedStreamServerStatusCommand(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)

	err := server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Shutdown(context.Background())

	// Get actual port
	addr := server.listener.Addr().String()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Connect to server
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	// Send STATUS command
	_, err = conn.Write([]byte("STATUS\n"))
	if err != nil {
		t.Fatalf("Failed to send STATUS command: %v", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	response = strings.TrimSpace(response)
	expected := "SUCCESS ACTIVE"

	if response != expected {
		t.Errorf("STATUS response: got %q, want %q", response, expected)
	}
}

// TestThreadedStreamServerRewriteCommand tests REWRITE command over network
func TestThreadedStreamServerRewriteCommand(t *testing.T) {
	mockTrigger := &MockActionTrigger{}
	handler := &ConfigdRequestHandler{
		ActionTrigger: mockTrigger,
	}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)

	err := server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Shutdown(context.Background())

	addr := server.listener.Addr().String()
	time.Sleep(100 * time.Millisecond)

	tests := []struct {
		name         string
		command      string
		expectedResp string
		expectedArgs []string
	}{
		{
			name:         "rewrite no args",
			command:      "REWRITE",
			expectedResp: "SUCCESS REWRITES COMPLETE",
			expectedArgs: []string{},
		},
		{
			name:         "rewrite single arg",
			command:      "REWRITE dhparam",
			expectedResp: "SUCCESS REWRITES COMPLETE",
			expectedArgs: []string{"dhparam"},
		},
		{
			name:         "rewrite multiple args",
			command:      "REWRITE amavis antivirus mta",
			expectedResp: "SUCCESS REWRITES COMPLETE",
			expectedArgs: []string{"amavis", "antivirus", "mta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock state
			mockTrigger.mu.Lock()
			mockTrigger.rewriteCallCount = 0
			mockTrigger.lastConfigs = nil
			mockTrigger.mu.Unlock()

			// Connect to server
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatalf("Failed to connect to server: %v", err)
			}
			defer conn.Close()

			// Send command
			_, err = conn.Write([]byte(tt.command + "\n"))
			if err != nil {
				t.Fatalf("Failed to send command: %v", err)
			}

			// Read response
			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			response = strings.TrimSpace(response)
			if response != tt.expectedResp {
				t.Errorf("response: got %q, want %q", response, tt.expectedResp)
			}

			// Verify trigger was called
			if mockTrigger.GetRewriteCallCount() != 1 {
				t.Errorf("TriggerRewrite call count: got %d, want 1", mockTrigger.GetRewriteCallCount())
			}

			// Verify args
			lastConfigs := mockTrigger.GetLastConfigs()
			if len(lastConfigs) != len(tt.expectedArgs) {
				t.Errorf("args length: got %d, want %d", len(lastConfigs), len(tt.expectedArgs))
			}

			for i, expectedArg := range tt.expectedArgs {
				if i < len(lastConfigs) && lastConfigs[i] != expectedArg {
					t.Errorf("args[%d]: got %q, want %q", i, lastConfigs[i], expectedArg)
				}
			}
		})
	}
}

// TestThreadedStreamServerUnknownCommand tests unknown command over network
func TestThreadedStreamServerUnknownCommand(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)

	err := server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Shutdown(context.Background())

	addr := server.listener.Addr().String()
	time.Sleep(100 * time.Millisecond)

	// Connect to server
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	// Send unknown command
	_, err = conn.Write([]byte("UNKNOWN\n"))
	if err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	response = strings.TrimSpace(response)
	expected := "ERROR UNKNOWN COMMAND"

	if response != expected {
		t.Errorf("response: got %q, want %q", response, expected)
	}
}

// TestThreadedStreamServerConcurrentConnections tests multiple concurrent connections
func TestThreadedStreamServerConcurrentConnections(t *testing.T) {
	mockTrigger := &MockActionTrigger{}
	handler := &ConfigdRequestHandler{
		ActionTrigger: mockTrigger,
	}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)

	err := server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Shutdown(context.Background())

	addr := server.listener.Addr().String()
	time.Sleep(100 * time.Millisecond)

	// Number of concurrent connections
	numConnections := 10
	var wg sync.WaitGroup
	wg.Add(numConnections)

	// Track errors
	errors := make(chan error, numConnections)

	// Launch concurrent connections
	for i := range numConnections {
		go func(id int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", addr)
			if err != nil {
				errors <- err
				return
			}
			defer conn.Close()

			// Alternate between STATUS and REWRITE commands
			var command string
			if id%2 == 0 {
				command = "STATUS\n"
			} else {
				command = "REWRITE test\n"
			}

			_, err = conn.Write([]byte(command))
			if err != nil {
				errors <- err
				return
			}

			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				errors <- err
				return
			}

			response = strings.TrimSpace(response)
			if !strings.HasPrefix(response, "SUCCESS") {
				errors <- err
				return
			}
		}(i)
	}

	// Wait for all connections to complete
	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent connection error: %v", err)
	}

	// Verify REWRITE was called for half the connections
	expectedCalls := numConnections / 2
	actualCalls := mockTrigger.GetRewriteCallCount()
	if actualCalls != expectedCalls {
		t.Errorf("TriggerRewrite call count: got %d, want %d", actualCalls, expectedCalls)
	}
}

// TestThreadedStreamServerIPv6 tests IPv6 support
func TestThreadedStreamServerIPv6(t *testing.T) {
	// Check if IPv6 is available
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 not available on this system")
	}
	listener.Close()

	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("::1", 0, true, handler)

	err = server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start IPv6 server: %v", err)
	}
	defer server.Shutdown(context.Background())

	addr := server.listener.Addr().String()
	time.Sleep(100 * time.Millisecond)

	// Connect to IPv6 server
	conn, err := net.Dial("tcp6", addr)
	if err != nil {
		t.Fatalf("Failed to connect to IPv6 server: %v", err)
	}
	defer conn.Close()

	// Send STATUS command
	_, err = conn.Write([]byte("STATUS\n"))
	if err != nil {
		t.Fatalf("Failed to send STATUS command: %v", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	response = strings.TrimSpace(response)
	expected := "SUCCESS ACTIVE"

	if response != expected {
		t.Errorf("STATUS response: got %q, want %q", response, expected)
	}
}

// TestConfigdRequestHandlerRewriteNoTrigger tests REWRITE command when ActionTrigger is nil (covers server.go:195-197)
func TestConfigdRequestHandlerRewriteNoTrigger(t *testing.T) {
	handler := &ConfigdRequestHandler{ActionTrigger: nil}

	response := handler.HandleRequest(context.Background(), "REWRITE", []string{"cfg"})
	expected := "ERROR INTERNAL ERROR"

	if response != expected {
		t.Errorf("REWRITE with nil trigger: got %q, want %q", response, expected)
	}
}

// TestServeForeverInvalidAddress tests that ServeForever returns an error for an invalid address (covers server.go:68-74)
func TestServeForeverInvalidAddress(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	// Use an invalid address that cannot be bound
	server := NewThreadedStreamServer("999.999.999.999", 9999, false, handler)

	err := server.ServeForever(context.Background())
	if err == nil {
		server.Shutdown(context.Background())
		t.Fatal("expected error from ServeForever with invalid address, got nil")
	}
}

// TestShutdownWithNilListener tests Shutdown when listener is nil (covers server.go:113 nil guard)
func TestShutdownWithNilListener(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
	// Never call ServeForever, so listener stays nil
	server.Shutdown(context.Background()) // must not panic
}

// errorConn is a net.Conn whose Write always returns an error.
type errorConn struct {
	net.Conn
	readData  string
	readDone  bool
	writeFail bool
	closeFail bool
}

func (e *errorConn) Read(b []byte) (int, error) {
	if !e.readDone && len(e.readData) > 0 {
		n := copy(b, e.readData)
		e.readDone = true
		return n, nil
	}
	// Signal EOF so ReadString returns
	return 0, fmt.Errorf("EOF")
}

func (e *errorConn) Write(b []byte) (int, error) {
	if e.writeFail {
		return 0, fmt.Errorf("write error")
	}
	return len(b), nil
}

func (e *errorConn) Close() error {
	if e.closeFail {
		return fmt.Errorf("close error")
	}
	return nil
}

// TestHandleConnectionReadError tests the read error path in handleConnection (covers server.go:136-141)
// Triggered by a connection that is closed before sending a newline-terminated line.
func TestHandleConnectionReadError(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)

	err := server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Shutdown(context.Background())

	addr := server.listener.Addr().String()
	time.Sleep(50 * time.Millisecond)

	// Connect and immediately close without sending a newline — ReadString will get EOF
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	// Send partial data (no newline) then close
	_, _ = conn.Write([]byte("STAT"))
	conn.Close()

	// Give the server goroutine time to handle the error path
	time.Sleep(100 * time.Millisecond)
}

// TestHandleConnectionWriteErrorOnEmptyCommand tests write error on the empty-command path (covers server.go:149-152)
func TestHandleConnectionWriteErrorOnEmptyCommand(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
	server.wg.Add(1)

	conn := &errorConn{
		readData:  "\n", // empty line → parts will be empty
		writeFail: true, // Write will fail after ReadString succeeds
	}

	// handleConnection runs synchronously (wg.Done called inside)
	server.handleConnection(context.Background(), conn)
}

// TestHandleConnectionWriteErrorOnResponse tests write error on the normal response path (covers server.go:165-168)
func TestHandleConnectionWriteErrorOnResponse(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
	server.wg.Add(1)

	conn := &errorConn{
		readData:  "STATUS\n",
		writeFail: true, // Write will fail when sending response
	}

	server.handleConnection(context.Background(), conn)
}

// TestHandleConnectionCloseError tests the conn.Close() error path in handleConnection (covers server.go:127-130)
func TestHandleConnectionCloseError(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)
	server.wg.Add(1)

	conn := &errorConn{
		readData:  "STATUS\n",
		writeFail: false,
		closeFail: true, // Close will return an error
	}

	server.handleConnection(context.Background(), conn)
}

// TestThreadedStreamServerEmptyCommand tests handling of empty commands
func TestThreadedStreamServerEmptyCommand(t *testing.T) {
	handler := &ConfigdRequestHandler{}
	server := NewThreadedStreamServer("127.0.0.1", 0, false, handler)

	err := server.ServeForever(context.Background())
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Shutdown(context.Background())

	addr := server.listener.Addr().String()
	time.Sleep(100 * time.Millisecond)

	// Connect to server
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	// Send empty line
	_, err = conn.Write([]byte("\n"))
	if err != nil {
		t.Fatalf("Failed to send empty command: %v", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	response = strings.TrimSpace(response)
	expected := "ERROR UNKNOWN COMMAND"

	if response != expected {
		t.Errorf("response: got %q, want %q", response, expected)
	}
}
