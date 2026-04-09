// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestContactService_ServiceUnavailable(t *testing.T) {
	// Try to connect to a port that's not listening
	unavailablePort := 19999
	result := ContactService("status", []string{}, unavailablePort, "ipv4")

	if !result {
		t.Error("Expected ContactService to return true when service is unavailable")
	}
}

func TestContactService_SuccessfulConnection(t *testing.T) {
	// Start a mock server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	// Handle one connection
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the command
		buffer := make([]byte, 2048)
		_, err = conn.Read(buffer)
		if err != nil {
			return
		}

		// Send OK response
		_, _ = conn.Write([]byte("OK: Command received\n"))
	}()

	result := ContactService("status", []string{}, port, "ipv4")

	if result {
		t.Error("Expected ContactService to return false on successful connection")
	}
}

func TestContactService_WithArguments(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	receivedCommand := make(chan string, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buffer := make([]byte, 2048)
		n, err := conn.Read(buffer)
		if err != nil {
			return
		}

		receivedCommand <- string(buffer[:n])
		_, _ = conn.Write([]byte("OK\n"))
	}()

	result := ContactService("reload", []string{"mta", "proxy"}, port, "ipv4")

	if result {
		t.Error("Expected ContactService to return false on successful connection")
	}

	select {
	case cmd := <-receivedCommand:
		cmd = strings.TrimSpace(cmd)
		expected := "reload mta proxy"
		if cmd != expected {
			t.Errorf("Expected command '%s', got '%s'", expected, cmd)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for command")
	}
}

func TestContactService_ErrorResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buffer := make([]byte, 2048)
		_, err = conn.Read(buffer)
		if err != nil {
			return
		}

		// Send ERROR response
		_, _ = conn.Write([]byte("ERROR: Invalid command\n"))
	}()

	result := ContactService("invalid", []string{}, port, "ipv4")

	if !result {
		t.Error("Expected ContactService to return true when service returns ERROR")
	}
}

func TestContactService_ServerClosesConnectionImmediately(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Close immediately without reading or writing
		conn.Close()
	}()

	result := ContactService("status", []string{}, port, "ipv4")

	if !result {
		t.Error("Expected ContactService to return true when connection is closed immediately")
	}
}

func TestContactService_IPv6(t *testing.T) {
	// Check if IPv6 is available
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 not available on this system")
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buffer := make([]byte, 2048)
		_, err = conn.Read(buffer)
		if err != nil {
			return
		}

		_, _ = conn.Write([]byte("OK\n"))
	}()

	// Use IPv6 mode - this will connect to [::1]:port
	result := ContactService("status", []string{}, port, "ipv6")

	if result {
		t.Error("Expected ContactService to return false on successful IPv6 connection")
	}
}

func TestContactService_NoResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	// Server that accepts, reads, then closes without responding
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Read the request
		buffer := make([]byte, 2048)
		_, _ = conn.Read(buffer)
		// Close without sending response
		conn.Close()
	}()

	result := ContactService("status", []string{}, port, "ipv4")

	if !result {
		t.Error("Expected ContactService to return true when server closes without responding")
	}
}

func TestContactService_EmptyCommand(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	receivedCommand := make(chan string, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buffer := make([]byte, 2048)
		n, err := conn.Read(buffer)
		if err != nil {
			return
		}

		receivedCommand <- string(buffer[:n])
		_, _ = conn.Write([]byte("OK\n"))
	}()

	result := ContactService("", []string{}, port, "ipv4")

	if result {
		t.Error("Expected ContactService to return false on successful connection")
	}

	select {
	case cmd := <-receivedCommand:
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			t.Errorf("Expected empty command, got '%s'", cmd)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for command")
	}
}

func TestContactService_MultipleCommands(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	commands := []struct {
		command string
		args    []string
		want    string
	}{
		{"status", []string{}, "status"},
		{"reload", []string{"mta"}, "reload mta"},
		{"restart", []string{"proxy", "ldap"}, "restart proxy ldap"},
	}

	for _, tc := range commands {
		t.Run(fmt.Sprintf("%s_%v", tc.command, tc.args), func(t *testing.T) {
			receivedCommand := make(chan string, 1)

			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()

				buffer := make([]byte, 2048)
				n, err := conn.Read(buffer)
				if err != nil {
					return
				}

				receivedCommand <- string(buffer[:n])
				_, _ = conn.Write([]byte("OK\n"))
			}()

			result := ContactService(tc.command, tc.args, port, "ipv4")

			if result {
				t.Error("Expected ContactService to return false on successful connection")
			}

			select {
			case cmd := <-receivedCommand:
				cmd = strings.TrimSpace(cmd)
				if cmd != tc.want {
					t.Errorf("Expected command '%s', got '%s'", tc.want, cmd)
				}
			case <-time.After(1 * time.Second):
				t.Error("Timeout waiting for command")
			}
		})
	}
}
