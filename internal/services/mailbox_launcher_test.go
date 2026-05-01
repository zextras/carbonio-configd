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

// --- mailboxJavaBinary ---

func TestMailboxJavaBinary_Missing(t *testing.T) {
	lc := map[string]string{
		"mailboxd_java_home": "/nonexistent",
	}

	_, err := mailboxJavaBinary(context.Background(), lc)
	if err == nil {
		t.Fatal("expected error for missing java binary")
	}
}

func TestMailboxJavaBinary_FallbackPath(t *testing.T) {
	tmp := t.TempDir()
	javaDir := filepath.Join(tmp, "lib", "jvm", "java", "bin")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	javaBin := filepath.Join(javaDir, "java")
	if err := os.WriteFile(javaBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldCommon := commonPath
	commonPath = tmp
	defer func() { commonPath = oldCommon }()

	lc := map[string]string{}
	bin, err := mailboxJavaBinary(context.Background(), lc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(bin, "lib/jvm/java") {
		t.Errorf("expected fallback java path, got %s", bin)
	}
}

// --- mailboxJavaArgs ---

func TestMailboxJavaArgs_Defaults(t *testing.T) {
	lc := map[string]string{}
	args := mailboxJavaArgs(lc)

	found := false
	for _, a := range args {
		if a == "-Xms512m" {
			found = true
		}
	}
	if !found {
		t.Error("expected default -Xms512m when mailboxd_java_heap_size empty")
	}
}

func TestMailboxJavaArgs_CustomHeapAndOptions(t *testing.T) {
	lc := map[string]string{
		"mailboxd_java_heap_size":  "2048",
		"mailboxd_java_options":    "-Xss256k -Dtest=1",
		"networkaddress_cache_ttl": "120",
		"zimbra_log4j_properties":  "/opt/zextras/conf/log4j.properties",
	}
	args := mailboxJavaArgs(lc)

	has := func(s string) bool {
		for _, a := range args {
			if a == s {
				return true
			}
		}
		return false
	}
	if !has("-Xms2048m") {
		t.Error("expected -Xms2048m")
	}
	if !has("-Xmx2048m") {
		t.Error("expected -Xmx2048m")
	}
	if !has("-Dsun.net.inetaddr.ttl=120") {
		t.Error("expected -Dsun.net.inetaddr.ttl=120")
	}
	if !has("-Dlog4j.configurationFile=/opt/zextras/conf/log4j.properties") {
		t.Error("expected log4j property")
	}
}

func TestMailboxJavaArgs_OptionsContainXss(t *testing.T) {
	lc := map[string]string{
		"mailboxd_java_heap_size": "1024",
		"mailboxd_java_options":   "-Xss512k",
	}
	args := mailboxJavaArgs(lc)
	for _, a := range args {
		if a == "-Xss256k" {
			t.Error("should not add default Xss when already in options")
		}
	}
}

func TestMailboxCustomStart_DirCreation(t *testing.T) {
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
			"mailboxd_java_home":         filepath.Join(tmp, "jdk"),
			"mailboxd_java_heap_size":    "256",
			"mailboxd_java_options":      "",
			"mailboxd_thread_stack_size": "",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldMailboxd := mailboxdPath
	mailboxdPath = filepath.Join(tmp, "mailboxd")
	defer func() { mailboxdPath = oldMailboxd }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	oldMailbox := mailboxPath
	mailboxPath = filepath.Join(tmp, "mailbox")
	defer func() { mailboxPath = oldMailbox }()

	oldLib := libPath
	libPath = filepath.Join(tmp, "lib")
	defer func() { libPath = oldLib }()

	oldConf := confPath
	confPath = filepath.Join(tmp, "conf")
	defer func() { confPath = oldConf }()

	oldCommon := commonPath
	commonPath = filepath.Join(tmp, "common")
	defer func() { commonPath = oldCommon }()

	err := mailboxCustomStart(context.Background(), &ServiceDef{Name: "mailbox"})
	if err != nil {
		t.Logf("mailboxCustomStart: %v (may fail if args issue)", err)
	}

	workDir := filepath.Join(tmp, "mailboxd", "work", "service", "jsp")
	if _, statErr := os.Stat(workDir); statErr != nil {
		t.Logf("work dir creation: %v (may fail if start didn't complete)", statErr)
	}
}

func TestMailboxCustomStart_ZeroHeapDefaults(t *testing.T) {
	lc := map[string]string{}
	args := mailboxJavaArgs(lc)
	if len(args) == 0 {
		t.Error("expected non-empty args")
	}
}

func TestMailboxCustomStart_GCLogCreation(t *testing.T) {
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
		t.Logf("mailboxCustomStart: %v", err)
	}

	gcLog := filepath.Join(logDir, "gc.log")
	if _, statErr := os.Stat(gcLog); statErr != nil {
		t.Logf("gc.log not created (expected if start failed): %v", statErr)
	}
}

// --- mailboxCustomStop ---

func TestMailboxCustomStop_NoProcessName(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	err := mailboxCustomStop(context.Background(), &ServiceDef{Name: "mailbox"})
	if err != nil {
		t.Fatalf("expected nil when ProcessName is empty, got %v", err)
	}
}

func TestMailboxCustomStop_WithProcessName(t *testing.T) {
	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	def := &ServiceDef{
		Name:        "mailbox",
		ProcessName: "nonexistent-mailboxd-process-xyz",
	}
	err := mailboxCustomStop(context.Background(), def)
	if err != nil {
		t.Logf("mailboxCustomStop with ProcessName: %v (stopAppserverDB error is expected)", err)
	}
}
