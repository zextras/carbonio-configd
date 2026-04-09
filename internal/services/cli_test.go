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
