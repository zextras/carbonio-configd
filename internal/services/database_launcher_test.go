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

// --- stopMysqldByPidFile ---

func TestStopMysqldByPidFile_Missing(t *testing.T) {
	tmp := t.TempDir()
	pidfile := filepath.Join(tmp, "mysql.pid")

	err := stopMysqldByPidFile(context.Background(), "test-db", pidfile)
	if err != nil {
		t.Fatalf("expected nil for missing pidfile, got %v", err)
	}
}

func TestStopMysqldByPidFile_InvalidPID(t *testing.T) {
	tmp := t.TempDir()
	pidfile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidfile, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := stopMysqldByPidFile(context.Background(), "test-db", pidfile)
	if err == nil {
		t.Fatal("expected error for invalid pid")
	}

	if _, statErr := os.Stat(pidfile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile to be removed after invalid pid")
	}
}

func TestStopMysqldByPidFile_ReadError(t *testing.T) {
	tmp := t.TempDir()
	pidfile := filepath.Join(tmp, "subdir", "mysql.pid")

	_, err := os.Stat(pidfile)
	if !os.IsNotExist(err) {
		t.Skip("skipping: parent dir must not exist")
	}

	result := stopMysqldByPidFile(context.Background(), "test-db", pidfile)
	if result != nil {
		t.Errorf("expected nil for missing parent dir (IsNotExist), got %v", result)
	}
}

func TestStopMysqldByPidFile_InvalidPidRemovesFile(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := stopMysqldByPidFile(context.Background(), "test-db", pidFile)
	if err == nil {
		t.Fatal("expected error for invalid pid")
	}
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile to be removed after invalid pid")
	}
}

// --- flushAppserverDBDirtyPages ---

func TestFlushAppserverDBDirtyPages_LoadConfigFails(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}
	defer func() { loadConfig = oldLC }()

	// Should not panic
	flushAppserverDBDirtyPages(context.Background())
}

func TestFlushAppserverDBDirtyPages_EmptyPassword(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	// Should not panic when password is empty
	flushAppserverDBDirtyPages(context.Background())
}

func TestFlushAppserverDBDirtyPages_WithPassword(t *testing.T) {
	tmp := t.TempDir()
	mysqlBin := filepath.Join(tmp, "mysql")
	if err := os.WriteFile(mysqlBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldBinPath := binPath
	binPath = tmp
	defer func() { binPath = oldBinPath }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"zimbra_mysql_password": "testpass",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	flushAppserverDBDirtyPages(context.Background())
}

func TestFlushAppserverDBDirtyPages_CmdFails(t *testing.T) {
	tmp := t.TempDir()
	mysqlBin := filepath.Join(tmp, "mysql")
	script := "#!/bin/sh\nif [ \"$1\" = \"-e\" ]; then echo 'error' >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(mysqlBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	oldBin := binPath
	binPath = tmp
	defer func() { binPath = oldBin }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"zimbra_mysql_password": "testpass",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	flushAppserverDBDirtyPages(context.Background())
}

// --- spawnMysqldSafe ---

func TestSpawnMysqldSafe_EmptyDefaultsFile(t *testing.T) {
	err := spawnMysqldSafe(context.Background(), "test", "", "/dev/null", "/tmp/test.pid")
	if err == nil {
		t.Fatal("expected error when defaults file is empty")
	}
}

func TestSpawnMysqldSafe_AlreadyRunning(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", self)), 0o644); err != nil {
		t.Fatal(err)
	}

	err := spawnMysqldSafe(context.Background(), "test-db",
		"/etc/my.cnf", "/tmp/err.log", pidFile)
	if err != nil {
		t.Fatalf("expected nil when pidfile shows running, got %v", err)
	}
}

func TestSpawnMysqldSafe_LogFileError(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldLog := logPath
	logPath = "/proc/nonexistent-dir-for-test/mysqld"
	defer func() { logPath = oldLog }()

	oldPidFile := appserverDBPidFile
	appserverDBPidFile = pidFile
	defer func() { appserverDBPidFile = oldPidFile }()

	err := spawnMysqldSafe(context.Background(), "test-db", "/tmp/my.cnf", "/tmp/err.log", pidFile)
	if err == nil {
		t.Fatal("expected error for inaccessible log path")
	}
}

func TestSpawnMysqldSafe_WithErrLog(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPidFile := appserverDBPidFile
	appserverDBPidFile = pidFile
	defer func() { appserverDBPidFile = oldPidFile }()

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

	err := spawnMysqldSafe(context.Background(), "test-db", "/tmp/my.cnf", "/tmp/err.log", pidFile)
	if err != nil {
		t.Logf("spawnMysqldSafe with errlog: %v", err)
	}
}

func TestSpawnMysqldSafe_NoErrLog(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPidFile := appserverDBPidFile
	appserverDBPidFile = pidFile
	defer func() { appserverDBPidFile = oldPidFile }()

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

	err := spawnMysqldSafe(context.Background(), "test-db", "/tmp/my.cnf", "", pidFile)
	if err != nil {
		t.Logf("spawnMysqldSafe no errlog: %v", err)
	}
}

// --- startAppserverDB / stopAppserverDB ---

func TestStopAppserverDB_MissingPidfile(t *testing.T) {
	tmp := t.TempDir()
	oldPidFile := appserverDBPidFile
	appserverDBPidFile = filepath.Join(tmp, "missing.pid")
	defer func() { appserverDBPidFile = oldPidFile }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := stopAppserverDB(context.Background())
	if err != nil {
		t.Fatalf("expected nil for missing pidfile, got %v", err)
	}
}

func TestStartAppserverDB_LoadConfigFails(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}
	defer func() { loadConfig = oldLC }()

	err := startAppserverDB(context.Background())
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
}

func TestStartAppserverDB_LoadConfigOk(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", self)), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPidFile := appserverDBPidFile
	appserverDBPidFile = pidFile
	defer func() { appserverDBPidFile = oldPidFile }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{"mysql_mycnf": "/tmp/test.cnf"}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := startAppserverDB(context.Background())
	if err != nil {
		t.Logf("startAppserverDB: %v (may fail if mysqld_safe not available)", err)
	}
}

func TestStartAppserverDB_WithConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPidFile := appserverDBPidFile
	appserverDBPidFile = pidFile
	defer func() { appserverDBPidFile = oldPidFile }()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mysql_mycnf":      "/tmp/my.cnf",
			"mysql_errlogfile": "/tmp/err.log",
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

	err := startAppserverDB(context.Background())
	t.Logf("startAppserverDB: %v", err)
}

// --- stopAntispamDB ---

func TestStopAntispamDB_MissingPidfile(t *testing.T) {
	tmp := t.TempDir()
	oldPidFile := antispamDBPidFile
	antispamDBPidFile = filepath.Join(tmp, "missing.pid")
	defer func() { antispamDBPidFile = oldPidFile }()

	err := stopAntispamDB(context.Background())
	if err != nil {
		t.Fatalf("expected nil for missing pidfile, got %v", err)
	}
}
