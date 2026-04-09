// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/logger"
)

func TestMain(m *testing.M) {
	// Initialize logger for tests
	logger.InitStructuredLogging(&logger.Config{
		Format: logger.FormatText,
		Level:  logger.LogLevelInfo,
	})
	os.Exit(m.Run())
}

func TestExecutor_Initialization(t *testing.T) {
	mockLdap := &mockLdapManager{}
	baseDir := "/opt/zextras"

	executor := NewExecutor(baseDir, mockLdap).(*executor)

	if executor == nil {
		t.Fatal("NewExecutor should not return nil")
	}

	if executor.baseDir != baseDir {
		t.Errorf("baseDir = %v, want %v", executor.baseDir, baseDir)
	}

	expectedPostconfPath := filepath.Join(baseDir, "common", "sbin", "postconf")
	if executor.postconfPath != expectedPostconfPath {
		t.Errorf("postconfPath = %v, want %v", executor.postconfPath, expectedPostconfPath)
	}

	if executor.ldapManager != mockLdap {
		t.Error("ldapManager not set correctly")
	}

	if len(executor.mappedFiles) == 0 {
		t.Error("mappedFiles should be initialized")
	}

	// Check zimbraSSLDHParam mapping
	expectedDHParamPath := filepath.Join(baseDir, "conf", "dhparam.pem")
	if executor.mappedFiles["zimbraSSLDHParam"] != expectedDHParamPath {
		t.Errorf("zimbraSSLDHParam mapping = %v, want %v",
			executor.mappedFiles["zimbraSSLDHParam"], expectedDHParamPath)
	}
}

func TestExecutePostconf(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	// Create a fake postconf script that just exits successfully
	postconfPath := filepath.Join(tmpDir, "common", "sbin", "postconf")
	if err := os.MkdirAll(filepath.Dir(postconfPath), 0o755); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
exit 0
`
	if err := os.WriteFile(postconfPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	executor := NewExecutor(tmpDir, mockLdap)

	op := PostconfOperation{
		Key:   "myhostname",
		Value: "mail.example.com",
	}

	err := executor.ExecutePostconf(context.Background(), op)
	if err != nil {
		t.Errorf("ExecutePostconf failed: %v", err)
	}
}

func TestExecutePostconfBatch_MultilineValues(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	// Create a fake postconf script
	postconfPath := filepath.Join(tmpDir, "common", "sbin", "postconf")
	if err := os.MkdirAll(filepath.Dir(postconfPath), 0o755); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
echo "$@" > ` + filepath.Join(tmpDir, "postconf_args.txt") + `
exit 0
`
	if err := os.WriteFile(postconfPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	executor := NewExecutor(tmpDir, mockLdap)

	// Test multi-line value normalization
	ops := []PostconfOperation{
		{
			Key:   "smtpd_restriction_classes",
			Value: "restriction_class1\nrestriction_class2\nrestriction_class3",
		},
	}

	err := executor.ExecutePostconfBatch(context.Background(), ops)
	if err != nil {
		t.Errorf("ExecutePostconfBatch failed: %v", err)
	}

	// Read the captured arguments
	argsFile := filepath.Join(tmpDir, "postconf_args.txt")
	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("Failed to read args file: %v", err)
	}

	// Should have comma-separated format
	args := string(argsData)
	if !contains(args, "restriction_class1, restriction_class2, restriction_class3") {
		t.Errorf("Multi-line value not normalized correctly: %s", args)
	}
}

func TestExecutePostconfBatch_EmptyBatch(t *testing.T) {
	mockLdap := &mockLdapManager{}
	executor := NewExecutor("/opt/zextras", mockLdap)

	err := executor.ExecutePostconfBatch(context.Background(), nil)
	if err != nil {
		t.Errorf("ExecutePostconfBatch with nil should not error: %v", err)
	}

	err = executor.ExecutePostconfBatch(context.Background(), []PostconfOperation{})
	if err != nil {
		t.Errorf("ExecutePostconfBatch with empty slice should not error: %v", err)
	}
}

func TestExecutePostconfd(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	// Create a fake postconf script
	postconfPath := filepath.Join(tmpDir, "common", "sbin", "postconf")
	if err := os.MkdirAll(filepath.Dir(postconfPath), 0o755); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
exit 0
`
	if err := os.WriteFile(postconfPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	executor := NewExecutor(tmpDir, mockLdap)

	op := PostconfdOperation{
		Key: "deprecated_param",
	}

	err := executor.ExecutePostconfd(context.Background(), op)
	if err != nil {
		t.Errorf("ExecutePostconfd failed: %v", err)
	}
}

func TestExecutePostconfdBatch_NonExistentKey(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	// Create a fake postconf script that fails (key doesn't exist)
	postconfPath := filepath.Join(tmpDir, "common", "sbin", "postconf")
	if err := os.MkdirAll(filepath.Dir(postconfPath), 0o755); err != nil {
		t.Fatal(err)
	}

	script := `#!/bin/sh
echo "postconf: warning: unknown parameter: deprecated_param" >&2
exit 1
`
	if err := os.WriteFile(postconfPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	executor := NewExecutor(tmpDir, mockLdap)

	ops := []PostconfdOperation{
		{Key: "deprecated_param"},
	}

	// Should not return error even if postconf fails (acceptable behavior)
	err := executor.ExecutePostconfdBatch(context.Background(), ops)
	if err != nil {
		t.Errorf("ExecutePostconfdBatch should not error on non-existent keys: %v", err)
	}
}

func TestExecuteMapfile_WriteBase64Data(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap).(*executor)

	// Set up mapped file path
	testFilePath := filepath.Join(tmpDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	// Create test data
	testData := "-----BEGIN DH PARAMETERS-----\ntest data\n-----END DH PARAMETERS-----\n"
	base64Data := base64.StdEncoding.EncodeToString([]byte(testData))

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: base64Data,
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Fatalf("ExecuteMapfile failed: %v", err)
	}

	// Verify file was written
	writtenData, err := os.ReadFile(testFilePath)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}

	if string(writtenData) != testData {
		t.Errorf("Written data = %q, want %q", string(writtenData), testData)
	}

	// Test idempotency - writing same data again should succeed
	err = executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("ExecuteMapfile should be idempotent: %v", err)
	}
}

func TestExecuteMapfile_InvalidBase64(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap).(*executor)

	testFilePath := filepath.Join(tmpDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: "invalid!@#$%base64",
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err == nil {
		t.Error("ExecuteMapfile should fail with invalid base64 data")
	}
}

func TestExecuteMapfile_UnmappedKey(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap)

	op := MapfileOperation{
		Key:        "unmappedKey",
		IsLocal:    false,
		Base64Data: "dGVzdA==",
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err == nil {
		t.Error("ExecuteMapfile should fail with unmapped key")
	}

	if !contains(err.Error(), "unmapped MAPFILE key") {
		t.Errorf("Error should mention unmapped key, got: %v", err)
	}
}

func TestExecuteMapfile_MAPLOCAL_FileExists(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap).(*executor)

	// Create test file
	testFilePath := filepath.Join(tmpDir, "existing.crt")
	if err := os.WriteFile(testFilePath, []byte("test cert"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor.mappedFiles["testCert"] = testFilePath

	op := MapfileOperation{
		Key:     "testCert",
		IsLocal: true, // MAPLOCAL
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("MAPLOCAL with existing file should succeed: %v", err)
	}
}

func TestExecuteMapfile_MAPLOCAL_FileNotExists(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap).(*executor)

	testFilePath := filepath.Join(tmpDir, "nonexistent.crt")
	executor.mappedFiles["testCert"] = testFilePath

	op := MapfileOperation{
		Key:     "testCert",
		IsLocal: true, // MAPLOCAL
	}

	// Should not error, just log that file doesn't exist
	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("MAPLOCAL with non-existent file should not error: %v", err)
	}
}

func TestHandleEmptyMapfileData_RestoreFromBackup(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap).(*executor)

	testFilePath := filepath.Join(tmpDir, "test.pem")
	backupPath := testFilePath + ".crb"

	executor.mappedFiles["testKey"] = testFilePath

	// Create backup file
	backupData := []byte("backup content")
	if err := os.WriteFile(backupPath, backupData, 0o600); err != nil {
		t.Fatal(err)
	}

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: "", // Empty data
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Fatalf("ExecuteMapfile with empty data should restore from backup: %v", err)
	}

	// Verify file was restored from backup
	restoredData, err := os.ReadFile(testFilePath)
	if err != nil {
		t.Fatalf("Failed to read restored file: %v", err)
	}

	if string(restoredData) != string(backupData) {
		t.Errorf("Restored data = %q, want %q", string(restoredData), string(backupData))
	}
}

func TestHandleEmptyMapfileData_LeaveExistingUntouched(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap).(*executor)

	testFilePath := filepath.Join(tmpDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	// Create existing file (no backup)
	existingData := []byte("existing content")
	if err := os.WriteFile(testFilePath, existingData, 0o600); err != nil {
		t.Fatal(err)
	}

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: "", // Empty data
	}

	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Fatalf("ExecuteMapfile with empty data should leave existing file: %v", err)
	}

	// Verify file was not deleted
	unchangedData, err := os.ReadFile(testFilePath)
	if err != nil {
		t.Fatalf("Existing file should not be deleted: %v", err)
	}

	if string(unchangedData) != string(existingData) {
		t.Errorf("File was modified, want unchanged")
	}
}

func TestHandleEmptyMapfileData_NoBackupNoFile(t *testing.T) {
	mockLdap := &mockLdapManager{}
	tmpDir := t.TempDir()

	executor := NewExecutor(tmpDir, mockLdap).(*executor)

	testFilePath := filepath.Join(tmpDir, "test.pem")
	executor.mappedFiles["testKey"] = testFilePath

	op := MapfileOperation{
		Key:        "testKey",
		IsLocal:    false,
		Base64Data: "", // Empty data
	}

	// No backup, no existing file - should not error
	err := executor.ExecuteMapfile(context.Background(), op)
	if err != nil {
		t.Errorf("ExecuteMapfile with no data and no file should not error: %v", err)
	}

	// File should not be created
	if _, err := os.Stat(testFilePath); !os.IsNotExist(err) {
		t.Error("File should not be created when there's no data")
	}
}

func TestExecuteLdapWrite_Success(t *testing.T) {
	mockLdap := &mockLdapManager{}
	executor := NewExecutor("/opt/zextras", mockLdap)

	op := LdapOperation{
		Key:   "ldap_db_maxsize",
		Value: "1073741824",
	}

	err := executor.ExecuteLdapWrite(context.Background(), op)
	if err != nil {
		t.Errorf("ExecuteLdapWrite should succeed: %v", err)
	}

	if !mockLdap.modifyCalled {
		t.Error("LDAP ModifyAttribute should have been called")
	}

	if mockLdap.modifyKey != op.Key {
		t.Errorf("LDAP key = %v, want %v", mockLdap.modifyKey, op.Key)
	}

	if mockLdap.modifyValue != op.Value {
		t.Errorf("LDAP value = %v, want %v", mockLdap.modifyValue, op.Value)
	}
}

func TestExecuteLdapWrite_Failure(t *testing.T) {
	mockLdap := &mockLdapManager{
		modifyErr: errors.New("ldap connection failed"),
	}
	executor := NewExecutor("/opt/zextras", mockLdap)

	op := LdapOperation{
		Key:   "ldap_db_maxsize",
		Value: "1073741824",
	}

	err := executor.ExecuteLdapWrite(context.Background(), op)
	if err == nil {
		t.Error("ExecuteLdapWrite should fail when LDAP manager returns error")
	}

	if !contains(err.Error(), "LDAP write failed") {
		t.Errorf("Error should mention LDAP write failure, got: %v", err)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
