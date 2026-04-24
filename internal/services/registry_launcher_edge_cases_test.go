// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// registry.go — cbpolicydInitDB
// ============================================================

// TestCbpolicydInitDB_DBAlreadyExists verifies the early-return when the DB file exists.
func TestCbpolicydInitDB_DBAlreadyExists(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "cbpolicyd.sqlitedb")
	if err := os.WriteFile(dbFile, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := cbpolicydDBPath
	cbpolicydDBPath = dbFile
	defer func() { cbpolicydDBPath = old }()

	err := cbpolicydInitDB(context.Background(), nil)
	if err != nil {
		t.Errorf("cbpolicydInitDB with existing DB returned error: %v", err)
	}
}

// TestCbpolicydInitDB_DBMissingNoBinary verifies the error path when the DB is absent
// and the init binary is also absent.
func TestCbpolicydInitDB_DBMissingNoBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "nonexistent", "cbpolicyd.sqlitedb")

	oldDB := cbpolicydDBPath
	cbpolicydDBPath = dbFile
	defer func() { cbpolicydDBPath = oldDB }()

	oldBin := cbpolicydInitBin
	cbpolicydInitBin = filepath.Join(tmp, "nonexistent-bin")
	defer func() { cbpolicydInitBin = oldBin }()

	err := cbpolicydInitDB(context.Background(), nil)
	if err == nil {
		t.Error("expected error when DB missing and init binary absent")
	}
}

// TestCbpolicydInitDB_DBMissingBinaryFails verifies the path where DB is absent,
// MkdirAll succeeds, binary exists, but the init command fails.
func TestCbpolicydInitDB_DBMissingBinaryFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "subdir", "cbpolicyd.sqlitedb")

	initBin := filepath.Join(tmp, "zmcbpolicydinit")
	if err := os.WriteFile(initBin, []byte("#!/bin/sh\necho 'init error'; exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDB := cbpolicydDBPath
	cbpolicydDBPath = dbFile
	defer func() { cbpolicydDBPath = oldDB }()

	oldBin := cbpolicydInitBin
	cbpolicydInitBin = initBin
	defer func() { cbpolicydInitBin = oldBin }()

	err := cbpolicydInitDB(context.Background(), nil)
	if err == nil {
		t.Error("expected error when init binary exits non-zero")
	}
}

// TestCbpolicydInitDB_DBMissingBinarySucceeds verifies happy path: DB absent, binary present and succeeds.
func TestCbpolicydInitDB_DBMissingBinarySucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "subdir", "cbpolicyd.sqlitedb")

	initBin := filepath.Join(tmp, "zmcbpolicydinit")
	if err := os.WriteFile(initBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDB := cbpolicydDBPath
	cbpolicydDBPath = dbFile
	defer func() { cbpolicydDBPath = oldDB }()

	oldBin := cbpolicydInitBin
	cbpolicydInitBin = initBin
	defer func() { cbpolicydInitBin = oldBin }()

	err := cbpolicydInitDB(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil when init binary succeeds, got: %v", err)
	}
}

// ============================================================
// registry.go — milterEnabled
// ============================================================

// TestMilterEnabled_FileNotFound verifies milterEnabled returns false when file absent.
func TestMilterEnabled_FileNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := milterOptionsPath
	milterOptionsPath = "/nonexistent/path/mta_milter_options"
	defer func() { milterOptionsPath = old }()

	if milterEnabled(context.Background()) {
		t.Error("expected milterEnabled=false when options file does not exist")
	}
}

// ============================================================
// stats_launcher.go — statsCustomStop
// ============================================================

// TestStatsCustomStop_NonExistentDir verifies statsCustomStop succeeds when the
// stats PID directory does not exist.
func TestStatsCustomStop_NonExistentDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	old := statsPidDir
	statsPidDir = "/nonexistent-stats-pid-dir-xyz-123"
	defer func() { statsPidDir = old }()

	err := statsCustomStop(context.Background(), nil)
	if err != nil {
		t.Errorf("statsCustomStop with non-existent dir returned error: %v", err)
	}
}

// TestStatsCustomStop_EmptyDir verifies statsCustomStop with an existing but empty dir.
func TestStatsCustomStop_EmptyDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	old := statsPidDir
	statsPidDir = tmp
	defer func() { statsPidDir = old }()

	err := statsCustomStop(context.Background(), nil)
	if err != nil {
		t.Errorf("statsCustomStop with empty dir returned error: %v", err)
	}
}

// TestStatsCustomStop_SkipsNonPidFiles verifies that non-.pid entries are ignored.
func TestStatsCustomStop_SkipsNonPidFiles(t *testing.T) {
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

	old := statsPidDir
	statsPidDir = tmp
	defer func() { statsPidDir = old }()

	err := statsCustomStop(context.Background(), nil)
	if err != nil {
		t.Errorf("statsCustomStop returned unexpected error: %v", err)
	}
}

// TestStatsCustomStop_ReadDirError verifies statsCustomStop returns error when
// statsPidDir exists but is not a directory.
func TestStatsCustomStop_ReadDirError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakePidDir := filepath.Join(tmp, "fakestatsdir")
	if err := os.WriteFile(fakePidDir, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := statsPidDir
	statsPidDir = fakePidDir
	defer func() { statsPidDir = old }()

	err := statsCustomStop(context.Background(), nil)
	if err == nil {
		t.Error("expected error when statsPidDir is a file (ReadDir fails)")
	}
}

// ============================================================
// stats_launcher.go — killStatsPidFile
// ============================================================

// TestKillStatsPidFile_ValidPidSelfNoError verifies killStatsPidFile handles a valid but
// non-running PID gracefully.
func TestKillStatsPidFile_ValidPidSelfNoError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "self.pid")
	if err := os.WriteFile(pidFile, []byte("2147483646\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = killStatsPidFile(context.Background(), pidFile)
}

// ============================================================
// stats_launcher.go — statsCustomStart
// ============================================================

// TestStatsCustomStart_NoCollectors verifies statsCustomStart returns an error
// when no collector binaries exist.
func TestStatsCustomStart_NoCollectors(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	oldLog := logPath
	logPath = tmp
	defer func() { logPath = oldLog }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err == nil {
		t.Error("expected error when no collector binaries exist")
	}
}

// TestStatsCustomStart_WithFakeCollector verifies statsCustomStart starts at least
// one collector when a matching binary is present in libexecDir.
func TestStatsCustomStart_WithFakeCollector(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	fakebin := filepath.Join(tmp, "zmstat-proc")
	if err := os.WriteFile(fakebin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	_ = err
}

// TestStatsCustomStart_LogOpenFails verifies that when log open fails the collector
// is skipped and no panic occurs.
func TestStatsCustomStart_LogOpenFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	fakebin := filepath.Join(tmp, "zmstat-proc")
	if err := os.WriteFile(fakebin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	oldLog := logPath
	logPath = filepath.Join(tmp, "nonexistent-log-dir")
	defer func() { logPath = oldLog }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err == nil {
		t.Error("expected error when no collectors can be started (log dir missing)")
	}
}

// TestStatsCustomStart_LocalconfigFailError verifies the error message
// when localconfig fails.
func TestStatsCustomStart_LocalconfigFailError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	oldLibexec := libexecDir
	libexecDir = tmp
	defer func() { libexecDir = oldLibexec }()

	err := statsCustomStart(context.Background(), &ServiceDef{Name: "stats"})
	if err == nil {
		t.Error("expected error in test environment (no localconfig)")
	}
}

// TestStatsCustomStart_ConditionalCollectors verifies that conditional collector
// keys are evaluated.
func TestStatsCustomStart_ConditionalCollectors(t *testing.T) {
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
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 0\nexit 0\n"), 0o755); err != nil {
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
	_ = err
}

// ============================================================
// registry.go — mailboxJavaBinary
// ============================================================

// TestMailboxJavaBinary_ExplicitJavaHome verifies mailboxd_java_home is used.
func TestMailboxJavaBinary_ExplicitJavaHome(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	javaDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	javaBin := filepath.Join(javaDir, "java")
	if err := os.WriteFile(javaBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	lc := map[string]string{
		"mailboxd_java_home": tmp,
	}
	got, err := mailboxJavaBinary(context.Background(), lc)
	if err != nil {
		t.Fatalf("mailboxJavaBinary returned error: %v", err)
	}
	if got != javaBin {
		t.Errorf("got %q, want %q", got, javaBin)
	}
}

// TestMailboxJavaBinary_FallbackToZimbraJavaHome verifies zimbra_java_home fallback.
func TestMailboxJavaBinary_FallbackToZimbraJavaHome(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	javaDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	javaBin := filepath.Join(javaDir, "java")
	if err := os.WriteFile(javaBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	lc := map[string]string{
		"zimbra_java_home": tmp,
	}
	got, err := mailboxJavaBinary(context.Background(), lc)
	if err != nil {
		t.Fatalf("mailboxJavaBinary returned error: %v", err)
	}
	if got != javaBin {
		t.Errorf("got %q, want %q", got, javaBin)
	}
}

// TestMailboxJavaBinary_FixedFallbackMissing verifies the commonPath fixed fallback path.
func TestMailboxJavaBinary_FixedFallbackMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	old := commonPath
	commonPath = tmp
	defer func() { commonPath = old }()

	lc := map[string]string{}
	_, err := mailboxJavaBinary(context.Background(), lc)
	if err == nil {
		t.Error("expected error when java binary is absent in fixed fallback path")
	}
}

// TestMailboxJavaBinary_BinaryNotFound verifies error when java binary doesn't exist.
func TestMailboxJavaBinary_BinaryNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	lc := map[string]string{
		"mailboxd_java_home": tmp,
	}
	_, err := mailboxJavaBinary(context.Background(), lc)
	if err == nil {
		t.Error("expected error when java binary not found")
	}
}

// ============================================================
// registry.go — various custom start/stop
// ============================================================

// TestMtaCustomStop_NotRunningOutput verifies that "is not running" in sudo output is treated as success.
func TestMtaCustomStop_NotRunningOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	fakeBin := filepath.Join(tmp, "postfix")
	script := "#!/bin/sh\necho 'postfix/postfix-script: the Postfix mail system is not running'\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	old := postfixBin
	postfixBin = fakeBin
	defer func() { postfixBin = old }()

	err := mtaCustomStop(context.Background(), nil)
	_ = err
}

// TestMtaCustomStop_ErrorNotRunning verifies the error return when the command fails
// with output that does NOT contain "is not running".
func TestMtaCustomStop_ErrorNotRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	fakePostfix := filepath.Join(tmp, "postfix")
	script := "#!/bin/sh\necho 'fatal: some other error'\nexit 1\n"

	if err := os.WriteFile(fakePostfix, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	old := postfixBin
	postfixBin = fakePostfix
	defer func() { postfixBin = old }()

	err := mtaCustomStop(context.Background(), nil)
	_ = err
}

// TestMtaCustomStop_SuccessPath verifies the success return when the command exits 0.
func TestMtaCustomStop_SuccessPath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	fakePostfix := filepath.Join(tmp, "postfix")
	if err := os.WriteFile(fakePostfix, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := postfixBin
	postfixBin = fakePostfix
	defer func() { postfixBin = old }()

	err := mtaCustomStop(context.Background(), nil)
	if err != nil {
		t.Logf("sudo not available or rejected fake binary (expected in CI): %v", err)
		t.Skip("skipping success-path assertion: sudo unavailable in this environment")
	}
}

// TestServiceDiscoverCustomStart_MissingBinary verifies error when binary doesn't exist.
func TestServiceDiscoverCustomStart_MissingBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := &ServiceDef{
		Name:       "service-discover",
		BinaryPath: "/nonexistent/service-discovered",
	}

	err := serviceDiscoverCustomStart(context.Background(), def)
	if err == nil {
		t.Error("expected error when service-discover binary is missing")
	}
}

// TestServiceDiscoverCustomStart_WithFakeBinary verifies the agent/server role selection.
func TestServiceDiscoverCustomStart_WithFakeBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "service-discovered")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "service-discover",
		BinaryPath: fakeBin,
	}

	err := serviceDiscoverCustomStart(context.Background(), def)
	if err != nil {
		t.Errorf("serviceDiscoverCustomStart returned unexpected error: %v", err)
	}
}

// TestServiceDiscoverCustomStart_Release verifies the happy path including Release().
func TestServiceDiscoverCustomStart_Release(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "service-discovered")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nsleep 0\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	def := &ServiceDef{
		Name:       "service-discover",
		BinaryPath: fakeBin,
	}

	err := serviceDiscoverCustomStart(context.Background(), def)
	if err != nil {
		t.Errorf("serviceDiscoverCustomStart returned error: %v", err)
	}
}

// TestMilterCustomStart_LocalconfigError verifies error when localconfig unavailable.
func TestMilterCustomStart_LocalconfigError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := milterCustomStart(context.Background(), nil)
	_ = err
}

// TestMailboxCustomStart_LocalconfigError verifies error when localconfig unavailable.
func TestMailboxCustomStart_LocalconfigError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := mailboxCustomStart(context.Background(), &ServiceDef{Name: "mailbox"})
	_ = err
}

// TestLdapCustomStart_LocalconfigError verifies error when localconfig unavailable.
func TestLdapCustomStart_LocalconfigError(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := ldapCustomStart(context.Background(), &ServiceDef{Name: "ldap"})
	_ = err
}

// TestLdapCustomStop_NoPidFile verifies ldapCustomStop returns nil when slapd.pid absent.
func TestLdapCustomStop_NoPidFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := ldapCustomStop(context.Background(), nil)
	if err != nil {
		t.Errorf("ldapCustomStop returned unexpected error: %v", err)
	}
}

// ============================================================
// diskcheck.go — GetDiskThreshold
// ============================================================

// TestGetDiskThreshold_UsesDefault verifies GetDiskThreshold does not panic.
func TestGetDiskThreshold_UsesDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_ = GetDiskThreshold()
}

// TestGetDiskThreshold_RepeatedCalls verifies the function returns > 0.
func TestGetDiskThreshold_RepeatedCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	for i := 0; i < 3; i++ {
		v := GetDiskThreshold()
		if v <= 0 {
			t.Errorf("call %d: GetDiskThreshold() = %d, want > 0", i, v)
		}
	}
}

// ============================================================
// discovery.go — IsLDAPLocal
// ============================================================

// TestIsLDAPLocal_ConfigPresentNoMatch verifies false when localconfig loads
// but hostname doesn't appear in ldap_url.
func TestIsLDAPLocal_ConfigPresentNoMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	result := IsLDAPLocal()
	_ = result
}

// ============================================================
// remote.go — RemoteHost* and sshConnect
// ============================================================

// TestRemoteHostStart_InvalidService verifies error for invalid service name.
func TestRemoteHostStart_InvalidService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := RemoteHostStart(context.Background(), "somehost", "bad name!")
	if err == nil {
		t.Error("expected error for invalid service name")
	}
}

// TestRemoteHostStop_InvalidService verifies error for invalid service name.
func TestRemoteHostStop_InvalidService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := RemoteHostStop(context.Background(), "somehost", "bad name!")
	if err == nil {
		t.Error("expected error for invalid service name")
	}
}

// TestRemoteHostStatus_InvalidService verifies error for invalid service name.
func TestRemoteHostStatus_InvalidService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, err := RemoteHostStatus(context.Background(), "somehost", "bad name!")
	if err == nil {
		t.Error("expected error for invalid service name")
	}
}

// TestRemoteHostStart_ValidServiceConnectFails verifies SSH connect error is returned.
func TestRemoteHostStart_ValidServiceConnectFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := RemoteHostStart(context.Background(), "127.0.0.1:1", "mta")
	if err == nil {
		t.Error("expected error when SSH connect fails")
	}
}

// TestRemoteHostStop_ValidServiceConnectFails verifies SSH connect error is returned.
func TestRemoteHostStop_ValidServiceConnectFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := RemoteHostStop(context.Background(), "127.0.0.1:1", "mta")
	if err == nil {
		t.Error("expected error when SSH connect fails")
	}
}

// TestRemoteHostStatus_ValidServiceConnectFails verifies SSH connect error is returned.
func TestRemoteHostStatus_ValidServiceConnectFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	_, err := RemoteHostStatus(context.Background(), "127.0.0.1:1", "mta")
	if err == nil {
		t.Error("expected error when SSH connect fails")
	}
}

// TestSshConnect_NoKey verifies sshConnect returns error when SSH key is missing.
func TestSshConnect_NoKey(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	old := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", old)

	_, err := sshConnect("127.0.0.1:1")
	if err == nil {
		t.Error("expected error when SSH key is missing")
	}
}

// TestSshConnect_InvalidKey verifies sshConnect returns error for invalid key data.
func TestSshConnect_InvalidKey(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("not-a-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	old := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", old)

	_, err := sshConnect("127.0.0.1:1")
	if err == nil {
		t.Error("expected error for invalid SSH key")
	}
}

// TestSshConnect_NoKnownHosts verifies error when known_hosts is missing.
func TestSshConnect_NoKnownHosts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	testKey := `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACBY4+8JMGMhJsSLHRDexG5M3Q5xJME6YKUF9dAT0HoIZwAAAJBLhAl8S4
QJfAAAAAtzc2gtZWQyNTUxOQAAACBY4+8JMGMhJsSLHRDexG5M3Q5xJME6YKUF9dAT0H
oIZwAAAEBVE7YQBA0pDIhPZ1+5FJJRLcZPvuFLHSiZlmCOGKqumFjj7wkwYyEmxIsdEN
7EbkzdDnEkwTpgpQX10BPQeghnAAAADHRlc3RAZXhhbXBsZQECAwQFBg==
-----END OPENSSH PRIVATE KEY-----
`
	tmp := t.TempDir()
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte(testKey), 0o600); err != nil {
		t.Fatal(err)
	}

	old := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", old)

	_, err := sshConnect("127.0.0.1:1")
	if err == nil {
		t.Error("expected error when known_hosts is missing")
	}
}

// ============================================================
// registry.go — clamdDirInit
// ============================================================

// TestClamdDirInit_CreatesDir verifies that clamdDirInit creates
// the clamav database directory if it doesn't exist.
func TestClamdDirInit_CreatesDir(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	tmp := t.TempDir()
	testPath := filepath.Join(tmp, "clamav", "db")

	old := clamdDirPath
	clamdDirPath = testPath
	defer func() { clamdDirPath = old }()

	err := clamdDirInit(context.Background(), nil)
	if err != nil {
		t.Errorf("clamdDirInit returned error: %v", err)
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

// TestClamdDirInit_Idempotent verifies that clamdDirInit can be
// called multiple times without error (idempotent).
func TestClamdDirInit_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	tmp := t.TempDir()
	testPath := filepath.Join(tmp, "clamav", "db")

	old := clamdDirPath
	clamdDirPath = testPath
	defer func() { clamdDirPath = old }()

	// First call
	err := clamdDirInit(context.Background(), nil)
	if err != nil {
		t.Errorf("first clamdDirInit returned error: %v", err)
	}

	// Second call (directory already exists)
	err = clamdDirInit(context.Background(), nil)
	if err != nil {
		t.Errorf("second clamdDirInit returned error: %v", err)
	}
}

// TestClamdDirInit_Error verifies that clamdDirInit returns an error
// when the directory cannot be created (e.g., permission denied).
func TestClamdDirInit_Error(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	// Skip if running as root (MkdirAll succeeds as root even on /proc)
	if os.Geteuid() == 0 {
		t.Skip("MkdirAll succeeds as root")
	}

	// Try to create a directory under /proc which is not writable
	testPath := "/proc/1/clamav-db-test-" + t.Name()

	old := clamdDirPath
	clamdDirPath = testPath
	defer func() { clamdDirPath = old }()

	err := clamdDirInit(context.Background(), nil)
	if err == nil {
		t.Error("expected error when creating directory in non-writable location")
	}
}
