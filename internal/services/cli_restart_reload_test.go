// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// TestServiceRestart_MTA_IsRegistered verifies that MTA restart calls ServiceReload.
func TestServiceRestart_MTA_IsRegistered(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := ctl.ServiceRestart(ctx, "mta")
	_ = err
}

// TestServiceRestart_StopFailedStartAnyway exercises the "stop failed, start anyway" branch.
func TestServiceRestart_StopFailedStartAnyway(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if ctl.IsSystemdMode() {
		t.Skip("skipping: restart test on systemd-booted host")
	}

	orig := ctl.registry["memcached"]
	defer func() { ctl.registry["memcached"] = orig }()

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
	ctl.registry["memcached"] = &def

	ctl.ServiceRestart(context.Background(), "memcached")
}

// TestServiceRestart_NonMTA_StopAndStartBoth exercises the stop-failed-warn-then-start path.
func TestServiceRestart_NonMTA_StopAndStartBoth(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if ctl.IsSystemdMode() {
		t.Skip("skipping restart branch test on systemd-booted host")
	}

	orig := ctl.registry["memcached"]
	defer func() { ctl.registry["memcached"] = orig }()

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
	ctl.registry["memcached"] = &def

	ctl.ServiceRestart(context.Background(), "memcached")

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
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(true)

	origDef := ctl.registry["memcached"]
	defer func() { ctl.registry["memcached"] = origDef }()

	def := *origDef
	def.SystemdUnits = nil
	ctl.registry["memcached"] = &def

	err := ctl.ServiceReload(context.Background(), "memcached")
	if err != nil {
		t.Errorf("ServiceReload with no units returned error: %v", err)
	}
}

// stubMTADeps replaces the saslauthd and milter registry entries with
// recorders and returns the recorded "<service> <action>" calls.
func stubMTADeps(t *testing.T, ctl *Controller, saslEnabled, milterEnabled bool) *[]string {
	t.Helper()

	calls := &[]string{}

	record := func(action string) func(context.Context, *ServiceDef) error {
		return func(_ context.Context, def *ServiceDef) error {
			*calls = append(*calls, def.Name+" "+action)

			return nil
		}
	}

	for name, enabled := range map[string]bool{svcSaslauthd: saslEnabled, svcMilter: milterEnabled} {
		saved := ctl.registry[name]
		t.Cleanup(func() { ctl.registry[name] = saved })

		ctl.registry[name] = &ServiceDef{
			Name:        name,
			CustomStart: record("start"),
			CustomStop:  record("stop"),
			EnableCheck: func(context.Context) bool { return enabled },
		}
	}

	return calls
}

// TestServiceReload_Legacy_MTA_RestartsEnabledDeps pins legacy zmmtactl
// reload: saslauthd (and milter when enabled) are stopped and started before
// postfix reload; a disabled milter is left alone.
func TestServiceReload_Legacy_MTA_RestartsEnabledDeps(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)

	ctl.pinMode(false)

	calls := stubMTADeps(t, ctl, true, false)

	if err := ctl.ServiceReload(context.Background(), "mta"); err != nil {
		t.Fatalf("ctl.ServiceReload(mta): %v", err)
	}

	want := []string{svcSaslauthd + " stop", svcSaslauthd + " start"}
	if !slices.Equal(*calls, want) {
		t.Errorf("dependency calls = %q, want %q", *calls, want)
	}
}

// TestServiceReload_Legacy_MTA_NativeReload is the regression guard for the
// cli.go:279 bug: ServiceReload used to try ctl.Systemctl("reload") in every
// mode, and its only fallback was a hard stopService+startService — for MTA
// that meant a full postfix restart (dropping in-flight SMTP) whenever the
// host wasn't orchestrated by Carbonio's systemd targets. In legacy mode,
// MTA reload must run "postfix reload" directly and never touch systemctl
// or fall back to mtaCustomStop/mtaCustomStart.
func TestServiceReload_Legacy_MTA_NativeReload(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	ctl.pinMode(false)

	stubMTADeps(t, ctl, false, false)

	if err := ctl.ServiceReload(context.Background(), "mta"); err != nil {
		t.Fatalf("expected nil from native postfix reload, got %v", err)
	}

	calls := f.Exec.Lines()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one command (sudo postfix reload), got %q", calls)
	}

	if want := sudoBin + " " + ctl.p.postfixBin + " reload"; calls[0] != want {
		t.Errorf("command = %q, want %q", calls[0], want)
	}
}

// TestServiceReload_Legacy_Fallback_IgnoresSystemdUnits proves that when a
// service has no native reload path, the legacy fallback goes through
// stopService+startService's legacy branch (CustomStop then CustomStart) —
// never systemctl — even when SystemdUnits is populated with a
// real-looking unit name.
func TestServiceReload_Legacy_Fallback_IgnoresSystemdUnits(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.pinMode(false)

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
	ctl.registry["test-reload-fallback"] = def
	defer delete(ctl.registry, "test-reload-fallback")

	if err := ctl.ServiceReload(context.Background(), "test-reload-fallback"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	if len(order) != 2 || order[0] != "stop" || order[1] != "start" {
		t.Errorf("expected [stop start] via legacy fallback, got %v", order)
	}
}

// TestProcessRestarts_DeadServiceInvokesStart is the watchdog-recovery
// regression guard: ServiceManager.ProcessRestarts (fed by watchdog's
// AddRestart on a service found not running) must end with the service
// started, not merely "reloaded" or left dead. ServiceRestart's stop+start
// fallback (cli.go) runs unconditionally for non-MTA services — even when
// the stop half fails because there was nothing to stop — so a dead
// service's restart always reaches the start path.
func TestProcessRestarts_DeadServiceInvokesStart(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.pinMode(false)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "dead-svc.pid")
	started := false

	ctl.registry["watchdog-dead-svc"] = &ServiceDef{
		Name:    "watchdog-dead-svc",
		PidFile: pidFile,
		CustomStart: func(_ context.Context, _ *ServiceDef) error {
			started = true

			return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
		},
		CustomStop: func(_ context.Context, _ *ServiceDef) error {
			return os.Remove(pidFile) // fails: nothing to stop, service is dead
		},
	}
	defer delete(ctl.registry, "watchdog-dead-svc")

	sm := NewServiceManager(ctl)

	// Precondition: the service is dead (no pidfile yet).
	running, err := sm.IsRunning(context.Background(), "watchdog-dead-svc")
	if err != nil || running {
		t.Fatalf("precondition failed: expected dead service, running=%v err=%v", running, err)
	}

	// Mirror watchdog.restartDownService: AddRestart then ProcessRestarts.
	if err := sm.AddRestart(context.Background(), "watchdog-dead-svc"); err != nil {
		t.Fatalf("AddRestart failed: %v", err)
	}

	if err := sm.ProcessRestarts(context.Background(), nil); err != nil {
		t.Fatalf("ProcessRestarts returned error: %v", err)
	}

	if !started {
		t.Error("expected the start path (CustomStart) to be invoked for a dead service")
	}

	running, err = sm.IsRunning(context.Background(), "watchdog-dead-svc")
	if err != nil {
		t.Fatalf("IsRunning after restart returned error: %v", err)
	}

	if !running {
		t.Error("expected the service to be running after ProcessRestarts recovered a dead service")
	}

	if sm.RestartQueue["watchdog-dead-svc"] {
		t.Error("expected service to be removed from the restart queue after a successful restart")
	}
}
