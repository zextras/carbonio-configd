// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/systemd"
)

func stubSystemctl(t *testing.T, bin string) {
	t.Helper()

	orig := systemctlBin
	systemctlBin = bin

	t.Cleanup(func() { systemctlBin = orig })
}

func withStatusUnit(t *testing.T) {
	t.Helper()

	withMode(t, true)

	orig := Registry["memcached"]

	t.Cleanup(func() { Registry["memcached"] = orig })

	def := *orig
	def.SystemdUnits = []string{"carbonio-stub.service"}
	def.PidFile = ""
	def.ProcessName = ""
	Registry["memcached"] = &def
}

// A non-zero systemctl exit (inactive/unknown unit) means "not running".
func TestServiceStatus_IsActiveNonZeroExit_NotRunning(t *testing.T) {
	if !systemd.IsBooted() {
		t.Skip("requires a systemd-booted host")
	}

	withStatusUnit(t)

	bin := filepath.Join(t.TempDir(), "systemctl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stubSystemctl(t, bin)

	running, err := ServiceStatus(context.Background(), "memcached")
	if err != nil || running {
		t.Errorf("ServiceStatus = %v, %v; want false, nil", running, err)
	}
}

// An exec failure must surface as an error, not as "service down".
func TestServiceStatus_SystemctlExecFailure_ReturnsError(t *testing.T) {
	if !systemd.IsBooted() {
		t.Skip("requires a systemd-booted host")
	}

	withStatusUnit(t)
	stubSystemctl(t, filepath.Join(t.TempDir(), "missing-systemctl"))

	running, err := ServiceStatus(context.Background(), "memcached")
	if err == nil {
		t.Fatal("expected an error when systemctl cannot be executed")
	}

	if running {
		t.Error("running must be false alongside an error")
	}
}

// A cancelled context kills systemctl; that is an error, not "inactive".
func TestServiceStatus_ContextCancelled_ReturnsError(t *testing.T) {
	if !systemd.IsBooted() {
		t.Skip("requires a systemd-booted host")
	}

	withStatusUnit(t)

	bin := filepath.Join(t.TempDir(), "systemctl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stubSystemctl(t, bin)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ServiceStatus(ctx, "memcached"); err == nil {
		t.Error("expected an error for a cancelled context")
	}
}

func TestWithDefaultSSHPort(t *testing.T) {
	cases := map[string]string{
		"example.com":      "example.com:22",
		"example.com:2222": "example.com:2222",
		"10.0.0.1":         "10.0.0.1:22",
		"::1":              "[::1]:22",
		"2001:db8::5":      "[2001:db8::5]:22",
		"[::1]":            "[::1]:22",
		"[::1]:2200":       "[::1]:2200",
	}

	for in, want := range cases {
		if got := withDefaultSSHPort(in); got != want {
			t.Errorf("withDefaultSSHPort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteHost_RejectsShellMetacharacters(t *testing.T) {
	bad := []string{"a|b", "a`b`", "a'b", `a"b`, "a(b)", "a>b", "a<b", "a b", "a\tb", "a\\b", "a*b", "a#b", "$(id)"}

	for _, name := range bad {
		if err := RemoteHostStart(context.Background(), "127.0.0.1", name); err == nil {
			t.Errorf("RemoteHostStart accepted service %q", name)
		}

		if err := remoteExec(context.Background(), "127.0.0.1", name, "mta", "ok"); err == nil {
			t.Errorf("remoteExec accepted action %q", name)
		}
	}
}
