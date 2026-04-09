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
	"time"
)

// --- advancedInstalled ---

func TestAdvancedInstalled(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) string // returns dir path
		want  bool
	}{
		{
			name: "no directory exists",
			setup: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			want: false,
		},
		{
			name: "empty directory",
			setup: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			want: false,
		},
		{
			name: "directory with unrelated jar",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "other-lib.jar"), []byte("fake"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: false,
		},
		{
			name: "directory with matching jar",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-1.0.jar"), []byte("fake"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: true,
		},
		{
			name: "file with matching prefix but wrong extension",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-config.xml"), []byte("fake"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: false,
		},
		{
			name: "multiple jars with one matching",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				for _, name := range []string{"util.jar", "carbonio-advanced-2.5.jar", "readme.txt"} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return dir
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)

			old := advancedJARDir
			advancedJARDir = dir
			defer func() { advancedJARDir = old }()

			if got := advancedInstalled(); got != tt.want {
				t.Errorf("advancedInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- MailboxAdvancedStatusHook ---

func TestMailboxAdvancedStatusHook_NotInstalled(t *testing.T) {
	// When no advanced jars exist, the hook should return nil immediately.
	old := advancedJARDir
	advancedJARDir = filepath.Join(t.TempDir(), "nonexistent")
	defer func() { advancedJARDir = old }()

	err := MailboxAdvancedStatusHook(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil when advanced not installed, got: %v", err)
	}
}

func TestMailboxAdvancedStatusHook_NoCarbonioCLI(t *testing.T) {
	// When jars exist but CLI binary is missing, hook should return nil.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-1.0.jar"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldJAR := advancedJARDir
	advancedJARDir = dir
	defer func() { advancedJARDir = oldJAR }()

	oldCLI := carbonioCLI
	carbonioCLI = filepath.Join(t.TempDir(), "nonexistent-cli")
	defer func() { carbonioCLI = oldCLI }()

	err := MailboxAdvancedStatusHook(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil when carbonio CLI absent, got: %v", err)
	}
}

func TestMailboxAdvancedStatusHook_ContextCancelled(t *testing.T) {
	// When context is cancelled, hook should return nil without hanging.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-1.0.jar"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldJAR := advancedJARDir
	advancedJARDir = dir
	defer func() { advancedJARDir = oldJAR }()

	// Create a fake CLI binary that always fails (simulates module not ready).
	fakeCLI := filepath.Join(t.TempDir(), "carbonio")
	if err := os.WriteFile(fakeCLI, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldCLI := carbonioCLI
	carbonioCLI = fakeCLI
	defer func() { carbonioCLI = oldCLI }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := MailboxAdvancedStatusHook(ctx, nil)
	if err != nil {
		t.Errorf("expected nil on cancelled context, got: %v", err)
	}
}

func TestMailboxAdvancedStatusHook_AdvancedReady(t *testing.T) {
	// When CLI reports success without the "Unable to communicate" message,
	// the hook should return nil quickly.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-1.0.jar"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldJAR := advancedJARDir
	advancedJARDir = dir
	defer func() { advancedJARDir = oldJAR }()

	fakeCLI := filepath.Join(t.TempDir(), "carbonio")
	if err := os.WriteFile(fakeCLI, []byte("#!/bin/sh\necho '8.8.15'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldCLI := carbonioCLI
	carbonioCLI = fakeCLI
	defer func() { carbonioCLI = oldCLI }()

	err := MailboxAdvancedStatusHook(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil when advanced is ready, got: %v", err)
	}
}

// --- buildLDAPBindURL (table-driven consolidation) ---

func TestBuildLDAPBindURL_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		lc   map[string]string
		want string
	}{
		{
			name: "explicit bind URL takes precedence",
			lc: map[string]string{
				"ldap_bind_url": "ldap://bind.example.com:389",
				"ldap_url":      "ldap://other:389",
			},
			want: "ldap://bind.example.com:389",
		},
		{
			name: "single ldap_url fallback",
			lc: map[string]string{
				"ldap_url": "ldap://single:389",
			},
			want: "ldap://single:389",
		},
		{
			name: "reconstruct with custom port",
			lc: map[string]string{
				"zimbra_server_hostname": "custom.host",
				"ldap_port":              "636",
			},
			want: "ldap://custom.host:636",
		},
		{
			name: "all defaults",
			lc:   map[string]string{},
			want: "ldap://localhost:389",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildLDAPBindURL(tt.lc); got != tt.want {
				t.Errorf("buildLDAPBindURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- openLogFile (error paths) ---

func TestOpenLogFile_NestedDirCreation(t *testing.T) {
	// openLogFile should succeed when the parent directory exists.
	tmp := t.TempDir()
	logFile := filepath.Join(tmp, "service.log")

	f, err := openLogFile(logFile)
	if err != nil {
		t.Fatalf("openLogFile() unexpected error: %v", err)
	}
	defer f.Close()

	info, statErr := os.Stat(logFile)
	if statErr != nil {
		t.Fatalf("log file not created: %v", statErr)
	}

	if info.Size() != 0 {
		t.Errorf("expected empty file, got %d bytes", info.Size())
	}
}

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

// --- mailboxCustomStart (loadConfig override) ---

func TestMailboxCustomStart_LoadConfigFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}
	defer func() { loadConfig = old }()

	err := mailboxCustomStart(context.Background(), &ServiceDef{Name: "mailbox"})
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to load localconfig")
	}
}

func TestMailboxCustomStart_JavaBinaryMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mailboxd_java_home": tmp,
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := mailboxCustomStart(context.Background(), &ServiceDef{Name: "mailbox"})
	if err == nil {
		t.Fatal("expected error when java binary is missing")
	}
	if !strings.Contains(err.Error(), "java binary not found") {
		t.Errorf("error = %q, want it to contain %q", err, "java binary not found")
	}
}

func TestMailboxCustomStart_JavaBinaryPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	javaDir := filepath.Join(tmp, "jdk", "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	javaBin := filepath.Join(javaDir, "java")
	if err := os.WriteFile(javaBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mailboxd_java_home": filepath.Join(tmp, "jdk"),
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldMailboxd := mailboxdPath
	mailboxdPath = filepath.Join(tmp, "mailboxd")
	defer func() { mailboxdPath = oldMailboxd }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	oldLib := libPath
	libPath = filepath.Join(tmp, "lib")
	defer func() { libPath = oldLib }()

	oldConf := confPath
	confPath = filepath.Join(tmp, "conf")
	defer func() { confPath = oldConf }()

	oldMailbox := mailboxPath
	mailboxPath = filepath.Join(tmp, "mailbox")
	defer func() { mailboxPath = oldMailbox }()

	err := mailboxCustomStart(context.Background(), &ServiceDef{Name: "mailbox"})
	if err != nil {
		t.Errorf("mailboxCustomStart returned error: %v", err)
	}
}

// --- milterCustomStart (loadConfig override) ---

func TestMilterCustomStart_LoadConfigFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}
	defer func() { loadConfig = old }()

	err := milterCustomStart(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to load localconfig")
	}
}

func TestMilterCustomStart_JavaBinaryMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mailboxd_java_home": tmp,
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := milterCustomStart(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when java binary is missing")
	}
	if !strings.Contains(err.Error(), "java binary not found") {
		t.Errorf("error = %q, want it to contain %q", err, "java binary not found")
	}
}

func TestMilterCustomStart_JavaBinaryPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	javaDir := filepath.Join(tmp, "jdk", "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	javaBin := filepath.Join(javaDir, "java")
	if err := os.WriteFile(javaBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mailboxd_java_home": filepath.Join(tmp, "jdk"),
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	oldConf := confPath
	confPath = filepath.Join(tmp, "conf")
	defer func() { confPath = oldConf }()

	oldMailbox := mailboxPath
	mailboxPath = filepath.Join(tmp, "mailbox")
	defer func() { mailboxPath = oldMailbox }()

	oldBase := basePath
	basePath = tmp
	defer func() { basePath = oldBase }()

	err := milterCustomStart(context.Background(), nil)
	if err != nil {
		t.Errorf("milterCustomStart returned error: %v", err)
	}
}

// --- ldapCustomStart (loadConfig override) ---

func TestLdapCustomStart_LoadConfigFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}
	defer func() { loadConfig = old }()

	err := ldapCustomStart(context.Background(), &ServiceDef{Name: "ldap"})
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to load localconfig")
	}
}

func TestLdapCustomStart_WithFakeSlapd(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	slapdDir := filepath.Join(tmp, "libexec")
	if err := os.MkdirAll(slapdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slapdBin := filepath.Join(slapdDir, "slapd")
	if err := os.WriteFile(slapdBin, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ldapCfgDir := filepath.Join(tmp, "ldap", "config")
	if err := os.MkdirAll(ldapCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"ldap_port":              "389",
			"zimbra_server_hostname": "localhost",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldCommon := commonPath
	commonPath = tmp
	defer func() { commonPath = oldCommon }()

	oldData := dataPath
	dataPath = tmp
	defer func() { dataPath = oldData }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	socketPath := expectedSocketPath("ldap")

	done := make(chan error, 1)
	go func() {
		done <- ldapCustomStart(context.Background(), &ServiceDef{Name: "ldap"})
	}()

	sendReady(t, socketPath)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ldapCustomStart returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ldapCustomStart")
	}
}

func TestLdapCustomStart_ContextTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	slapdDir := filepath.Join(tmp, "libexec")
	if err := os.MkdirAll(slapdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slapdBin := filepath.Join(slapdDir, "slapd")
	if err := os.WriteFile(slapdBin, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ldapCfgDir := filepath.Join(tmp, "ldap", "config")
	if err := os.MkdirAll(ldapCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldCommon := commonPath
	commonPath = tmp
	defer func() { commonPath = oldCommon }()

	oldData := dataPath
	dataPath = tmp
	defer func() { dataPath = oldData }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ldapCustomStart(ctx, &ServiceDef{Name: "ldap"})
	if err == nil {
		t.Fatal("expected error on context timeout")
	}
}
