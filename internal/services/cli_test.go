// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestIsDepEnabled_UnknownService verifies that an unregistered dep is disabled.
func TestIsDepEnabled_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if isDepEnabled(context.Background(), "nonexistent-service-xyz") {
		t.Error("expected isDepEnabled to return false for unknown service")
	}
}

// TestIsDepEnabled_NoEnableCheck verifies that a service without EnableCheck is enabled.
func TestIsDepEnabled_NoEnableCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// "memcached" is a real service with no EnableCheck — always enabled.
	if !isDepEnabled(context.Background(), "memcached") {
		t.Error("expected isDepEnabled to return true for service with no EnableCheck")
	}
}

// TestIsDepEnabled_EnableCheckTrue verifies that EnableCheck returning true enables the dep.
func TestIsDepEnabled_EnableCheckTrue(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.EnableCheck = func(_ context.Context) bool { return true }
	Registry["memcached"] = &def

	if !isDepEnabled(context.Background(), "memcached") {
		t.Error("expected isDepEnabled to return true when EnableCheck returns true")
	}
}

// TestIsDepEnabled_EnableCheckFalse verifies that EnableCheck returning false disables the dep.
func TestIsDepEnabled_EnableCheckFalse(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	def := *orig
	def.EnableCheck = func(_ context.Context) bool { return false }
	Registry["memcached"] = &def

	if isDepEnabled(context.Background(), "memcached") {
		t.Error("expected isDepEnabled to return false when EnableCheck returns false")
	}
}

// TestNewCLIServiceManager verifies the CLI service manager is configured for systemd.
func TestNewCLIServiceManager(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := newCLIServiceManager()
	if sm == nil {
		t.Fatal("newCLIServiceManager() returned nil")
	}

	if !sm.UseSystemd {
		t.Error("expected UseSystemd=true in CLI service manager")
	}

	if sm.CommandMap == nil {
		t.Error("CommandMap should not be nil")
	}
}

// TestServiceStart_UnknownService verifies error for unknown service.
func TestServiceStart_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := ServiceStart(context.Background(), "nonexistent-xyz")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

// TestServiceStop_UnknownService verifies error for unknown service.
func TestServiceStop_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := ServiceStop(context.Background(), "nonexistent-xyz")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

// TestServiceRestart_UnknownService verifies error for unknown service.
func TestServiceRestart_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := ServiceRestart(context.Background(), "nonexistent-xyz")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

// TestServiceReload_UnknownService verifies error for unknown service.
func TestServiceReload_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := ServiceReload(context.Background(), "nonexistent-xyz")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

// TestServiceStatus_UnknownService verifies error for unknown service.
func TestServiceStatus_UnknownService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, err := ServiceStatus(context.Background(), "nonexistent-xyz")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

// TestRunPreStartHooks_NoHooks verifies that no hooks returns nil.
func TestRunPreStartHooks_NoHooks(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	def := &ServiceDef{Name: "test"}
	err := runPreStartHooks(context.Background(), "test", sm, def)
	if err != nil {
		t.Errorf("runPreStartHooks with no hooks returned error: %v", err)
	}
}

// TestRunPreStartHooks_HookError verifies that hook errors are returned.
func TestRunPreStartHooks_HookError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	wantErr := errors.New("hook failure")
	def := &ServiceDef{
		Name: "test",
		PreStart: []Hook{
			func(_ context.Context, _ *ServiceManager) error { return wantErr },
		},
	}
	err := runPreStartHooks(context.Background(), "test", sm, def)
	if err == nil {
		t.Fatal("expected error from failing pre-start hook")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want wrapping %v", err, wantErr)
	}
}

// TestRunPreStartHooks_HookSuccess verifies that successful hooks return nil.
func TestRunPreStartHooks_HookSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	called := false
	def := &ServiceDef{
		Name: "test",
		PreStart: []Hook{
			func(_ context.Context, _ *ServiceManager) error {
				called = true
				return nil
			},
		},
	}
	err := runPreStartHooks(context.Background(), "test", sm, def)
	if err != nil {
		t.Errorf("runPreStartHooks returned unexpected error: %v", err)
	}
	if !called {
		t.Error("hook was not called")
	}
}

// TestRunPostStartHooks_HookError verifies that post-start hook errors are logged
// but not returned (the function returns nothing).
func TestRunPostStartHooks_HookError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	sm := NewServiceManager()
	called := false
	def := &ServiceDef{
		Name: "test",
		PostStart: []Hook{
			func(_ context.Context, _ *ServiceManager) error {
				called = true
				return errors.New("post-start failure")
			},
		},
	}
	// runPostStartHooks does not return an error; it logs and continues.
	runPostStartHooks(context.Background(), "test", sm, def)
	if !called {
		t.Error("post-start hook was not called")
	}
}

// TestStartEnabledDependencies_UnknownDep verifies that an unknown dep fails gracefully.
// isDepEnabled returns false for unknown deps, so the dep is skipped — no error.
func TestStartEnabledDependencies_UnknownDep(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:         "parent",
		Dependencies: []string{"nonexistent-dep-xyz"},
	}
	err := startEnabledDependencies(context.Background(), "parent", def)
	if err != nil {
		t.Errorf("expected no error for unknown (disabled) dep, got: %v", err)
	}
}

// TestStartEnabledDependencies_NoDeps verifies empty dependencies are a no-op.
func TestStartEnabledDependencies_NoDeps(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{Name: "parent"}
	err := startEnabledDependencies(context.Background(), "parent", def)
	if err != nil {
		t.Errorf("expected no error with no dependencies, got: %v", err)
	}
}

// TestServiceListStatus_ReturnsAllServices verifies ServiceListStatus returns entries
// for every registered service.
func TestServiceListStatus_ReturnsAllServices(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctx := context.Background()
	result := ServiceListStatus(ctx)
	if len(result) == 0 {
		t.Fatal("expected at least one service in ServiceListStatus result")
	}
	// Every entry must have a non-empty Name
	for _, si := range result {
		if si.Name == "" {
			t.Error("ServiceInfo has empty Name")
		}
	}
	// Result count must match registry size
	if len(result) != len(Registry) {
		t.Errorf("expected %d entries, got %d", len(Registry), len(result))
	}
}

// TestSystemctl_NotBooted verifies Systemctl returns ErrSystemdNotBooted when
// systemd is not the init system (which is the case in the CI/test environment).
func TestSystemctl_NotBooted(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// systemd.IsBooted() returns false in this test environment (no /run/systemd/system).
	// Systemctl must return ErrSystemdNotBooted immediately without invoking systemctl.
	err := Systemctl(context.Background(), "status", "carbonio-fake.service")
	// In CI the host may or may not have systemd. Accept either:
	// - ErrSystemdNotBooted (non-systemd host)
	// - any other error (systemd host where unit doesn't exist)
	// What we must NOT get is a nil error for a non-existent unit.
	if err == nil {
		t.Error("expected Systemctl to return an error for a non-existent unit")
	}
}

// TestRewriteViaConfigd_OKFromLocalListener verifies a successful REWRITE round-trip.
func TestRewriteViaConfigd_OKFromLocalListener(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port

	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"zmconfigd_listen_port": fmt.Sprintf("%d", port),
		}, nil
	}

	defer func() { loadConfig = old }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		defer func() { _ = conn.Close() }()

		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			_ = scanner.Text()
		}

		_, _ = conn.Write([]byte("OK\n"))
	}()

	err = rewriteViaConfigd(context.Background(), []string{"proxy", "mta"})
	if err != nil {
		t.Errorf("rewriteViaConfigd() returned error: %v", err)
	}
}

// TestRewriteViaConfigd_NoPortFallsToDefault verifies error when no port config and default port is closed.
func TestRewriteViaConfigd_NoPortFallsToDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}

	defer func() { loadConfig = old }()

	err := rewriteViaConfigd(context.Background(), []string{"proxy"})
	if err == nil {
		t.Fatal("expected error when configd is not reachable")
	}

	if !strings.Contains(err.Error(), "configd not reachable") {
		t.Errorf("error = %q, want containing %q", err.Error(), "configd not reachable")
	}
}

// TestRewriteViaConfigd_ErrorFromListener verifies error when configd returns ERROR.
func TestRewriteViaConfigd_ErrorFromListener(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port

	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"zmconfigd_listen_port": fmt.Sprintf("%d", port),
		}, nil
	}

	defer func() { loadConfig = old }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		defer func() { _ = conn.Close() }()

		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			_ = scanner.Text()
		}

		_, _ = conn.Write([]byte("ERROR something went wrong\n"))
	}()

	err = rewriteViaConfigd(context.Background(), []string{"proxy"})
	if err == nil {
		t.Fatal("expected error when configd returns ERROR")
	}

	if !strings.Contains(err.Error(), "configd returned error") {
		t.Errorf("error = %q, want containing %q", err.Error(), "configd returned error")
	}
}

func TestRunningPID_NilDef(t *testing.T) {
	if RunningPID(nil) != 0 {
		t.Error("expected 0 for nil def")
	}
}

func TestRunningPID_EmptyPidFile(t *testing.T) {
	def := &ServiceDef{Name: "test", PidFile: "", ProcessName: ""}
	if RunningPID(def) != 0 {
		t.Error("expected 0 for empty PidFile and ProcessName")
	}
}

func TestRunningPID_ValidPidFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may read real proc")
	}
	self := os.Getpid()
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "test",
		PidFile:     pidFile,
		ProcessName: "nonexistent-process-xyz",
	}
	pid := RunningPID(def)
	if pid != self {
		t.Errorf("expected PID %d, got %d", self, pid)
	}
}

func TestRunningPID_PidFileDeadFallsBackToProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: reads /proc")
	}
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "dead.pid")
	if err := os.WriteFile(pidFile, []byte("99999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "test",
		PidFile:     pidFile,
		ProcessName: "nonexistent-process-xyz-test",
	}
	pid := RunningPID(def)
	if pid != 0 {
		t.Errorf("expected 0 for dead pidfile and nonexistent process, got %d", pid)
	}
}

func TestPidFromPidFile_EmptyPath(t *testing.T) {
	if pidFromPidFile("") != 0 {
		t.Error("expected 0 for empty path")
	}
}

func TestPidFromPidFile_NonexistentFile(t *testing.T) {
	if pidFromPidFile("/nonexistent/path/test.pid") != 0 {
		t.Error("expected 0 for nonexistent file")
	}
}

func TestPidFromPidFile_ValidPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: reads /proc")
	}
	self := os.Getpid()
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pid := pidFromPidFile(pidFile)
	if pid != self {
		t.Errorf("expected %d, got %d", self, pid)
	}
}

func TestPidFromProcessName_EmptyName(t *testing.T) {
	if pidFromProcessName("") != 0 {
		t.Error("expected 0 for empty process name")
	}
}

func TestPidFromProcessName_Nonexistent(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: reads /proc")
	}
	pid := pidFromProcessName("nonexistent-process-xyz-12345")
	if pid != 0 {
		t.Errorf("expected 0 for nonexistent process, got %d", pid)
	}
}

func TestServiceListStatusStream_Cancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := ServiceListStatusStream(ctx)
	count := 0
	for range ch {
		count++
	}
	_ = count
}

func TestServiceListStatusStream_Default(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctx := context.Background()
	ch := ServiceListStatusStream(ctx)

	entries := 0
	for info := range ch {
		if info.Name == "" {
			t.Error("expected non-empty Name in ServiceInfo")
		}
		entries++
	}

	if entries != len(Registry) {
		t.Errorf("expected %d entries, got %d", len(Registry), entries)
	}
}

func TestDefaultIsSystemdMode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_ = defaultIsSystemdMode()
}

func TestIsSystemdMode_Override(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return true }
	if !IsSystemdMode() {
		t.Error("expected IsSystemdMode to return true when override is true")
	}

	isSystemdModeFn = func() bool { return false }
	if IsSystemdMode() {
		t.Error("expected IsSystemdMode to return false when override is false")
	}
}

func TestServiceStatus_SystemdModeOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return false }

	def := &ServiceDef{
		Name:        "test-legacy-status",
		ProcessName: "nonexistent-process-xyz",
	}
	Registry["test-legacy-status"] = def
	defer delete(Registry, "test-legacy-status")

	running, err := ServiceStatus(context.Background(), "test-legacy-status")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for nonexistent process in legacy mode")
	}
}

func TestNoRewrite_SkipConfigRewrite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	origNoRewrite := NoRewrite
	defer func() { NoRewrite = origNoRewrite }()

	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return false }
	NoRewrite = true

	def := &ServiceDef{
		Name:          "test-norewrite",
		ConfigRewrite: []string{"proxy", "mta"},
	}
	Registry["test-norewrite"] = def
	defer delete(Registry, "test-norewrite")

	err := ServiceStart(context.Background(), "test-norewrite")
	if err == nil {
		t.Error("expected error for service with no launcher")
	}
}

func TestServiceReload_FallbackOnNonSystemd(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return false }

	err := ServiceReload(context.Background(), "proxy")
	_ = err
}

func TestServiceStatus_LegacyMode_PidFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return false }

	self := os.Getpid()
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "test-pid-status",
		PidFile:     pidFile,
		ProcessName: "nonexistent-pid-status-xyz",
	}
	Registry["test-pid-status"] = def
	defer delete(Registry, "test-pid-status")

	running, err := ServiceStatus(context.Background(), "test-pid-status")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !running {
		t.Error("expected running=true for service pointing to our own PID")
	}
}

func TestServiceStatus_LegacyMode_ProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return false }

	def := &ServiceDef{
		Name:        "test-proc-scan",
		ProcessName: "nonexistent-proc-scan-xyz",
	}
	Registry["test-proc-scan"] = def
	defer delete(Registry, "test-proc-scan")

	running, err := ServiceStatus(context.Background(), "test-proc-scan")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for nonexistent process")
	}
}

func TestServiceStatus_LegacyMode_NoPidFileNoProcessName(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()

	isSystemdModeFn = func() bool { return false }

	def := &ServiceDef{
		Name: "test-no-pid-no-proc",
	}
	Registry["test-no-pid-no-proc"] = def
	defer delete(Registry, "test-no-pid-no-proc")

	running, err := ServiceStatus(context.Background(), "test-no-pid-no-proc")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected running=false for service with no PidFile and no ProcessName")
	}
}

func TestStartEnabledDependencies_DisabledDep(t *testing.T) {
	def := &ServiceDef{
		Name:         "test-with-disabled-dep",
		Dependencies: []string{"nonexistent-service-xyz"},
	}

	err := startEnabledDependencies(context.Background(), "test-with-disabled-dep", def)
	if err != nil {
		t.Errorf("startEnabledDependencies with disabled dep should not error, got %v", err)
	}
}

func TestRunPreStartHooks_Nil(t *testing.T) {
	sm := NewServiceManager()
	def := &ServiceDef{Name: "test", PreStart: nil}
	err := runPreStartHooks(context.Background(), "test", sm, def)
	if err != nil {
		t.Errorf("expected nil for no hooks, got %v", err)
	}
}

func TestRunPostStartHooks_Nil(t *testing.T) {
	def := &ServiceDef{Name: "test", PostStart: nil}
	runPostStartHooks(context.Background(), "test", nil, def)
}

func TestRewriteConfigs_NoScript(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	oldBase := basePath
	basePath = "/nonexistent/path/for/test"
	defer func() { basePath = oldBase }()

	def := &ServiceDef{
		Name:          "test-rewrite-no-script",
		ConfigRewrite: []string{"proxy"},
	}
	rewriteConfigs(context.Background(), def)
}

func TestServiceStart_AlreadyRunningPid(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	// Register a service that's "running" (our own PID in pidfile)
	tmp := t.TempDir()
	self := os.Getpid()
	pidFile := filepath.Join(tmp, "test.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:        "test-already-running",
		PidFile:     pidFile,
		ProcessName: "nonexistent-already-running-xyz",
	}
	Registry["test-already-running"] = def
	defer delete(Registry, "test-already-running")

	err := ServiceStart(context.Background(), "test-already-running")
	if err != nil {
		t.Errorf("expected nil when service already running, got %v", err)
	}
}

func TestServiceRestart_UnknownSvc(t *testing.T) {
	err := ServiceRestart(context.Background(), "nonexistent-service-xyz")
	if err == nil {
		t.Error("expected error for unknown service")
	}
}

func TestStartEnabledDependencies_FailsToStart(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	depDef := &ServiceDef{
		Name:        "test-dep-fail",
		ProcessName: "nonexistent-dep-fail-xyz",
		BinaryPath:  "/nonexistent/binary",
	}
	Registry["test-dep-fail"] = depDef
	defer delete(Registry, "test-dep-fail")

	parentDef := &ServiceDef{
		Name:         "test-parent-dep",
		Dependencies: []string{"test-dep-fail"},
		BinaryPath:   "/nonexistent/binary",
	}
	Registry["test-parent-dep"] = parentDef
	defer delete(Registry, "test-parent-dep")

	err := startEnabledDependencies(context.Background(), "test-parent-dep", parentDef)
	if err == nil {
		t.Error("expected error when dependency fails to start")
	}
}
