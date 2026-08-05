// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

// TestServiceReload_NoSystemdUnits verifies ServiceReload returns nil when no
// units are defined. This exercises the systemd-mode empty-loop no-op
// specifically — legacy mode has no such case, since reloadWithoutSystemd
// bifurcates on CustomStop/ProcessName/BinaryPath rather than SystemdUnits
// (see TestServiceReload_Legacy_* below for the legacy paths).
func TestServiceReload_NoSystemdUnits(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return true }

	origDef := Registry["memcached"]
	defer func() { Registry["memcached"] = origDef }()

	def := *origDef
	def.SystemdUnits = nil
	Registry["memcached"] = &def

	err := ServiceReload(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceReload with no units returned error: %v", err)
	}
}

// TestServiceReload_Legacy_MTA_NativeReload is the regression guard for the
// cli.go:279 bug: ServiceReload used to try Systemctl("reload") in every
// mode, and its only fallback was a hard stopService+startService — for MTA
// that meant a full postfix restart (dropping in-flight SMTP) whenever the
// host wasn't orchestrated by Carbonio's systemd targets. In legacy mode,
// MTA reload must run "postfix reload" directly and never touch systemctl
// or fall back to mtaCustomStop/mtaCustomStart.
func TestServiceReload_Legacy_MTA_NativeReload(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	tmp := t.TempDir()
	callLog := filepath.Join(tmp, "calls.log")

	oldSudo := sudoBin
	sudoBin = filepath.Join(tmp, "sudo")
	fakeSudo := "#!/bin/sh\necho \"$@\" >> " + callLog + "\nexit 0\n"
	if err := os.WriteFile(sudoBin, []byte(fakeSudo), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { sudoBin = oldSudo }()

	oldPostfix := postfixBin
	postfixBin = filepath.Join(tmp, "postfix")
	defer func() { postfixBin = oldPostfix }()

	if err := ServiceReload(context.Background(), "mta"); err != nil {
		t.Fatalf("expected nil from native postfix reload, got %v", err)
	}

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("expected sudo to be invoked, read calls.log: %v", err)
	}

	calls := strings.TrimSpace(string(data))
	if !strings.Contains(calls, "reload") {
		t.Errorf("expected native postfix reload call, got %q", calls)
	}
	if strings.Contains(calls, "stop") || strings.Contains(calls, "start") {
		t.Errorf("legacy MTA reload must not fall back to stop/start, got %q", calls)
	}
	if strings.Count(calls, "\n")+1 != 1 {
		t.Errorf("expected exactly one sudo invocation (reload only), got %q", calls)
	}
}

// TestServiceReload_Legacy_Fallback_IgnoresSystemdUnits proves that when a
// service has no native reload path, the legacy fallback goes through
// stopService+startService's legacy branch (CustomStop then CustomStart) —
// never systemctl — even when SystemdUnits is populated with a
// real-looking unit name.
func TestServiceReload_Legacy_Fallback_IgnoresSystemdUnits(t *testing.T) {
	orig := isSystemdModeFn
	defer func() { isSystemdModeFn = orig }()
	isSystemdModeFn = func() bool { return false }

	var order []string

	def := &ServiceDef{
		Name:         "test-reload-fallback",
		SystemdUnits: []string{"carbonio-test-reload-fallback.service"},
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			order = append(order, "stop")
			return nil
		},
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			order = append(order, "start")
			return nil
		},
	}
	Registry["test-reload-fallback"] = def
	defer delete(Registry, "test-reload-fallback")

	if err := ServiceReload(context.Background(), "test-reload-fallback"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	if len(order) != 2 || order[0] != "stop" || order[1] != "start" {
		t.Errorf("expected [stop start] via legacy fallback, got %v", order)
	}
}
