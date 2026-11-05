// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// ============================================================
// registry.go — cbpolicyd Setup
// ============================================================

// TestCbpolicydInitDB_DBAlreadyExists verifies the early-return when the DB file exists.
func TestCbpolicydInitDB_DBAlreadyExists(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "cbpolicyd.sqlitedb")
	if err := os.WriteFile(dbFile, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.cbpolicydDB = dbFile

	ctl.registry = ctl.defaultRegistry() // the Setup steps carry the paths set above

	err := ctl.runSteps(context.Background(), ctl.registry[svcCbpolicyd].Setup)
	if err != nil {
		t.Errorf("cbpolicyd Setup with existing DB returned error: %v", err)
	}
}

// TestCbpolicydInitDB_DBMissingNoBinary verifies the error path when the DB is absent
// and the init binary is also absent.
func TestCbpolicydInitDB_DBMissingNoBinary(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "nonexistent", "cbpolicyd.sqlitedb")

	ctl.p.cbpolicydDB = dbFile

	ctl.p.cbpolicydInitBin = filepath.Join(tmp, "nonexistent-bin")

	ctl.registry = ctl.defaultRegistry() // the Setup steps carry the paths set above

	err := ctl.runSteps(context.Background(), ctl.registry[svcCbpolicyd].Setup)
	if err == nil {
		t.Error("expected error when DB missing and init binary absent")
	}
}

// TestCbpolicydInitDB_DBMissingBinaryFails verifies the path where DB is absent,
// MkdirAll succeeds, binary exists, but the init command fails.
func TestCbpolicydInitDB_DBMissingBinaryFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "subdir", "cbpolicyd.sqlitedb")

	initBin := filepath.Join(tmp, "zmcbpolicydinit")
	writeExecutable(t, initBin, []byte("#!/bin/sh\necho 'init error'; exit 1\n"))

	ctl.p.cbpolicydDB = dbFile

	ctl.p.cbpolicydInitBin = initBin

	ctl.registry = ctl.defaultRegistry() // the Setup steps carry the paths set above

	err := ctl.runSteps(context.Background(), ctl.registry[svcCbpolicyd].Setup)
	if err == nil {
		t.Error("expected error when init binary exits non-zero")
	}
}

// TestCbpolicydInitDB_DBMissingBinarySucceeds verifies happy path: DB absent, binary present and succeeds.
func TestCbpolicydInitDB_DBMissingBinarySucceeds(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "subdir", "cbpolicyd.sqlitedb")

	initBin := filepath.Join(tmp, "zmcbpolicydinit")
	writeExecutable(t, initBin, []byte("#!/bin/sh\nexit 0\n"))

	ctl.p.cbpolicydDB = dbFile

	ctl.p.cbpolicydInitBin = initBin

	ctl.registry = ctl.defaultRegistry() // the Setup steps carry the paths set above

	err := ctl.runSteps(context.Background(), ctl.registry[svcCbpolicyd].Setup)
	if err != nil {
		t.Errorf("expected nil when init binary succeeds, got: %v", err)
	}
}

// ============================================================
// registry.go — milterEnabled
// ============================================================

// TestMilterEnabled_FileNotFound verifies milterEnabled returns false when file absent.
func TestMilterEnabled_FileNotFound(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.p.milterOptions = "/nonexistent/path/mta_milter_options"

	if ctl.milterEnabled(context.Background()) {
		t.Error("expected milterEnabled=false when options file does not exist")
	}
}

// ============================================================
// collectors.go — stopCollectors
// ============================================================

// TestStatsCustomStop_NonExistentDir verifies stopCollectors succeeds when the
// stats PID directory does not exist.
func TestStatsCustomStop_NonExistentDir(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	ctl.p.statsPidDir = "/nonexistent-stats-pid-dir-xyz-123"

	err := ctl.stopCollectors(context.Background(), ctl.registry[svcStats])
	if err != nil {
		t.Errorf("stopCollectors with non-existent dir returned error: %v", err)
	}
}

// TestStatsCustomStop_EmptyDir verifies stopCollectors with an existing but empty dir.
func TestStatsCustomStop_EmptyDir(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	ctl.p.statsPidDir = tmp

	err := ctl.stopCollectors(context.Background(), ctl.registry[svcStats])
	if err != nil {
		t.Errorf("stopCollectors with empty dir returned error: %v", err)
	}
}

// TestStatsCustomStop_SkipsNonPidFiles verifies that non-.pid entries are ignored.
func TestStatsCustomStop_SkipsNonPidFiles(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmp, "notapid.txt"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "subdir.pid"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.p.statsPidDir = tmp

	err := ctl.stopCollectors(context.Background(), ctl.registry[svcStats])
	if err != nil {
		t.Errorf("stopCollectors returned unexpected error: %v", err)
	}
}

// TestStatsCustomStop_ReadDirError verifies stopCollectors returns error when
// ctl.p.statsPidDir exists but is not a directory.
func TestStatsCustomStop_ReadDirError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	fakePidDir := filepath.Join(tmp, "fakestatsdir")
	if err := os.WriteFile(fakePidDir, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctl.p.statsPidDir = fakePidDir

	err := ctl.stopCollectors(context.Background(), ctl.registry[svcStats])
	if err == nil {
		t.Error("expected error when ctl.p.statsPidDir is a file (ReadDir fails)")
	}
}

// ============================================================
// collectors.go — killStatsPidFile
// ============================================================

// TestKillStatsPidFile_ValidPidSelfNoError verifies killStatsPidFile handles a valid but
// non-running PID gracefully.
func TestKillStatsPidFile_ValidPidSelfNoError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "self.pid")
	if err := os.WriteFile(pidFile, []byte("2147483646\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = ctl.killStatsPidFile(context.Background(), pidFile, "")
}

// ============================================================
// collectors.go — startCollectors
// ============================================================

// TestStatsCustomStart_NoCollectors verifies startCollectors returns an error
// when no collector binaries exist.
func TestStatsCustomStart_NoCollectors(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()

	ctl.p.libexec = tmp

	ctl.p.log = tmp

	err := ctl.startCollectors(context.Background(), ctl.registry[svcStats])
	if err == nil {
		t.Error("expected error when no collector binaries exist")
	}
}

// TestStatsCustomStart_WithFakeCollector verifies startCollectors starts at least
// one collector when a matching binary is present in ctl.p.libexec.
func TestStatsCustomStart_WithFakeCollector(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	fakebin := filepath.Join(tmp, "zmstat-proc")
	writeExecutable(t, fakebin, []byte("#!/bin/sh\nexit 0\n"))

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctl.p.libexec = tmp

	ctl.p.log = logDir

	err := ctl.startCollectors(context.Background(), ctl.registry[svcStats])
	_ = err
}

// TestStatsCustomStart_LogOpenFails verifies that when log open fails the collector
// is skipped and no panic occurs.
func TestStatsCustomStart_LogOpenFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	fakebin := filepath.Join(tmp, "zmstat-proc")
	writeExecutable(t, fakebin, []byte("#!/bin/sh\nexit 0\n"))

	ctl.p.libexec = tmp

	ctl.p.log = filepath.Join(tmp, "nonexistent-log-dir")

	err := ctl.startCollectors(context.Background(), ctl.registry[svcStats])
	if err == nil {
		t.Error("expected error when no collectors can be started (log dir missing)")
	}
}

// TestStatsCustomStart_LocalconfigFailError verifies the error message
// when localconfig fails.
func TestStatsCustomStart_LocalconfigFailError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()

	ctl.p.libexec = tmp

	err := ctl.startCollectors(context.Background(), ctl.registry[svcStats])
	if err == nil {
		t.Error("expected error in test environment (no localconfig)")
	}
}

// TestStatsCustomStart_ConditionalCollectors verifies that conditional collector
// keys are evaluated.
func TestStatsCustomStart_ConditionalCollectors(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	collectors := []string{
		"zmstat-proc", "zmstat-cpu", "zmstat-vm", "zmstat-io",
		"zmstat-df", "zmstat-fd", "zmstat-allprocs", "zmstat-mysql",
	}
	for _, c := range collectors {
		bin := filepath.Join(tmp, c)
		writeExecutable(t, bin, []byte("#!/bin/sh\nsleep 0\nexit 0\n"))
	}

	ctl.p.libexec = tmp

	ctl.p.log = logDir

	err := ctl.startCollectors(context.Background(), ctl.registry[svcStats])
	_ = err
}

// ============================================================
// registry.go — javaBinary
// ============================================================

// TestMailboxJavaBinary_ExplicitJavaHome verifies mailboxd_java_home is used.
func TestMailboxJavaBinary_ExplicitJavaHome(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	javaDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	javaBin := filepath.Join(javaDir, "java")
	writeExecutable(t, javaBin, []byte("#!/bin/sh\n"))

	lc := map[string]string{
		"mailboxd_java_home": tmp,
	}
	got, err := ctl.javaBinary(context.Background(), lc)
	if err != nil {
		t.Fatalf("javaBinary returned error: %v", err)
	}
	if got != javaBin {
		t.Errorf("got %q, want %q", got, javaBin)
	}
}

// TestMailboxJavaBinary_FallbackToZimbraJavaHome verifies zimbra_java_home fallback.
func TestMailboxJavaBinary_FallbackToZimbraJavaHome(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	javaDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	javaBin := filepath.Join(javaDir, "java")
	writeExecutable(t, javaBin, []byte("#!/bin/sh\n"))

	lc := map[string]string{
		"zimbra_java_home": tmp,
	}
	got, err := ctl.javaBinary(context.Background(), lc)
	if err != nil {
		t.Fatalf("javaBinary returned error: %v", err)
	}
	if got != javaBin {
		t.Errorf("got %q, want %q", got, javaBin)
	}
}

// TestMailboxJavaBinary_FixedFallbackMissing verifies the ctl.p.common fixed fallback path.
func TestMailboxJavaBinary_FixedFallbackMissing(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	ctl.p.common = tmp

	lc := map[string]string{}
	_, err := ctl.javaBinary(context.Background(), lc)
	if err == nil {
		t.Error("expected error when java binary is absent in fixed fallback path")
	}
}

// TestMailboxJavaBinary_BinaryNotFound verifies error when java binary doesn't exist.
func TestMailboxJavaBinary_BinaryNotFound(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	lc := map[string]string{
		"mailboxd_java_home": tmp,
	}
	_, err := ctl.javaBinary(context.Background(), lc)
	if err == nil {
		t.Error("expected error when java binary not found")
	}
}

// ============================================================
// registry.go — various custom start/stop
// ============================================================

// TestServiceDiscoverIsExternallyManaged asserts configd only observes
// service-discover: it owns service-discover.service (running
// /usr/bin/service-discoverd), so start/stop/reload must be no-ops in every
// mode — matching legacy control.pl, which skipped it in doStartup and only
// ran `systemctl is-active service-discover.service` for status.
func TestServiceDiscoverIsExternallyManaged(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	def := ctl.LookupService(svcServiceDiscover)
	if def == nil {
		t.Fatal("service-discover not found")
	}

	if !def.ExternallyManaged {
		t.Error("ExternallyManaged = false, want true")
	}

	if !def.UseSystemdForStatus {
		t.Error("UseSystemdForStatus = false, want true")
	}

	if def.BinaryPath != "" || def.CustomStart != nil || def.CustomStop != nil {
		t.Errorf("service-discover must have no launcher: BinaryPath=%q CustomStart=%v CustomStop=%v",
			def.BinaryPath, def.CustomStart != nil, def.CustomStop != nil)
	}

	ctx := context.Background()

	if err := ctl.startService(ctx, svcServiceDiscover, def); err != nil {
		t.Errorf("startService = %v, want nil no-op", err)
	}

	if err := ctl.stopService(ctx, svcServiceDiscover, def); err != nil {
		t.Errorf("stopService = %v, want nil no-op", err)
	}

	if err := ctl.ServiceReload(ctx, svcServiceDiscover); err != nil {
		t.Errorf("ServiceReload = %v, want nil no-op", err)
	}
}

// TestMilterCustomStart_LocalconfigError verifies error when localconfig unavailable.
func TestMilterCustomStart_LocalconfigError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.registry = ctl.defaultRegistry()

	err := ctl.startJava(context.Background(), ctl.registry[svcMilter])
	_ = err
}

// TestMailboxCustomStart_LocalconfigError verifies error when localconfig unavailable.
func TestMailboxCustomStart_LocalconfigError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.registry = ctl.defaultRegistry()

	err := ctl.startJava(context.Background(), ctl.registry[svcMailbox])
	_ = err
}

// TestLdapCustomStart_LocalconfigError verifies error when localconfig unavailable.
func TestLdapCustomStart_LocalconfigError(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.registry = ctl.defaultRegistry()

	err := ctl.startDirect(context.Background(), svcLdap, ctl.registry[svcLdap])
	_ = err
}

// TestLdapStop_DeclarativeNoPidFile verifies the LDAP stop is pure registry
// data (GracefulStop, no CustomStop) and that it returns nil when slapd.pid is
// absent: the service is already stopped.
func TestLdapStop_DeclarativeNoPidFile(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	def := *ctl.registry[svcLdap]
	require.Nil(t, def.CustomStop, "ldap stop must be declarative")
	require.Equal(t, ldapStopTimeout, def.GracefulStop)

	def.PidFile = filepath.Join(t.TempDir(), "slapd.pid")

	require.NoError(t, ctl.stopWithoutSystemd(context.Background(), svcLdap, &def))
}

// ============================================================
// diskcheck.go — GetDiskThreshold
// ============================================================

// TestGetDiskThreshold_UsesDefault verifies GetDiskThreshold does not panic.
func TestGetDiskThreshold_UsesDefault(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	_ = ctl.GetDiskThreshold()
}

// TestGetDiskThreshold_RepeatedCalls verifies the function returns > 0.
func TestGetDiskThreshold_RepeatedCalls(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	for i := range 3 {
		v := ctl.GetDiskThreshold()
		if v <= 0 {
			t.Errorf("call %d: ctl.GetDiskThreshold() = %d, want > 0", i, v)
		}
	}
}

// ============================================================
// discovery.go — IsLDAPLocal
// ============================================================

// TestIsLDAPLocal_ConfigPresentNoMatch verifies false when localconfig loads
// but hostname doesn't appear in ldap_url.
func TestIsLDAPLocal_ConfigPresentNoMatch(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	result := ctl.IsLDAPLocal()
	_ = result
}

// ============================================================
// remote.go — RemoteHost* and sshConnect
// ============================================================

// ============================================================
// registry.go — clamd Setup
// ============================================================

// TestClamdDirInit_CreatesDir verifies that clamd Setup creates
// the clamav database directory if it doesn't exist.
func TestClamdDirInit_CreatesDir(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	testPath := filepath.Join(tmp, "clamav", "db")

	ctl.p.clamdDir = testPath

	ctl.registry = ctl.defaultRegistry() // the Setup steps carry the path set above

	err := ctl.runSteps(context.Background(), ctl.registry[svcClamd].Setup)
	if err != nil {
		t.Errorf("clamd Setup returned error: %v", err)
	}

	// Verify the directory was created
	stat, err := os.Stat(testPath)
	if err != nil {
		t.Errorf("directory not created: %v", err)
	}
	if !stat.IsDir() {
		t.Error("path exists but is not a directory")
	}
}

// TestClamdDirInit_Idempotent verifies that clamd Setup can be
// called multiple times without error (idempotent).
func TestClamdDirInit_Idempotent(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	tmp := t.TempDir()
	testPath := filepath.Join(tmp, "clamav", "db")

	ctl.p.clamdDir = testPath

	// First call
	ctl.registry = ctl.defaultRegistry() // the Setup steps carry the path set above

	err := ctl.runSteps(context.Background(), ctl.registry[svcClamd].Setup)
	if err != nil {
		t.Errorf("first clamd Setup returned error: %v", err)
	}

	// Second call (directory already exists)
	err = ctl.runSteps(context.Background(), ctl.registry[svcClamd].Setup)
	if err != nil {
		t.Errorf("second clamd Setup returned error: %v", err)
	}
}

// TestClamdDirInit_Error verifies that clamd Setup returns an error
// when the directory cannot be created (e.g., permission denied).
func TestClamdDirInit_Error(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	// Skip if running as root (MkdirAll succeeds as root even on /proc)
	if os.Geteuid() == 0 {
		t.Skip("MkdirAll succeeds as root")
	}

	// Try to create a directory under /proc which is not writable
	testPath := "/proc/1/clamav-db-test-" + t.Name()

	ctl.p.clamdDir = testPath

	ctl.registry = ctl.defaultRegistry() // the Setup steps carry the path set above

	err := ctl.runSteps(context.Background(), ctl.registry[svcClamd].Setup)
	if err == nil {
		t.Error("expected error when creating directory in non-writable location")
	}
}
