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

// --- milterCustomStart (ctl.loadConfig override) ---

func TestMilterCustomStart_LoadConfigFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}

	err := ctl.milterCustomStart(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when ctl.loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to load localconfig")
	}
}

func TestMilterCustomStart_JavaBinaryMissing(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mailboxd_java_home": tmp,
		}, nil
	}

	err := ctl.milterCustomStart(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when java binary is missing")
	}
	if !strings.Contains(err.Error(), "java binary not found") {
		t.Errorf("error = %q, want it to contain %q", err, "java binary not found")
	}
}

func TestMilterCustomStart_JavaBinaryPresent(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

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

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"mailboxd_java_home": filepath.Join(tmp, "jdk"),
		}, nil
	}

	ctl.p.log = logDir

	ctl.p.conf = filepath.Join(tmp, "conf")

	ctl.p.mailbox = filepath.Join(tmp, "mailbox")

	ctl.p.base = tmp

	err := ctl.milterCustomStart(context.Background(), nil)
	if err != nil {
		t.Errorf("milterCustomStart returned error: %v", err)
	}
}
