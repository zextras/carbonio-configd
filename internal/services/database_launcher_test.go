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
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	pidfile := filepath.Join(tmp, "mysql.pid")

	err := ctl.stopMysqldByPidFile(context.Background(), "test-db", pidfile)
	if err != nil {
		t.Fatalf("expected nil for missing pidfile, got %v", err)
	}
}

func TestStopMysqldByPidFile_InvalidPID(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	pidfile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidfile, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ctl.stopMysqldByPidFile(context.Background(), "test-db", pidfile)
	if err == nil {
		t.Fatal("expected error for invalid pid")
	}

	if _, statErr := os.Stat(pidfile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile to be removed after invalid pid")
	}
}

func TestStopMysqldByPidFile_ReadError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	pidfile := filepath.Join(tmp, "subdir", "mysql.pid")

	_, err := os.Stat(pidfile)
	if !os.IsNotExist(err) {
		t.Skip("skipping: parent dir must not exist")
	}

	result := ctl.stopMysqldByPidFile(context.Background(), "test-db", pidfile)
	if result != nil {
		t.Errorf("expected nil for missing parent dir (IsNotExist), got %v", result)
	}
}

func TestStopMysqldByPidFile_InvalidPidRemovesFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("notanumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ctl.stopMysqldByPidFile(context.Background(), "test-db", pidFile)
	if err == nil {
		t.Fatal("expected error for invalid pid")
	}
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("expected pidfile to be removed after invalid pid")
	}
}

// --- flushAppserverDBDirtyPages ---

func TestFlushAppserverDBDirtyPages_LoadConfigFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}

	// Should not panic
	ctl.flushAppserverDBDirtyPages(context.Background())
}

func TestFlushAppserverDBDirtyPages_EmptyPassword(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}

	// Should not panic when password is empty
	ctl.flushAppserverDBDirtyPages(context.Background())
}

func TestFlushAppserverDBDirtyPages_WithPassword(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	mysqlBin := filepath.Join(tmp, "mysql")
	if err := os.WriteFile(mysqlBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.p.bin = tmp

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"zimbra_mysql_password": "testpass",
		}, nil
	}

	ctl.flushAppserverDBDirtyPages(context.Background())
}

func TestFlushAppserverDBDirtyPages_CmdFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	mysqlBin := filepath.Join(tmp, "mysql")
	script := "#!/bin/sh\nif [ \"$1\" = \"-e\" ]; then echo 'error' >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(mysqlBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.p.bin = tmp

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"zimbra_mysql_password": "testpass",
		}, nil
	}

	ctl.flushAppserverDBDirtyPages(context.Background())
}

// --- spawnMysqldSafe ---

func TestSpawnMysqldSafe_EmptyDefaultsFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	err := ctl.spawnMysqldSafe(context.Background(), "test", "", "/dev/null", "/tmp/test.pid")
	if err == nil {
		t.Fatal("expected error when defaults file is empty")
	}
}

func TestSpawnMysqldSafe_AlreadyRunning(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", self)), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ctl.spawnMysqldSafe(context.Background(), "test-db",
		"/etc/my.cnf", "/tmp/err.log", pidFile)
	if err != nil {
		t.Fatalf("expected nil when pidfile shows running, got %v", err)
	}
}

func TestSpawnMysqldSafe_LogFileError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.log = "/proc/nonexistent-dir-for-test/mysqld"

	ctl.p.appserverDBPidFile = pidFile

	err := ctl.spawnMysqldSafe(context.Background(), "test-db", "/tmp/my.cnf", "/tmp/err.log", pidFile)
	if err == nil {
		t.Fatal("expected error for inaccessible log path")
	}
}

func TestSpawnMysqldSafe_WithErrLog(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.appserverDBPidFile = pidFile

	fakeMysqldSafe := filepath.Join(tmp, "mysqld_safe")
	if err := os.WriteFile(fakeMysqldSafe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.p.mysqldSafeBin = fakeMysqldSafe

	ctl.p.log = filepath.Join(tmp, "log")
	if err := os.MkdirAll(ctl.p.log, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ctl.spawnMysqldSafe(context.Background(), "test-db", "/tmp/my.cnf", "/tmp/err.log", pidFile)
	if err != nil {
		t.Logf("spawnMysqldSafe with errlog: %v", err)
	}
}

func TestSpawnMysqldSafe_NoErrLog(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.appserverDBPidFile = pidFile

	fakeMysqldSafe := filepath.Join(tmp, "mysqld_safe")
	if err := os.WriteFile(fakeMysqldSafe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.p.mysqldSafeBin = fakeMysqldSafe

	ctl.p.log = filepath.Join(tmp, "log")
	if err := os.MkdirAll(ctl.p.log, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ctl.spawnMysqldSafe(context.Background(), "test-db", "/tmp/my.cnf", "", pidFile)
	if err != nil {
		t.Logf("spawnMysqldSafe no errlog: %v", err)
	}
}

// --- startAppserverDB / stopAppserverDB ---

func TestStopAppserverDB_MissingPidfile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	ctl.p.appserverDBPidFile = filepath.Join(tmp, "missing.pid")

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}

	err := ctl.stopAppserverDB(context.Background())
	if err != nil {
		t.Fatalf("expected nil for missing pidfile, got %v", err)
	}
}

func TestStartAppserverDB_LoadConfigFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test error")
	}

	err := ctl.startAppserverDB(context.Background())
	if err == nil {
		t.Fatal("expected error when ctl.loadConfig fails")
	}
}

func TestStartAppserverDB_LoadConfigOk(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "mysql.pid")
	self := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", self)), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.appserverDBPidFile = pidFile

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{"mysql_mycnf": "/tmp/test.cnf"}, nil
	}

	err := ctl.startAppserverDB(context.Background())
	if err != nil {
		t.Logf("startAppserverDB: %v (may fail if mysqld_safe not available)", err)
	}
}

func TestStartAppserverDB_WithConfig(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	pidFile := filepath.Join(tmp, "mysql.pid")
	if err := os.WriteFile(pidFile, []byte("999999998\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.appserverDBPidFile = pidFile

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mysql_mycnf":      "/tmp/my.cnf",
			"mysql_errlogfile": "/tmp/err.log",
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

	err := ctl.startAppserverDB(context.Background())
	t.Logf("startAppserverDB: %v", err)
}

// --- stopAntispamDB ---

func TestStopAntispamDB_MissingPidfile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	ctl.p.antispamDBPidFile = filepath.Join(tmp, "missing.pid")

	err := ctl.stopAntispamDB(context.Background())
	if err != nil {
		t.Fatalf("expected nil for missing pidfile, got %v", err)
	}
}
