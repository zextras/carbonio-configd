// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestKillProcess_NoMatch verifies killProcess returns nil when no process matches.
func TestKillProcess_NoMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	err := killProcess(context.Background(), "carbonio-configd-unique-needle-xyzzy-no-match-99999")
	if err != nil {
		t.Errorf("killProcess with no match returned error: %v", err)
	}
}

// TestKillProcess_SelfExclusion verifies killProcess skips our own PID even when matched.
func TestKillProcess_SelfExclusion(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	self := os.Getpid()

	selfDir := filepath.Join(tmpDir, strconv.Itoa(self))
	if err := os.MkdirAll(selfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	needle := "killprocess-selftest-needle-unique"
	if err := os.WriteFile(filepath.Join(selfDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(selfDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestKillProcess_ParentExclusion verifies killProcess skips the parent PID.
func TestKillProcess_ParentExclusion(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmpDir := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmpDir + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	parent := os.Getppid()

	parentDir := filepath.Join(tmpDir, strconv.Itoa(parent))
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	needle := "killprocess-parent-exclusion-needle"
	if err := os.WriteFile(filepath.Join(parentDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(parentDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestKillProcess_ParentExcluded verifies killProcess excludes parent PID (variant).
func TestKillProcess_ParentExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmp + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	parent := os.Getppid()

	parentDir := filepath.Join(tmp, strconv.Itoa(parent))
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	needle := "killprocess-parent-test-needle-xyzzy"
	if err := os.WriteFile(filepath.Join(parentDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(parentDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}

// TestKillProcess_SelfExcluded verifies killProcess does not kill itself (variant).
func TestKillProcess_SelfExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()
	oldRoot := procFSRoot
	procFSRoot = tmp + "/"
	defer func() { procFSRoot = oldRoot }()

	uid := os.Getuid()
	self := os.Getpid()

	selfDir := filepath.Join(tmp, strconv.Itoa(self))
	if err := os.MkdirAll(selfDir, 0o755); err != nil {
		t.Fatal(err)
	}

	needle := "killprocess-self-test-needle-xyzzy"
	if err := os.WriteFile(filepath.Join(selfDir, "cmdline"), []byte(needle+"\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := "Name:\ttest\nState:\tS (sleeping)\nUid:\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\t" + strconv.Itoa(uid) + "\n"
	if err := os.WriteFile(filepath.Join(selfDir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	err := killProcess(context.Background(), needle)
	if err != nil {
		t.Errorf("killProcess returned unexpected error: %v", err)
	}
}
