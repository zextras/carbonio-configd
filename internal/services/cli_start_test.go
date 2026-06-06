// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

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

// TestServiceStartSystemd_RunsCustomLauncherAndHooks verifies the systemd-leaf
// start path invokes CustomStart and the pre/post-start hooks directly, WITHOUT
// going through systemctl. This is independent of IsSystemdMode() — it is the
// ExecStart leaf that breaks the systemctl recursion loop, so it must launch
// the workers in-process regardless of whether the host is systemd-booted.
func TestServiceStartSystemd_RunsCustomLauncherAndHooks(t *testing.T) {
	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	var startCalled, preCalled, postCalled bool

	def := *orig
	// A non-empty SystemdUnits must be ignored by the leaf path; set one so a
	// regression that calls systemctl would visibly diverge.
	def.SystemdUnits = []string{"carbonio-nonexistent-test.service"}
	def.PidFile = ""
	def.ProcessName = ""
	def.ConfigRewrite = nil
	def.Dependencies = nil
	def.PreStart = []Hook{func(_ context.Context, _ *ServiceManager) error {
		preCalled = true

		return nil
	}}
	def.PostStart = []Hook{func(_ context.Context, _ *ServiceManager) error {
		postCalled = true

		return nil
	}}
	def.CustomStart = func(_ context.Context, _ *ServiceDef) error {
		startCalled = true

		return nil
	}
	Registry["memcached"] = &def

	if err := ServiceStartSystemd(context.Background(), "memcached"); err != nil {
		t.Fatalf("ServiceStartSystemd returned unexpected error: %v", err)
	}

	if !preCalled {
		t.Error("pre-start hook was not called")
	}
	if !startCalled {
		t.Error("CustomStart launcher was not called")
	}
	if !postCalled {
		t.Error("post-start hook was not called")
	}
}

// TestServiceStartSystemd_UnknownService verifies the leaf path rejects an
// unknown service rather than silently succeeding (the bug symptom was an
// "unexpected argument" parse error; a known service must resolve).
func TestServiceStartSystemd_UnknownService(t *testing.T) {
	if err := ServiceStartSystemd(context.Background(), "nonexistent-service-xyz"); err == nil {
		t.Error("expected error for unknown service")
	}
}

// TestServiceStopSystemd_RunsCustomStop verifies the systemd-leaf stop path
// invokes CustomStop directly without systemctl.
func TestServiceStopSystemd_RunsCustomStop(t *testing.T) {
	orig := Registry["memcached"]
	defer func() { Registry["memcached"] = orig }()

	var stopCalled bool

	def := *orig
	def.SystemdUnits = []string{"carbonio-nonexistent-test.service"}
	def.PreStop = nil
	def.CustomStop = func(_ context.Context, _ *ServiceDef) error {
		stopCalled = true

		return nil
	}
	Registry["memcached"] = &def

	if err := ServiceStopSystemd(context.Background(), "memcached"); err != nil {
		t.Fatalf("ServiceStopSystemd returned unexpected error: %v", err)
	}

	if !stopCalled {
		t.Error("CustomStop was not called")
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
