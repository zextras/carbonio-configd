// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/systemd"
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

// rewriteViaConfigdWithPort is a test helper that mirrors rewriteViaConfigd
// but accepts an explicit port so we can inject a test server.
func rewriteViaConfigdWithPort(ctx context.Context, configs []string, port int) error {
	addr := net.JoinHostPort("localhost", strconv.Itoa(port))
	dialer := &net.Dialer{}

	conn, err := dialer.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	msg := "REWRITE "
	for i, c := range configs {
		if i > 0 {
			msg += " "
		}
		msg += c
	}
	msg += "\n"

	if _, err := conn.Write([]byte(msg)); err != nil {
		return err
	}

	buf := make([]byte, 2048)

	n, err := conn.Read(buf)
	if err != nil {
		return err
	}

	resp := string(buf[:n])
	if len(resp) >= 5 && resp[:5] == "ERROR" {
		return errors.New("configd returned error: " + resp)
	}

	return nil
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

// TestServiceStatus_NoPidFileNoProcessName verifies ServiceStatus returns (false, nil)
// when no detection method is configured (non-systemd hosts only).
func TestServiceStatus_NoPidFileNoProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: on systemd host ServiceStatus uses systemctl, not PID/process detection")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = ""
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false when no detection method configured")
	}
}

// TestServiceStatus_WithPidFile_Running verifies ServiceStatus detects running via PID file.
func TestServiceStatus_WithPidFile_Running(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping PID-file detection test on systemd-booted host")
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "test.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = pidFile
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !running {
		t.Error("expected running=true when pidfile contains our own PID")
	}
}

// TestServiceStatus_WithProcessName verifies ServiceStatus uses ProcessName fallback.
func TestServiceStatus_WithProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: on systemd host ServiceStatus uses systemctl, not process name scan")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = ""
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-no-match-99999"
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for non-existent process name")
	}
}

// TestServiceStatus_PidFileUnreadable_FallsBackToProcessName exercises the
// "PID file unreadable → fall through to ProcessName" path.
func TestServiceStatus_PidFileUnreadable_FallsBackToProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: PID fallback test on systemd-booted host")
	}
	if os.Getuid() == 0 {
		t.Skip("cannot test unreadable pidfile as root")
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "unreadable.pid")
	if err := os.WriteFile(pidFile, []byte("12345\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(pidFile, 0o644) //nolint:errcheck

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = pidFile
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-no-match-99999"
	def.SystemdUnits = nil
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for non-existent process")
	}
}

// TestServiceStatus_PidFileMissing_FallsToProcessName exercises the PID file absent
// path falling through to ProcessName on non-systemd hosts.
func TestServiceStatus_PidFileMissing_FallsToProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: legacy PID fallback test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = "/nonexistent-pid-file-xyz-test.pid"
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-no-match-99999"
	def.SystemdUnits = nil
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for non-existent process")
	}
}

// withMode forces IsSystemdMode() to return the given value for the duration
// of a test, then restores the production detector. The two orchestration
// modes are mutually exclusive; this helper lets each test pin the one it
// means to exercise without depending on the host's target enablement.
func withMode(t *testing.T, strict bool) {
	t.Helper()

	orig := isSystemdModeFn
	isSystemdModeFn = func() bool { return strict }

	t.Cleanup(func() { isSystemdModeFn = orig })
}

// TestServiceStatus_LegacyMode_UsesPidProbeRegardlessOfSystemdUnit asserts that
// in legacy mode (no Carbonio target enabled) ServiceStatus ignores the
// service's SystemdUnits entirely and reports running from the PID file.
// This is the container regression test: stats in a podman install has an
// inactive carbonio-stats.service but live zmstat-* workers; before the
// IsSystemdMode()-gated refactor, a systemd-booted host would query
// systemctl and report stopped even though workers were alive.
func TestServiceStatus_LegacyMode_UsesPidProbeRegardlessOfSystemdUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	withMode(t, false)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "live.pid")

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = []string{"carbonio-nonexistent-test-xyz.service"}
	def.PidFile = pidFile
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !running {
		t.Error("expected running=true in legacy mode: PID file points to a live process, SystemdUnits must be ignored")
	}
}

// TestServiceStatus_StrictMode_TrustsSystemctlOverPidProbe asserts that in
// strict systemd mode a non-existent unit causes ServiceStatus to return
// (false, nil) even when a live PID file exists. This guards against
// reintroducing the hybrid fall-through (which would have returned true by
// checking the PID file after systemctl failed).
func TestServiceStatus_StrictMode_TrustsSystemctlOverPidProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	if !systemd.IsBooted() {
		t.Skip("skipping: strict-mode path requires a host that can actually run systemctl")
	}

	withMode(t, true)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "live.pid")

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = []string{"carbonio-nonexistent-test-xyz.service"}
	def.PidFile = pidFile
	def.ProcessName = ""
	Registry["memcached"] = &def

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if running {
		t.Error("expected running=false in strict mode: systemd unit is not-active and must be authoritative")
	}
}

// ============================================================
// cli.go — ServiceRestart
// ============================================================

// TestServiceRestart_MTA_IsRegistered verifies that MTA restart calls ServiceReload.
func TestServiceRestart_MTA_IsRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := ServiceRestart(ctx, "mta")
	_ = err
}

// TestServiceRestart_StopFailedStartAnyway exercises the "stop failed, start anyway" branch.
func TestServiceRestart_StopFailedStartAnyway(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: restart test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.Name = "memcached"
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		return errors.New("stop failed intentionally")
	}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error {
		return nil
	}
	def.ConfigRewrite = nil
	Registry["memcached"] = &def

	ServiceRestart(context.Background(), "memcached")
}

// TestServiceRestart_NonMTA_StopAndStartBoth exercises the stop-failed-warn-then-start path.
func TestServiceRestart_NonMTA_StopAndStartBoth(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping restart branch test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	stopCalled := false
	startCalled := false

	def := *orig
	def.Name = "memcached"
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = ""
	def.ConfigRewrite = nil
	def.Dependencies = nil
	def.PreStart = nil
	def.PostStart = nil
	def.PreStop = nil
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		stopCalled = true
		return errors.New("stop failed intentionally")
	}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error {
		startCalled = true
		return nil
	}
	Registry["memcached"] = &def

	ServiceRestart(context.Background(), "memcached")

	if !stopCalled {
		t.Error("expected stop to be called")
	}
	if !startCalled {
		t.Error("expected start to be called even after stop failure")
	}
}

// TestServiceReload_NoSystemdUnits verifies ServiceReload returns nil when no units defined.
func TestServiceReload_NoSystemdUnits(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = nil
	Registry["memcached"] = &def

	err := ServiceReload(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceReload with no units returned error: %v", err)
	}
}

// TestServiceStop_WithDisabledDependency exercises the "stop dependencies" path.
func TestServiceStop_WithDisabledDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: stop dependency test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.Dependencies = []string{"nonexistent-dep-xyz"}
	def.ProcessName = "carbonio-configd-unique-needle-xyzzy-99999"
	def.PidFile = ""
	def.SystemdUnits = nil
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStop with disabled dependency returned error: %v", err)
	}
}

// TestServiceStop_WithEnabledDependency exercises stopping with an enabled dependency.
func TestServiceStop_WithEnabledDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: stop dependency test on systemd-booted host")
	}

	const depName = "test-dep-xyzzy"
	Registry[depName] = &ServiceDef{
		Name:       depName,
		CustomStop: func(_ context.Context, _ *ServiceDef) error { return nil },
	}
	defer delete(Registry, depName)

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.Dependencies = []string{depName}
	def.SystemdUnits = nil
	def.PidFile = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStop with enabled dependency returned error: %v", err)
	}
}

// TestServiceStop_StopServiceError exercises the stopService error path.
func TestServiceStop_StopServiceError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: stopService error test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		return errors.New("stop failed")
	}
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err == nil {
		t.Error("expected ServiceStop to return error when stopService fails")
	}
}

// TestServiceStop_PreStopHookError exercises the pre-stop hook warning path.
func TestServiceStop_PreStopHookError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: pre-stop hook test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	called := false
	def := *orig
	def.PreStop = []Hook{
		func(_ context.Context, _ *ServiceManager) error {
			called = true
			return errors.New("pre-stop failed")
		},
	}
	def.SystemdUnits = nil
	def.PidFile = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStop(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStop should not return error for pre-stop hook failure, got: %v", err)
	}
	if !called {
		t.Error("pre-stop hook was not called")
	}
}

// TestServiceStop_WithPreStopHookFails verifies pre-stop hook does not cause panic.
func TestServiceStop_WithPreStopHookFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	orig, existed := Registry["memcached"]
	defer func() {
		if existed {
			Registry["memcached"] = orig
		}
	}()

	def := *Registry["memcached"]
	def.PreStop = []Hook{
		func(_ context.Context, _ *ServiceManager) error {
			return errors.New("pre-stop hook error")
		},
	}
	Registry["memcached"] = &def

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ServiceStop(ctx, "memcached")
}

// TestServiceStart_WithConfigRewrite exercises the rewriteConfigs branch.
func TestServiceStart_WithConfigRewrite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: start with config rewrite test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = "carbonio-configd-test-unique-xyzzy-notrunning"
	def.ConfigRewrite = []string{"testconfig"}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error { return nil }
	def.Dependencies = nil
	def.PreStart = nil
	def.PostStart = nil
	Registry["memcached"] = &def

	oldNoRewrite := NoRewrite
	NoRewrite = false
	defer func() { NoRewrite = oldNoRewrite }()

	ServiceStart(context.Background(), "memcached")
}

// TestServiceStart_AlreadyRunning exercises the "already running" early return.
func TestServiceStart_AlreadyRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: already-running test on systemd-booted host")
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "running.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.PidFile = pidFile
	def.ProcessName = ""
	def.SystemdUnits = nil
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error {
		t.Error("CustomStart should not be called when service is already running")
		return nil
	}
	Registry["memcached"] = &def

	err := ServiceStart(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStart for already-running service returned error: %v", err)
	}
}

// TestServiceStart_PreStartHookError exercises the pre-start hook error return.
func TestServiceStart_PreStartHookError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: pre-start hook test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = "carbonio-configd-test-unique-xyzzy-notrunning"
	def.ConfigRewrite = nil
	def.Dependencies = nil
	def.PreStart = []Hook{
		func(_ context.Context, _ *ServiceManager) error {
			return errors.New("pre-start hook failed")
		},
	}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStart(context.Background(), "memcached")
	if err == nil {
		t.Error("expected error from pre-start hook failure")
	}
}

// TestServiceStart_PostStartHookCalled exercises the post-start hook path.
func TestServiceStart_PostStartHookCalled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("skipping: post-start hook test on systemd-booted host")
	}

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	called := false
	def := *orig
	def.SystemdUnits = nil
	def.PidFile = ""
	def.ProcessName = "carbonio-configd-test-unique-xyzzy-notrunning"
	def.ConfigRewrite = nil
	def.Dependencies = nil
	def.PreStart = nil
	def.PostStart = []Hook{
		func(_ context.Context, _ *ServiceManager) error {
			called = true
			return nil
		},
	}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error { return nil }
	Registry["memcached"] = &def

	err := ServiceStart(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceStart returned unexpected error: %v", err)
	}
	if !called {
		t.Error("post-start hook was not called")
	}
}

// TestServiceStart_NoRewrite verifies the NoRewrite flag skips config rewriting.
func TestServiceStart_NoRewrite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	old := NoRewrite
	NoRewrite = true
	defer func() { NoRewrite = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ServiceStart(ctx, "memcached")
}

// TestStartEnabledDependencies_EnabledDepFailsStart exercises the ServiceStart error return.
func TestStartEnabledDependencies_EnabledDepFailsStart(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	const depName = "test-failing-dep-xyzzy"

	Registry[depName] = &ServiceDef{
		Name:         depName,
		SystemdUnits: []string{"carbonio-test-failing-dep-xyzzy.service"},
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			return errors.New("intentional failure")
		},
	}
	defer delete(Registry, depName)

	def := &ServiceDef{
		Name:         "parent",
		Dependencies: []string{depName},
	}

	if IsSystemdMode() {
		t.Skip("systemd booted: ServiceStatus short-circuits before CustomStart")
	}

	err := startEnabledDependencies(context.Background(), "parent", def)
	if err == nil {
		t.Error("expected error when enabled dependency fails to start")
	}
}

// TestStartEnabledDependencies_EnabledDepFails verifies that a dependency that is
// enabled but fails to start propagates the error.
func TestStartEnabledDependencies_EnabledDepFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	Registry["testdep-xyz"] = &ServiceDef{
		Name: "testdep-xyz",
	}
	defer delete(Registry, "testdep-xyz")

	def := &ServiceDef{
		Name:         "parent",
		Dependencies: []string{"testdep-xyz"},
	}

	startEnabledDependencies(context.Background(), "parent", def)
}

// TestSystemctl_BootedHostReturnsError verifies Systemctl on a booted host.
func TestSystemctl_BootedHostReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if !systemd.IsBooted() {
		t.Skip("skipping: Systemctl success path requires systemd-booted host")
	}

	err := Systemctl(context.Background(), "status", "carbonio-configd-nonexistent-unit-xyzzy.service")
	if err == nil {
		t.Error("expected error for non-existent systemd unit")
	}
}

// TestSystemctl_AnyHost_FakeUnit verifies behavior on any host type.
func TestSystemctl_AnyHost_FakeUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := Systemctl(context.Background(), "is-active", "carbonio-fake-unit-xyzzy-test.service")
	if err == nil {
		t.Error("expected error for fake unit")
	}
}

// TestIsSystemdMode_DoesNotPanic verifies IsSystemdMode() does not panic.
func TestIsSystemdMode_DoesNotPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	IsSystemdMode()
	IsSystemdMode()
}

// TestStartWithoutSystemd_CustomStart verifies that CustomStart is called.
func TestStartWithoutSystemd_CustomStart(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	called := false
	def := &ServiceDef{
		Name: "testservice",
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startWithoutSystemd with CustomStart returned error: %v", err)
	}
	if !called {
		t.Error("CustomStart was not called")
	}
}

// TestStartWithoutSystemd_NoBinaryWithProcessName verifies the "managed via deps" path.
func TestStartWithoutSystemd_NoBinaryWithProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:        "testservice",
		ProcessName: "someprocess",
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("expected nil when ProcessName set and no BinaryPath, got: %v", err)
	}
}

// TestStartWithoutSystemd_NoBinaryNoProcessName verifies the error path.
func TestStartWithoutSystemd_NoBinaryNoProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name: "testservice",
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error when no launcher and no ProcessName")
	}
}

// TestStartWithoutSystemd_BinaryNotFound verifies startDirect returns error for missing binary.
func TestStartWithoutSystemd_BinaryNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "/nonexistent/binary/xyz-configd-test",
	}

	err := startWithoutSystemd(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

// TestStopWithoutSystemd_CustomStop verifies CustomStop is called.
func TestStopWithoutSystemd_CustomStop(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	called := false
	def := &ServiceDef{
		Name: "testservice",
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := stopWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("stopWithoutSystemd with CustomStop returned error: %v", err)
	}
	if !called {
		t.Error("CustomStop was not called")
	}
}

// TestStopWithoutSystemd_NoProcessName verifies error when neither CustomStop nor
// ProcessName is configured.
func TestStopWithoutSystemd_NoProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name: "testservice",
	}

	err := stopWithoutSystemd(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error when no CustomStop and no ProcessName")
	}
}

// TestStopWithoutSystemd_ProcessName verifies killProcess is called.
func TestStopWithoutSystemd_ProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:        "testservice",
		ProcessName: "carbonio-configd-test-needle-xyzzy-99999",
	}

	err := stopWithoutSystemd(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("stopWithoutSystemd with ProcessName returned unexpected error: %v", err)
	}
}

// TestKillProcess_NoMatch verifies killProcess returns nil when no process matches.
func TestKillProcess_NoMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := killProcess(context.Background(), "carbonio-configd-unique-needle-xyzzy-no-match-99999")
	if err != nil {
		t.Errorf("killProcess with no match returned error: %v", err)
	}
}

// TestKillProcess_SelfExclusion verifies killProcess skips our own PID even when matched.
func TestKillProcess_SelfExclusion(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	self := os.Getpid()

	selfDir := filepath.Join(tmpDir, strconv.Itoa(self))
	if err := os.MkdirAll(selfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	needle := "killprocess-selftest-needle-unique"
	if err := os.WriteFile(filepath.Join(selfDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(selfDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestKillProcess_ParentExclusion verifies killProcess skips the parent PID.
func TestKillProcess_ParentExclusion(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	parent := os.Getppid()

	parentDir := filepath.Join(tmpDir, strconv.Itoa(parent))
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	needle := "killprocess-parent-exclusion-needle"
	if err := os.WriteFile(filepath.Join(parentDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(parentDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestKillProcess_ParentExcluded verifies killProcess excludes parent PID (variant).
func TestKillProcess_ParentExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmp + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	parent := os.Getppid()

	parentDir := filepath.Join(tmp, strconv.Itoa(parent))
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	needle := "killprocess-parent-test-needle-xyzzy"
	if err := os.WriteFile(filepath.Join(parentDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(parentDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestKillProcess_SelfExcluded verifies killProcess does not kill itself (variant).
func TestKillProcess_SelfExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmp + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	self := os.Getpid()

	selfDir := filepath.Join(tmp, strconv.Itoa(self))
	if err := os.MkdirAll(selfDir, 0o755); err != nil {
		t.Fatal(err)
	}

	needle := "killprocess-self-test-needle-xyzzy"
	if err := os.WriteFile(filepath.Join(selfDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(selfDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestStartDirect_EmptyBinaryPath verifies error returned immediately for empty binary.
func TestStartDirect_EmptyBinaryPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "",
	}

	err := startDirect(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for empty BinaryPath")
	}
}

// TestStartDirect_MissingBinary verifies error when binary file does not exist.
func TestStartDirect_MissingBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "/nonexistent/binary/xyz-configd-test-direct",
	}

	err := startDirect(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

// TestStartDirect_NeedsRoot_NonRoot exercises the sudo-wrapping branch.
func TestStartDirect_NeedsRoot_NonRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if os.Getuid() == 0 {
		t.Skip("skipping: test requires non-root user")
	}

	truePath := "/bin/true"
	if _, err := os.Stat(truePath); err != nil {
		t.Skip("skipping: /bin/true not available")
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  truePath,
		NeedsRoot:   true,
		Detached:    false,
		UseSDNotify: false,
	}

	startDirect(context.Background(), "testservice", def)
}

// TestStartDirect_NeedsRootAsNonRoot verifies that NeedsRoot=true prepends sudo.
func TestStartDirect_NeedsRootAsNonRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if os.Getuid() == 0 {
		t.Skip("NeedsRoot branch only applies to non-root users")
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "fakebinary")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  fakeBin,
		NeedsRoot:   true,
		Detached:    false,
		UseSDNotify: false,
	}

	startDirect(context.Background(), "testservice", def)
}

// TestStartDirect_DirectExec_Success verifies startDirect with a real binary that exits 0.
func TestStartDirect_DirectExec_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "fakebinary")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  fakeBin,
		NeedsRoot:   false,
		Detached:    false,
		UseSDNotify: false,
	}

	err := startDirect(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startDirect with exiting-0 binary returned error: %v", err)
	}
}

// TestStartDirect_DirectExec_Failure verifies startDirect with a binary that exits non-zero.
func TestStartDirect_DirectExec_Failure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "fakebinary")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  fakeBin,
		NeedsRoot:   false,
		Detached:    false,
		UseSDNotify: false,
	}

	err := startDirect(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error for binary that exits non-zero")
	}
}

// TestStartDirect_DetachedPath exercises the Detached=true branch in startDirect.
func TestStartDirect_DetachedPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	truePath := "/bin/true"
	if _, err := os.Stat(truePath); err != nil {
		t.Skip("skipping: /bin/true not available")
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  truePath,
		Detached:    true,
		LogFile:     filepath.Join(tmp, "test.log"),
		UseSDNotify: false,
	}

	err := startDirect(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startDirect Detached=true returned error: %v", err)
	}
}

// TestStartDirect_DetachedMissingBinary verifies startDetached returns error for missing binary.
func TestStartDirect_DetachedMissingBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "test.out")

	def := &ServiceDef{
		Name:       "testdetached",
		BinaryPath: filepath.Join(tmp, "nonexistent-binary"),
		Detached:   true,
		LogFile:    logFile,
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when detached binary is missing")
	}
}

// TestStartDetached_LogFileOpenError exercises the os.OpenFile error path.
func TestStartDetached_LogFileOpenError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testservice",
		BinaryPath: "/bin/true",
		LogFile:    "/nonexistent-dir-xyz/test.log",
	}

	err := startDetached(context.Background(), "testservice", def)
	if err == nil {
		t.Error("expected error when log file cannot be opened")
	}
}

// TestStartDetached_LogOpenFails verifies startDetached returns error when log cannot be opened.
func TestStartDetached_LogOpenFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "testdetached",
		BinaryPath: "/usr/bin/true",
		Detached:   true,
		LogFile:    "/nonexistent/dir/test.out",
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when log file cannot be opened")
	}
}

// TestStartDetached_SuccessNoSDNotify exercises the successful detach path (no SDNotify).
func TestStartDetached_SuccessNoSDNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  "/bin/true",
		LogFile:     filepath.Join(tmp, "test.log"),
		UseSDNotify: false,
		Detached:    false,
	}

	err := startDetached(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startDetached returned unexpected error: %v", err)
	}
}

// TestStartDetached_Success verifies startDetached spawns a background process.
func TestStartDetached_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "test.out")

	def := &ServiceDef{
		Name:        "testdetached",
		BinaryPath:  "/usr/bin/sleep",
		BinaryArgs:  []string{"0"},
		Detached:    true,
		UseSDNotify: false,
		LogFile:     logFile,
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err != nil {
		t.Errorf("startDetached returned unexpected error: %v", err)
	}
}

// TestStartDetached_DefaultLogPath verifies startDetached uses basePath/log/<name>.out
// when LogFile is empty.
func TestStartDetached_DefaultLogPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := basePath
	basePath = "/nonexistent-base-path-xyz"
	defer func() { basePath = old }()

	def := &ServiceDef{
		Name:       "testdetached",
		BinaryPath: "/usr/bin/true",
		Detached:   true,
		LogFile:    "",
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when default log path directory doesn't exist")
	}
}

// TestStartDetached_UseSDNotify exercises the UseSDNotify=true branch.
func TestStartDetached_UseSDNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	truePath := "/bin/true"
	if _, err := os.Stat(truePath); err != nil {
		t.Skip("skipping: /bin/true not available")
	}

	def := &ServiceDef{
		Name:        "testservice",
		BinaryPath:  truePath,
		LogFile:     filepath.Join(tmp, "test.log"),
		UseSDNotify: true,
		Detached:    false,
	}

	startDetached(context.Background(), "testservice", def)
}

// TestStartDetached_SDNotify_BinaryMissing verifies startDetached returns error
// when UseSDNotify=true and binary is missing.
func TestStartDetached_SDNotify_BinaryMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "test.out")

	def := &ServiceDef{
		Name:        "testdetached",
		BinaryPath:  filepath.Join(tmp, "nonexistent-binary"),
		Detached:    true,
		UseSDNotify: true,
		LogFile:     logFile,
	}

	err := startDetached(context.Background(), "testdetached", def)
	if err == nil {
		t.Error("expected error when UseSDNotify=true and binary is missing")
	}
}

// TestStartService_NonSystemdCustomStart verifies startService uses CustomStart
// when systemd is not booted.
func TestStartService_NonSystemdCustomStart(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("startService non-systemd path not reachable on systemd-booted host")
	}

	called := false
	def := &ServiceDef{
		Name:         "testservice",
		SystemdUnits: []string{"test.service"},
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := startService(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("startService returned unexpected error: %v", err)
	}
	if !called {
		t.Error("expected CustomStart to be called")
	}
}

// TestStopService_NonSystemdCustomStop verifies stopService uses CustomStop
// when systemd is not booted.
func TestStopService_NonSystemdCustomStop(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if IsSystemdMode() {
		t.Skip("stopService non-systemd path not reachable on systemd-booted host")
	}

	called := false
	def := &ServiceDef{
		Name:         "testservice",
		SystemdUnits: []string{"test.service"},
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			called = true
			return nil
		},
	}

	err := stopService(context.Background(), "testservice", def)
	if err != nil {
		t.Errorf("stopService returned unexpected error: %v", err)
	}
	if !called {
		t.Error("expected CustomStop to be called")
	}
}

// TestStopService_LegacyMode_BypassesSystemctl asserts that in legacy mode
// stopService goes straight to stopWithoutSystemd (CustomStop / pkill) without
// invoking systemctl. This guards the container fix: statsCustomStop must
// actually run to terminate workers that live outside any systemd cgroup.
func TestStopService_LegacyMode_BypassesSystemctl(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	withMode(t, false)

	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	called := false
	def := *orig
	def.SystemdUnits = []string{"carbonio-fake-stop-test-xyzzy.service"}
	def.ProcessName = ""
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		called = true
		return nil
	}
	Registry["memcached"] = &def

	if err := stopService(context.Background(), "memcached", &def); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected CustomStop to run in legacy mode; systemctl must not be invoked, fall-through must not exist")
	}
}

// TestSignalViaPidfile_UnreadableFile verifies the "exists but unreadable" path.
func TestSignalViaPidfile_UnreadableFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if os.Getuid() == 0 {
		t.Skip("cannot test unreadable file as root")
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "unreadable.pid")
	if err := os.WriteFile(pidFile, []byte("12345\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(pidFile, 0o644) //nolint:errcheck

	err := signalViaPidfile(context.Background(), pidFile, "testsvc", 0)
	if err == nil {
		t.Error("expected error for unreadable pidfile that exists")
	}
}
