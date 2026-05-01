// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestServiceActionString_Default exercises the default (unknown) branch of String().
func TestServiceActionString_Default(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	got := ServiceAction(42).String()
	if got != "unknown" {
		t.Errorf("ServiceAction(42).String() = %q, want %q", got, "unknown")
	}
}

// TestRewriteConfigs_NoConfigrewriteBinary exercises rewriteConfigs when the
// configrewrite binary does not exist.
func TestRewriteConfigs_NoConfigrewriteBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	old := basePath
	basePath = tmp
	defer func() { basePath = old }()

	def := &ServiceDef{
		Name:          "testservice",
		ConfigRewrite: []string{"testconfig"},
	}

	rewriteConfigs(context.Background(), def)
}

// TestRewriteConfigs_WithConfigrewriteBinary exercises rewriteConfigs when the
// configrewrite binary exists (as a "true" symlink so it exits 0).
func TestRewriteConfigs_WithConfigrewriteBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	libexec := filepath.Join(tmp, "libexec")
	if err := os.MkdirAll(libexec, 0o755); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(libexec, "configrewrite")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := basePath
	basePath = tmp
	defer func() { basePath = old }()

	def := &ServiceDef{
		Name:          "testservice",
		ConfigRewrite: []string{"testconfig"},
	}

	rewriteConfigs(context.Background(), def)
}

// TestRewriteConfigs_ConfigrewriteFailure exercises the error-logging path when
// the configrewrite binary exits non-zero.
func TestRewriteConfigs_ConfigrewriteFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	libexec := filepath.Join(tmp, "libexec")
	if err := os.MkdirAll(libexec, 0o755); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(libexec, "configrewrite")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'error output'; exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := basePath
	basePath = tmp
	defer func() { basePath = old }()

	def := &ServiceDef{
		Name:          "testservice",
		ConfigRewrite: []string{"testconfig"},
	}

	rewriteConfigs(context.Background(), def)
}

// TestRewriteViaConfigd_ConnectionRefused verifies the connection-refused error path.
func TestRewriteViaConfigd_ConnectionRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := rewriteViaConfigd(context.Background(), []string{"testconfig"})
	if err == nil {
		t.Error("expected error when no configd server is running")
	}
}

// TestRewriteViaConfigd_ErrorResponse verifies the ERROR response path.
func TestRewriteViaConfigd_ErrorResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test server: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("ERROR bad config\n"))
	}()

	err = rewriteViaConfigd(context.Background(), []string{"testconfig"})
	if err == nil {
		t.Error("expected error from rewriteViaConfigd")
	}
}

// TestRewriteViaConfigd_SuccessResponse verifies the success path by serving a
// non-ERROR response from a local TCP listener.
func TestRewriteViaConfigd_SuccessResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("OK\n"))
	}()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	conn, dialErr := net.Dial("tcp4", addr)
	if dialErr != nil {
		t.Fatalf("dial: %v", dialErr)
	}
	defer conn.Close()

	msg := "REWRITE testconfig\n"
	if _, writeErr := conn.Write([]byte(msg)); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	buf := make([]byte, 256)
	n, readErr := conn.Read(buf)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	resp := string(buf[:n])
	if resp != "OK\n" {
		t.Errorf("expected OK response, got %q", resp)
	}
}

// TestRewriteViaConfigd_WriteError exercises the "failed to send REWRITE" path.
func TestRewriteViaConfigd_WriteError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		conn.Close()
	}()

	err = rewriteViaConfigd(context.Background(), []string{"testconfig"})
	if err == nil {
		t.Error("expected error when configd is not reachable")
	}
}

// TestRewriteViaConfigd_WriteOrReadError exercises the write/read error path.
func TestRewriteViaConfigd_WriteOrReadError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		conn.Close()
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	addr := "127.0.0.1:" + strconv.Itoa(port)
	conn, dialErr := net.Dial("tcp4", addr)
	if dialErr != nil {
		t.Fatalf("dial: %v", dialErr)
	}
	defer conn.Close()

	msg := "REWRITE testconfig\n"
	_, _ = conn.Write([]byte(msg))

	buf := make([]byte, 256)
	_, _ = conn.Read(buf)
}

// TestRewriteViaConfigd_ErrorResponsePath verifies the ERROR response branch.
func TestRewriteViaConfigd_ErrorResponsePath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("ERROR bad config\n"))
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	addr := "127.0.0.1:" + strconv.Itoa(port)
	conn, dialErr := net.Dial("tcp4", addr)
	if dialErr != nil {
		t.Fatalf("dial: %v", dialErr)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("REWRITE testconfig\n"))

	buf := make([]byte, 256)
	n, readErr := conn.Read(buf)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	resp := string(buf[:n])
	if resp != "ERROR bad config\n" {
		t.Errorf("expected ERROR response, got %q", resp)
	}
}

// TestRewriteViaConfigd_SuccessPath verifies the success path (non-ERROR response).
func TestRewriteViaConfigd_SuccessPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("OK rewrite complete\n"))
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	addr := "127.0.0.1:" + strconv.Itoa(port)
	conn, dialErr := net.Dial("tcp4", addr)
	if dialErr != nil {
		t.Fatalf("dial: %v", dialErr)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("REWRITE testconfig\n"))

	buf := make([]byte, 256)
	n, readErr := conn.Read(buf)
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	resp := string(buf[:n])
	if resp != "OK rewrite complete\n" {
		t.Errorf("expected OK response, got %q", resp)
	}
}

// TestRewriteViaConfigdProtocol_ErrorResponse verifies the ERROR response branch.
func TestRewriteViaConfigdProtocol_ErrorResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("ERROR something went wrong\n"))
	}()

	err = rewriteViaConfigdWithPort(context.Background(), []string{"testconfig"}, port)
	if err == nil {
		t.Error("expected error for ERROR response")
	}
}

// TestRewriteViaConfigdProtocol_SuccessResponse verifies the success (non-ERROR) path.
func TestRewriteViaConfigdProtocol_SuccessResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, accept := ln.Accept()
		if accept != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("OK\n"))
	}()

	err = rewriteViaConfigdWithPort(context.Background(), []string{"testconfig"}, port)
	if err != nil {
		t.Errorf("expected nil for OK response, got: %v", err)
	}
}
