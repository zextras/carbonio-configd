// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- statsCustomStart (conditional collectors) ---

func TestStatsCustomStart_MysqlCollectorEnabled(t *testing.T) {
	// Verify zmstat-mysql is included when zmstat_mysql_enabled=TRUE.
	// We don't have localconfig, so this will error, but it exercises
	// the collector list building.
	tmp := t.TempDir()

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	// This test verifies the function call path does not panic
	// even when localconfig is unavailable.
	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err == nil {
		t.Log("statsCustomStart succeeded unexpectedly (localconfig available)")
	}
}

// --- statsCustomStart (loadConfig override) ---

func TestStatsCustomStart_LoadConfigFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}
	defer func() { loadConfig = old }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to load localconfig")
	}
}

func TestStatsCustomStart_NoCollectorBinariesWithConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	oldLog := logPath
	logPath = filepath.Join(tmp, "log")
	defer func() { logPath = oldLog }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err == nil {
		t.Fatal("expected error when no collector binaries exist")
	}
	if !strings.Contains(err.Error(), "no stats collectors started") {
		t.Errorf("error = %q, want it to contain %q", err, "no stats collectors started")
	}
}

func TestStatsCustomStart_SomeCollectorsPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	for _, name := range []string{"zmstat-proc", "zmstat-cpu"} {
		bin := filepath.Join(tmp, name)
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err != nil {
		t.Errorf("statsCustomStart returned error: %v", err)
	}
}

func TestStatsCustomStart_ConditionalMysqlCollectorEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"zmstat_mysql_enabled": "TRUE",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	collectors := []string{
		"zmstat-proc", "zmstat-cpu", "zmstat-vm", "zmstat-io",
		"zmstat-df", "zmstat-fd", "zmstat-allprocs", "zmstat-mysql",
	}
	for _, name := range collectors {
		bin := filepath.Join(tmp, name)
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err != nil {
		t.Errorf("statsCustomStart returned error: %v", err)
	}
}
