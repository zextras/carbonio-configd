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
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "FALSE",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	if antispamDBEnabled(context.Background()) {
		t.Error("expected false when antispam_mysql_enabled=FALSE")
	}
}

func TestAntispamDBEnabled_EnabledLocalhost(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "localhost",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	if !antispamDBEnabled(context.Background()) {
		t.Error("expected true for localhost")
	}
}

func TestAntispamDBEnabled_Enabled127(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "127.0.0.1",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	if !antispamDBEnabled(context.Background()) {
		t.Error("expected true for 127.0.0.1")
	}
}

func TestAntispamDBEnabled_MatchingHostname(t *testing.T) {
	tmp := t.TempDir()
	oldBin := binPath
	binPath = tmp
	defer func() { binPath = oldBin }()

	if err := os.WriteFile(filepath.Join(tmp, "zmhostname"), []byte("#!/bin/sh\necho testhost\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "testhost",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	if !antispamDBEnabled(context.Background()) {
		t.Error("expected true when hostname matches")
	}
}

func TestAntispamDBEnabled_LoadConfigFails(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}
	defer func() { loadConfig = oldLC }()

	if antispamDBEnabled(context.Background()) {
		t.Error("expected false when loadConfig fails")
	}
}

func TestAntispamDBEnabled_EmptyHost(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	if antispamDBEnabled(context.Background()) {
		t.Error("expected false when host is empty")
	}
}

func TestAntispamDBEnabled_HostnameMismatch(t *testing.T) {
	tmp := t.TempDir()
	oldBin := binPath
	binPath = tmp
	defer func() { binPath = oldBin }()

	if err := os.WriteFile(filepath.Join(tmp, "zmhostname"), []byte("#!/bin/sh\necho realhost\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "otherhost",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	if antispamDBEnabled(context.Background()) {
		t.Error("expected false when hostname does not match")
	}
}

func TestAntispamDBEnabled_ZmhostnameFails(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "somehost",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldBin := binPath
	binPath = "/nonexistent-bin-dir-for-test"
	defer func() { binPath = oldBin }()

	if antispamDBEnabled(context.Background()) {
		t.Error("expected false when zmhostname fails")
	}
}

func TestAntispamDBEnabled_LCError(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, fmt.Errorf("no config")
	}
	defer func() { loadConfig = oldLC }()

	if antispamDBEnabled(context.Background()) {
		t.Error("expected false when loadConfig fails")
	}
}

func TestAntispamDBEnabled_WithConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "amavisd-mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPidFile := antispamDBPidFile
	antispamDBPidFile = pidFile
	defer func() { antispamDBPidFile = oldPidFile }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_mycnf":      "/tmp/antispam.cnf",
			"antispam_mysql_enabled":    "TRUE",
			"antispam_mysql_host":       "127.0.0.1",
			"antispam_mysql_errlogfile": "/tmp/antispam-err.log",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	if !antispamDBEnabled(context.Background()) {
		t.Error("expected antispamDBEnabled to return true")
	}
}

// --- antispamCustomStart / antispamCustomStop ---

func TestAntispamCustomStart_Disabled(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "FALSE",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := antispamCustomStart(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected nil when disabled, got %v", err)
	}
}

func TestAntispamCustomStop_Disabled(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "FALSE",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := antispamCustomStop(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected nil when disabled, got %v", err)
	}
}

func TestAntispamCustomStart_EnabledButLocal(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_enabled": "TRUE",
			"antispam_mysql_host":    "127.0.0.1",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "amavisd-mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPidFile := antispamDBPidFile
	antispamDBPidFile = pidFile
	defer func() { antispamDBPidFile = oldPidFile }()

	err := antispamCustomStart(context.Background(), nil)
	t.Logf("antispamCustomStart returned: %v", err)
}

// --- startAntispamDB ---

func TestStartAntispamDB_EmptyMycnf(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := startAntispamDB(context.Background())
	if err != nil {
		t.Fatalf("expected nil when mycnf empty, got %v", err)
	}
}

func TestStartAntispamDB_WithConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "amavisd-mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPidFile := antispamDBPidFile
	antispamDBPidFile = pidFile
	defer func() { antispamDBPidFile = oldPidFile }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"antispam_mysql_mycnf":      "/tmp/antispam.cnf",
			"antispam_mysql_errlogfile": "/tmp/antispam-err.log",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	fakeMysqldSafe := filepath.Join(tmp, "mysqld_safe")
	if err := os.WriteFile(fakeMysqldSafe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldBin := mysqldSafeBin
	mysqldSafeBin = fakeMysqldSafe
	defer func() { mysqldSafeBin = oldBin }()

	oldLog := logPath
	logPath = filepath.Join(tmp, "log")
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { logPath = oldLog }()

	err := startAntispamDB(context.Background())
	t.Logf("startAntispamDB: %v", err)
}
