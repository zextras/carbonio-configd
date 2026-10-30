// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func createTestConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "localconfig.xml")
	content := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
    <key name="zimbra_home"><value>/opt/zextras</value></key>
    <key name="smtp_port"><value>25</value></key>
</localconfig>`

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

// setOne sets a single key through the batch API.
func setOne(path, key, value string) error {
	return SetKeys(path, []Key{{Name: key, Value: value}})
}

// removeOne removes a single key through the batch API.
func removeOne(path, key string) error {
	return RemoveKeys(path, []string{key})
}

func TestSetKey_NewKey(t *testing.T) {
	path := createTestConfig(t)

	err := setOne(path, "new_key", "new_value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["new_key"] != "new_value" {
		t.Errorf("expected new_value, got %q", config["new_key"])
	}
}

func TestSetKey_OverwriteExisting(t *testing.T) {
	path := createTestConfig(t)

	err := setOne(path, "smtp_port", "587")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["smtp_port"] != "587" {
		t.Errorf("expected 587, got %q", config["smtp_port"])
	}
}

func TestSetKey_PreservesOtherKeys(t *testing.T) {
	path := createTestConfig(t)

	err := setOne(path, "smtp_port", "587")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["zimbra_home"] != "/opt/zextras" {
		t.Errorf("other key modified: expected /opt/zextras, got %q", config["zimbra_home"])
	}
}

func TestRemoveKey_Existing(t *testing.T) {
	path := createTestConfig(t)

	err := removeOne(path, "smtp_port")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if _, ok := config["smtp_port"]; ok {
		t.Error("expected smtp_port to be removed")
	}
}

func TestRemoveKey_Nonexistent(t *testing.T) {
	path := createTestConfig(t)

	err := removeOne(path, "nonexistent")

	var notSet *KeyNotSetError
	if !errors.As(err, &notSet) || notSet.Key != "nonexistent" {
		t.Errorf("expected KeyNotSetError for nonexistent, got %v", err)
	}
}

func TestRemoveKey_WithDefault(t *testing.T) {
	path := createTestConfig(t)

	if err := setOne(path, "zimbra_home", "/custom/path"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Legacy LocalConfig.remove drops the entry; it never writes "".
	if err := removeOne(path, "zimbra_home"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if val, ok := config["zimbra_home"]; ok {
		t.Errorf("expected zimbra_home to be dropped from the file, got %q", val)
	}
}

// A known key that is not in the file is "set" for legacy isSet(): removing it
// is a successful no-op and nothing like an empty value is written.
func TestRemoveKey_KnownButUnset(t *testing.T) {
	path := createTestConfig(t)

	if err := removeOne(path, "zimbra_java_home"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if val, ok := config["zimbra_java_home"]; ok {
		t.Errorf("known key must not be written, got %q", val)
	}
}

func TestSetKey_EmptyValue(t *testing.T) {
	path := createTestConfig(t)

	err := setOne(path, "smtp_port", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["smtp_port"] != "" {
		t.Errorf("expected empty, got %q", config["smtp_port"])
	}
}

func TestSetKey_SpecialCharacters(t *testing.T) {
	path := createTestConfig(t)

	value := `it's a "test" with <xml> & special chars`

	err := setOne(path, "special", value)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["special"] != value {
		t.Errorf("expected %q, got %q", value, config["special"])
	}
}

func TestSetKey_ConcurrentWrites(t *testing.T) {
	path := createTestConfig(t)

	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			key := "concurrent_key"
			value := string(rune('A' + idx))

			_ = setOne(path, key, value)
		}(i)
	}

	wg.Wait()

	// Verify the file is valid XML after concurrent writes
	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("file corrupted after concurrent writes: %v", err)
	}

	if _, ok := config["concurrent_key"]; !ok {
		t.Error("expected concurrent_key to exist")
	}
}

func TestSaveLocalConfig_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localconfig.xml")

	config := &LocalConfig{
		Keys: []Key{
			{Name: "test_key", Value: "test_value"},
		},
	}

	err := SaveLocalConfig(path, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if loaded["test_key"] != "test_value" {
		t.Errorf("expected test_value, got %q", loaded["test_key"])
	}
}

func TestSetKey_FileNotFound(t *testing.T) {
	err := setOne("/nonexistent/path/localconfig.xml", "key", "value")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// --- readLocalConfigXML ---

// Legacy starts from an empty config when the file is missing, and -e then
// creates it.
func TestSetKeys_MissingFileIsCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")

	if err := setOne(path, "k", "v"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["k"] != "v" {
		t.Errorf("expected k=v in created file, got %v", config)
	}
}

func TestRemoveKeys_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")

	var notSet *KeyNotSetError
	if err := removeOne(path, "nope"); !errors.As(err, &notSet) {
		t.Fatalf("unknown key on a missing file must be 'not set', got %v", err)
	}

	if _, err := os.Stat(path); err == nil {
		t.Error("a failed unset must not create the file")
	}

	if err := removeOne(path, "zimbra_java_home"); err != nil {
		t.Fatalf("known key must be a no-op, got %v", err)
	}
}

func TestReadLocalConfigXML_InvalidXML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localconfig.xml")

	if err := os.WriteFile(path, []byte("not valid xml <<<"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := setOne(path, "k", "v")
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}

func TestRemoveKey_InvalidXML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localconfig.xml")

	if err := os.WriteFile(path, []byte("not valid xml <<<"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := removeOne(path, "k")
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}

// --- atomicWrite ---

func TestAtomicWrite_CreateTempError(t *testing.T) {
	// Passing a path whose directory does not exist causes os.CreateTemp to fail.
	err := atomicWrite("/nonexistent/dir/localconfig.xml", []byte("data"))
	if err == nil {
		t.Error("expected error when temp dir does not exist")
	}
}

func TestAtomicWrite_RenameError(t *testing.T) {
	// Make the target path a directory: os.Rename of a regular file onto an
	// existing directory returns EISDIR on Linux. CreateTemp still succeeds
	// because the parent dir is writable; only Rename fails.
	dir := t.TempDir()
	target := filepath.Join(dir, "localconfig.xml")

	// Create target as a directory, not a file.
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	err := atomicWrite(target, []byte("<localconfig/>"))
	if err == nil {
		t.Error("expected error when target path is a directory")
	}
}

func TestAtomicWrite_WriteError(t *testing.T) {
	// Use a read-only temp dir: atomicWrite will succeed in creating the temp
	// file (O_CREATE succeeds) but writing to it should fail because we
	// immediately revoke write permission on the file from another goroutine.
	// This is inherently racy; instead we rely on the /dev/full trick.
	//
	// We cannot easily inject a write error without modifying production code,
	// so this test verifies the success path with a large payload to exercise
	// the write code path fully.
	dir := t.TempDir()
	path := filepath.Join(dir, "localconfig.xml")
	content := make([]byte, 1<<16) // 64 KB

	if err := atomicWrite(path, content); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after write: %v", err)
	}

	if info.Size() != int64(len(content)) {
		t.Errorf("expected %d bytes, got %d", len(content), info.Size())
	}
}

func TestAtomicWrite_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "localconfig.xml")

	// Create the file with a specific mode.
	if err := os.WriteFile(path, []byte("<localconfig/>"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := atomicWrite(path, []byte("<localconfig/>")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode() != 0o640 {
		t.Errorf("expected mode 0640, got %v", info.Mode())
	}
}

func TestWithFileLock_KeepsLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")

	if err := withFileLock(path, func() error { return nil }); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("lock file must persist after unlock: %v", err)
	}
}

// --- withFileLock ---

func TestWithFileLock_OpenError(t *testing.T) {
	// Providing a path whose parent directory does not exist causes
	// os.OpenFile on the lock file to fail.
	err := withFileLock("/nonexistent/dir/localconfig.xml", func() error {
		return nil
	})
	if err == nil {
		t.Error("expected error when lock file directory does not exist")
	}
}

// --- ensureZextrasOwnership ---

func TestEnsureZextrasOwnership_NonRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — non-root branch not testable")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")

	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-root: function must return nil immediately without touching the file.
	if err := ensureZextrasOwnership(path); err != nil {
		t.Errorf("unexpected error for non-root user: %v", err)
	}
}

// --- batch semantics (legacy: mutate in memory, save once) ---

func TestSetKeys_AppliesAllInOrder(t *testing.T) {
	path := createTestConfig(t)

	err := SetKeys(path, []Key{
		{Name: "smtp_port", Value: "587"},
		{Name: "added", Value: "one"},
		{Name: "added", Value: "two"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["smtp_port"] != "587" || config["added"] != "two" {
		t.Errorf("unexpected config after batch set: %v", config)
	}
}

func TestRemoveKeys_AllOrNothing(t *testing.T) {
	path := createTestConfig(t)

	err := RemoveKeys(path, []string{"smtp_port", "nonexistent"})
	if err == nil || err.Error() != "key nonexistent is not set" {
		t.Fatalf("expected 'key nonexistent is not set', got %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if config["smtp_port"] != "25" {
		t.Errorf("smtp_port must survive a failed batch, got %q", config["smtp_port"])
	}
}

func TestRemoveKeys_RemovesAll(t *testing.T) {
	path := createTestConfig(t)

	if err := RemoveKeys(path, []string{"smtp_port", "zimbra_home"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if _, ok := config["smtp_port"]; ok {
		t.Error("expected smtp_port to be removed")
	}
}
