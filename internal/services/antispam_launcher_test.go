// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// --- antispamDBEnabled ---

func TestAntispamDBEnabled_Disabled(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "FALSE",
		}, nil
	}

	if ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected false when antispam_mysql_enabled=FALSE")
	}
}

func TestAntispamDBEnabled_EnabledLocalhost(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "localhost",
		}, nil
	}

	if !ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected true for localhost")
	}
}

func TestAntispamDBEnabled_Enabled127(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "127.0.0.1",
		}, nil
	}

	if !ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected true for 127.0.0.1")
	}
}

func TestAntispamDBEnabled_MatchingHostname(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	ctl.p.bin = tmp

	if err := os.WriteFile(filepath.Join(tmp, "zmhostname"), []byte("#!/bin/sh\necho testhost\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "testhost",
		}, nil
	}

	if !ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected true when hostname matches")
	}
}

func TestAntispamDBEnabled_LoadConfigFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}

	if ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected false when ctl.loadConfig fails")
	}
}

func TestAntispamDBEnabled_EmptyHost(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
		}, nil
	}

	if ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected false when host is empty")
	}
}

func TestAntispamDBEnabled_HostnameMismatch(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	ctl.p.bin = tmp

	if err := os.WriteFile(filepath.Join(tmp, "zmhostname"), []byte("#!/bin/sh\necho realhost\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "otherhost",
		}, nil
	}

	if ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected false when hostname does not match")
	}
}

func TestAntispamDBEnabled_ZmhostnameFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "somehost",
		}, nil
	}

	ctl.p.bin = "/nonexistent-bin-dir-for-test"

	if ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected false when zmhostname fails")
	}
}

func TestAntispamDBEnabled_LCError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{}, fmt.Errorf("no config")
	}

	if ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected false when ctl.loadConfig fails")
	}
}

func TestAntispamDBEnabled_WithConfig(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "amavisd-mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.antispamDBPidFile = pidFile

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_mycnf":      "/tmp/antispam.cnf",
			"antispam_mysql_enabled":    "TRUE",
			"antispam_mysql_host":       "127.0.0.1",
			"antispam_mysql_errlogfile": "/tmp/antispam-err.log",
		}, nil
	}

	if !ctl.antispamDBEnabled(context.Background()) {
		t.Error("expected antispamDBEnabled to return true")
	}
}

// --- antispamCustomStart / antispamCustomStop ---

func TestAntispamCustomStart_Disabled(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "FALSE",
		}, nil
	}

	err := ctl.antispamCustomStart(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected nil when disabled, got %v", err)
	}
}

func TestAntispamCustomStop_Disabled(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "FALSE",
		}, nil
	}

	err := ctl.antispamCustomStop(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected nil when disabled, got %v", err)
	}
}

func TestAntispamCustomStart_EnabledButLocal(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "127.0.0.1",
		}, nil
	}

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "amavisd-mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.antispamDBPidFile = pidFile

	err := ctl.antispamCustomStart(context.Background(), nil)
	t.Logf("antispamCustomStart returned: %v", err)
}

// --- startAntispamDB ---

func TestStartAntispamDB_EmptyMycnf(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}

	err := ctl.startAntispamDB(context.Background())
	if err != nil {
		t.Fatalf("expected nil when mycnf empty, got %v", err)
	}
}

func TestStartAntispamDB_WithConfig(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "amavisd-mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.antispamDBPidFile = pidFile

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_mycnf":      "/tmp/antispam.cnf",
			"antispam_mysql_errlogfile": "/tmp/antispam-err.log",
		}, nil
	}

	fakeMysqldSafe := filepath.Join(tmp, "mysqld_safe")
	if err := os.WriteFile(fakeMysqldSafe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.p.mysqldSafeBin = fakeMysqldSafe

	ctl.p.log = filepath.Join(tmp, "log")
	if err := os.MkdirAll(ctl.p.log, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ctl.startAntispamDB(context.Background())
	t.Logf("startAntispamDB: %v", err)
}
