// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfix

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNewPostfixManager verifies manager initialization.
func TestNewPostfixManager(t *testing.T) {
	tests := []struct {
		name            string
		postconfCmd     string
		expectedCmd     string
		expectedChanges int
		expectedDeletes int
	}{
		{
			name:            "default postconf command",
			postconfCmd:     "",
			expectedCmd:     "postconf",
			expectedChanges: 0,
			expectedDeletes: 0,
		},
		{
			name:            "custom postconf command",
			postconfCmd:     "/opt/zextras/bin/postconf",
			expectedCmd:     "/opt/zextras/bin/postconf",
			expectedChanges: 0,
			expectedDeletes: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPostfixManager(tt.postconfCmd)

			if pm.postconfCmd != tt.expectedCmd {
				t.Errorf("Expected postconfCmd=%s, got %s", tt.expectedCmd, pm.postconfCmd)
			}

			if len(pm.postconfChanges) != tt.expectedChanges {
				t.Errorf("Expected %d postconf changes, got %d", tt.expectedChanges, len(pm.postconfChanges))
			}

			if len(pm.postconfdDeletions) != tt.expectedDeletes {
				t.Errorf("Expected %d postconfd deletions, got %d", tt.expectedDeletes, len(pm.postconfdDeletions))
			}
		})
	}
}

// TestAddPostconf verifies postconf change queuing.
func TestAddPostconf(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		value       string
		expectError bool
	}{
		{
			name:        "valid postconf entry",
			key:         "myhostname",
			value:       "mail.example.com",
			expectError: false,
		},
		{
			name:        "empty key",
			key:         "",
			value:       "some value",
			expectError: true,
		},
		{
			name:        "empty value",
			key:         "mynetworks",
			value:       "",
			expectError: false,
		},
		{
			name:        "value with newlines",
			key:         "smtpd_recipient_restrictions",
			value:       "permit_mynetworks,\nreject_unauth_destination",
			expectError: false,
		},
		{
			name:        "value with spaces",
			key:         "smtpd_banner",
			value:       "$myhostname ESMTP $mail_name",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPostfixManager("")
			err := pm.AddPostconf(context.Background(), tt.key, tt.value)

			if tt.expectError && err == nil {
				t.Error("Expected error but got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError {
				// Verify change was queued
				postconf, _ := pm.GetPendingChanges()
				if postconf[tt.key] != tt.value {
					t.Errorf("Expected value '%s', got '%s'", tt.value, postconf[tt.key])
				}
			}
		})
	}
}

// TestAddPostconfd verifies postconfd deletion queuing.
func TestAddPostconfd(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		expectError bool
	}{
		{
			name:        "valid postconfd entry",
			key:         "content_filter",
			expectError: false,
		},
		{
			name:        "empty key",
			key:         "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPostfixManager("")
			err := pm.AddPostconfd(context.Background(), tt.key)

			if tt.expectError && err == nil {
				t.Error("Expected error but got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError {
				// Verify deletion was queued
				_, postconfd := pm.GetPendingChanges()
				found := slices.Contains(postconfd, tt.key)
				if !found {
					t.Errorf("Expected key '%s' in postconfd deletions", tt.key)
				}
			}
		})
	}
}

// TestGetPendingChanges verifies change retrieval.
func TestGetPendingChanges(t *testing.T) {
	pm := NewPostfixManager("")

	// Add multiple postconf changes
	pm.AddPostconf(context.Background(), "myhostname", "mail.example.com")
	pm.AddPostconf(context.Background(), "mynetworks", "127.0.0.0/8")
	pm.AddPostconf(context.Background(), "smtpd_banner", "$myhostname ESMTP")

	// Add multiple postconfd deletions
	pm.AddPostconfd(context.Background(), "content_filter")
	pm.AddPostconfd(context.Background(), "virtual_alias_maps")

	postconf, postconfd := pm.GetPendingChanges()

	// Verify postconf changes
	if len(postconf) != 3 {
		t.Errorf("Expected 3 postconf changes, got %d", len(postconf))
	}
	if postconf["myhostname"] != "mail.example.com" {
		t.Errorf("Unexpected value for myhostname: %s", postconf["myhostname"])
	}

	// Verify postconfd deletions
	if len(postconfd) != 2 {
		t.Errorf("Expected 2 postconfd deletions, got %d", len(postconfd))
	}

	// Verify returns are copies (not references)
	postconf["test"] = "value"
	postconf2, _ := pm.GetPendingChanges()
	if _, exists := postconf2["test"]; exists {
		t.Error("GetPendingChanges should return a copy, not a reference")
	}
}

// TestClearPending verifies pending change clearing.
func TestClearPending(t *testing.T) {
	pm := NewPostfixManager("")

	// Add changes
	pm.AddPostconf(context.Background(), "myhostname", "mail.example.com")
	pm.AddPostconfd(context.Background(), "content_filter")

	// Verify changes exist
	postconf, postconfd := pm.GetPendingChanges()
	if len(postconf) == 0 || len(postconfd) == 0 {
		t.Fatal("Expected pending changes before clearing")
	}

	// Clear pending
	pm.ClearPending(context.Background())

	// Verify empty
	postconf, postconfd = pm.GetPendingChanges()
	if len(postconf) != 0 {
		t.Errorf("Expected 0 postconf changes after clear, got %d", len(postconf))
	}
	if len(postconfd) != 0 {
		t.Errorf("Expected 0 postconfd deletions after clear, got %d", len(postconfd))
	}
}

// TestValueSanitization verifies newline replacement in values.
func TestValueSanitization(t *testing.T) {
	// Create a temporary postconf script that echoes arguments
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
# Echo all arguments to verify sanitization
echo "$@" > ` + filepath.Join(tmpDir, "output.txt") + `
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)

	// Add entry with newlines
	value := "permit_mynetworks,\nreject_unauth_destination,\nreject_invalid_hostname"
	pm.AddPostconf(context.Background(), "smtpd_recipient_restrictions", value)

	// Flush (should sanitize newlines)
	if err := pm.FlushPostconf(context.Background()); err != nil {
		t.Fatalf("FlushPostconf failed: %v", err)
	}

	// Read output to verify sanitization
	output, err := os.ReadFile(filepath.Join(tmpDir, "output.txt"))
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "\n") && strings.Count(outputStr, "\n") > 1 {
		// More than one newline means the value wasn't sanitized
		t.Error("Value should have newlines replaced with spaces")
	}

	// Verify the sanitized value is in the output
	sanitized := strings.ReplaceAll(value, "\n", " ")
	if !strings.Contains(outputStr, sanitized) {
		t.Errorf("Expected sanitized value in output. Got: %s", outputStr)
	}
}

// TestFlushPostconfSuccess verifies successful postconf execution.
func TestFlushPostconfSuccess(t *testing.T) {
	// Create a mock postconf script that succeeds
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
echo "postconf success"
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)
	pm.AddPostconf(context.Background(), "myhostname", "mail.example.com")
	pm.AddPostconf(context.Background(), "mynetworks", "127.0.0.0/8")

	// Flush should succeed
	if err := pm.FlushPostconf(context.Background()); err != nil {
		t.Errorf("FlushPostconf failed: %v", err)
	}

	// Verify pending changes are cleared
	postconf, _ := pm.GetPendingChanges()
	if len(postconf) != 0 {
		t.Errorf("Expected 0 pending changes after flush, got %d", len(postconf))
	}
}

// TestFlushPostconfFailure verifies postconf failure handling.
func TestFlushPostconfFailure(t *testing.T) {
	// Create a mock postconf script that fails
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
echo "postconf error" >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)
	pm.AddPostconf(context.Background(), "myhostname", "mail.example.com")

	// Flush should fail
	err := pm.FlushPostconf(context.Background())
	if err == nil {
		t.Error("Expected FlushPostconf to fail but got nil")
	}

	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("Expected error message to contain 'failed', got: %v", err)
	}
}

// TestFlushPostconfdSuccess verifies successful postconfd execution.
func TestFlushPostconfdSuccess(t *testing.T) {
	// Create a mock postconf script that succeeds
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
echo "postconfd success"
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)
	pm.AddPostconfd(context.Background(), "content_filter")
	pm.AddPostconfd(context.Background(), "virtual_alias_maps")

	// Flush should succeed
	if err := pm.FlushPostconfd(context.Background()); err != nil {
		t.Errorf("FlushPostconfd failed: %v", err)
	}

	// Verify pending deletions are cleared
	_, postconfd := pm.GetPendingChanges()
	if len(postconfd) != 0 {
		t.Errorf("Expected 0 pending deletions after flush, got %d", len(postconfd))
	}
}

// TestFlushPostconfdFailure verifies postconfd failure handling.
func TestFlushPostconfdFailure(t *testing.T) {
	// Create a mock postconf script that fails
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
echo "postconfd error" >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)
	pm.AddPostconfd(context.Background(), "content_filter")

	// Flush should fail
	err := pm.FlushPostconfd(context.Background())
	if err == nil {
		t.Error("Expected FlushPostconfd to fail but got nil")
	}

	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("Expected error message to contain 'failed', got: %v", err)
	}
}

// TestFlushEmptyQueue verifies flushing with no pending changes.
func TestFlushEmptyQueue(t *testing.T) {
	pm := NewPostfixManager("")

	// Flush with no changes should succeed
	if err := pm.FlushPostconf(context.Background()); err != nil {
		t.Errorf("FlushPostconf with empty queue failed: %v", err)
	}

	if err := pm.FlushPostconfd(context.Background()); err != nil {
		t.Errorf("FlushPostconfd with empty queue failed: %v", err)
	}
}

// TestMultipleFlushes verifies multiple flush operations.
func TestMultipleFlushes(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	script := `#!/bin/bash
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)

	// First batch
	pm.AddPostconf(context.Background(), "myhostname", "mail1.example.com")
	if err := pm.FlushPostconf(context.Background()); err != nil {
		t.Errorf("First flush failed: %v", err)
	}

	// Verify cleared
	postconf, _ := pm.GetPendingChanges()
	if len(postconf) != 0 {
		t.Error("Expected empty queue after first flush")
	}

	// Second batch
	pm.AddPostconf(context.Background(), "mynetworks", "127.0.0.0/8")
	if err := pm.FlushPostconf(context.Background()); err != nil {
		t.Errorf("Second flush failed: %v", err)
	}

	// Verify cleared again
	postconf, _ = pm.GetPendingChanges()
	if len(postconf) != 0 {
		t.Error("Expected empty queue after second flush")
	}
}

// TestPostconfCommandExecution verifies actual command execution format.
func TestPostconfCommandExecution(t *testing.T) {
	// Create a mock script that logs the exact arguments with indices
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")
	logPath := filepath.Join(tmpDir, "commands.log")

	script := fmt.Sprintf(`#!/bin/bash
# Log $0 and all arguments
echo "CALL: $@" >> %s
exit 0
`, logPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)

	// Test postconf command format
	pm.AddPostconf(context.Background(), "myhostname", "mail.example.com")
	pm.AddPostconf(context.Background(), "mynetworks", "127.0.0.0/8")
	if err := pm.FlushPostconf(context.Background()); err != nil {
		t.Fatalf("FlushPostconf failed: %v", err)
	}

	// Test postconfd command format
	pm.AddPostconfd(context.Background(), "content_filter")
	if err := pm.FlushPostconfd(context.Background()); err != nil {
		t.Fatalf("FlushPostconfd failed: %v", err)
	}

	// Read log to verify command format
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log: %v", err)
	}

	logStr := string(logData)

	// Verify postconf -e format
	// Should contain "-e" flag and key=value arguments
	if !strings.Contains(logStr, "-e") {
		t.Errorf("Expected -e flag in postconf commands. Got: %s", logStr)
	}
	if !strings.Contains(logStr, "myhostname=mail.example.com") {
		t.Errorf("Expected myhostname=mail.example.com in postconf commands. Got: %s", logStr)
	}

	// Verify postconfd -X format
	if !strings.Contains(logStr, "-X") {
		t.Errorf("Expected -X flag in postconfd commands. Got: %s", logStr)
	}
	if !strings.Contains(logStr, "content_filter") {
		t.Errorf("Expected content_filter in postconfd commands. Got: %s", logStr)
	}
}

// TestInterfaceCompliance verifies PostfixManager implements Manager interface.
func TestInterfaceCompliance(t *testing.T) {
	var _ Manager = (*PostfixManager)(nil)
}

// TestBatchedPostconfLargeSet verifies batched execution with 50+ parameters.
func TestBatchedPostconfLargeSet(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")
	logPath := filepath.Join(tmpDir, "calls.log")

	// Count how many times postconf is called
	script := fmt.Sprintf(`#!/bin/bash
echo "CALL" >> %s
echo "ARGS: $#" >> %s
exit 0
`, logPath, logPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)

	// Add 100 postconf parameters
	for i := range 100 {
		key := fmt.Sprintf("param_%d", i)
		value := fmt.Sprintf("value_%d", i)
		if err := pm.AddPostconf(context.Background(), key, value); err != nil {
			t.Fatalf("Failed to add postconf: %v", err)
		}
	}

	// Flush should execute only once
	if err := pm.FlushPostconf(context.Background()); err != nil {
		t.Fatalf("FlushPostconf failed: %v", err)
	}

	// Read log to verify single call
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log: %v", err)
	}

	logStr := string(logData)
	callCount := strings.Count(logStr, "CALL")

	if callCount != 1 {
		t.Errorf("Expected 1 postconf call for 100 parameters, got %d calls", callCount)
	}

	// Verify argument count: -e + 100 key=value pairs = 101 args
	if !strings.Contains(logStr, "ARGS: 101") {
		t.Errorf("Expected 101 arguments (-e + 100 params), got: %s", logStr)
	}
}

// TestBatchedPostconfErrorHandling verifies error handling in batched execution.
func TestBatchedPostconfErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")

	// Fail after reading arguments
	script := `#!/bin/bash
echo "Error processing batch" >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)

	// Add multiple parameters
	pm.AddPostconf(context.Background(), "param1", "value1")
	pm.AddPostconf(context.Background(), "param2", "value2")
	pm.AddPostconf(context.Background(), "param3", "value3")

	// Flush should fail and return error
	err := pm.FlushPostconf(context.Background())
	if err == nil {
		t.Fatal("Expected FlushPostconf to fail but got nil")
	}

	// Error message should indicate batch failure
	if !strings.Contains(err.Error(), "batch") {
		t.Errorf("Expected error message to contain 'batch', got: %v", err)
	}

	// Verify pending changes are NOT cleared on error
	postconf, _ := pm.GetPendingChanges()
	if len(postconf) == 0 {
		t.Error("Expected pending changes to remain after flush failure, but queue was cleared")
	}
}

// TestBatchedPostconfdLargeSet verifies batched deletion with 50+ keys.
func TestBatchedPostconfdLargeSet(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")
	logPath := filepath.Join(tmpDir, "calls.log")

	// Count how many times postconf is called
	script := fmt.Sprintf(`#!/bin/bash
echo "CALL" >> %s
echo "ARGS: $#" >> %s
exit 0
`, logPath, logPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)

	// Add 100 postconfd deletions
	for i := range 100 {
		key := fmt.Sprintf("param_%d", i)
		if err := pm.AddPostconfd(context.Background(), key); err != nil {
			t.Fatalf("Failed to add postconfd: %v", err)
		}
	}

	// Flush should execute only once
	if err := pm.FlushPostconfd(context.Background()); err != nil {
		t.Fatalf("FlushPostconfd failed: %v", err)
	}

	// Read log to verify single call
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log: %v", err)
	}

	logStr := string(logData)
	callCount := strings.Count(logStr, "CALL")

	if callCount != 1 {
		t.Errorf("Expected 1 postconf call for 100 deletions, got %d calls", callCount)
	}

	// Verify argument count: -X + 100 keys = 101 args
	if !strings.Contains(logStr, "ARGS: 101") {
		t.Errorf("Expected 101 arguments (-X + 100 keys), got: %s", logStr)
	}
}

// TestBatchedPostconfdDeduplication verifies duplicate keys are deduplicated during flush.
func TestBatchedPostconfdDeduplication(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")
	logPath := filepath.Join(tmpDir, "calls.log")

	script := fmt.Sprintf(`#!/bin/bash
for arg in "$@"; do
    echo "ARG: $arg" >> %s
done
exit 0
`, logPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm := NewPostfixManager(scriptPath)

	// Add multiple duplicates
	pm.AddPostconfd(context.Background(), "key1")
	pm.AddPostconfd(context.Background(), "key2")
	pm.AddPostconfd(context.Background(), "key1") // duplicate
	pm.AddPostconfd(context.Background(), "key3")
	pm.AddPostconfd(context.Background(), "key2") // duplicate
	pm.AddPostconfd(context.Background(), "key1") // duplicate

	if err := pm.FlushPostconfd(context.Background()); err != nil {
		t.Fatalf("FlushPostconfd failed: %v", err)
	}

	// Read log
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log: %v", err)
	}

	logStr := string(logData)

	// Should have -X flag + 3 unique keys = 4 arguments
	argCount := strings.Count(logStr, "ARG:")
	if argCount != 4 {
		t.Errorf("Expected 4 arguments (-X + 3 unique keys), got %d", argCount)
	}

	// Verify each key appears only once
	key1Count := strings.Count(logStr, "ARG: key1")
	key2Count := strings.Count(logStr, "ARG: key2")
	key3Count := strings.Count(logStr, "ARG: key3")

	if key1Count != 1 || key2Count != 1 || key3Count != 1 {
		t.Errorf("Expected each key to appear once, got key1=%d, key2=%d, key3=%d", key1Count, key2Count, key3Count)
	}
}

// TestPostconfCommandNotFound verifies behavior when postconf is not found.
func TestPostconfCommandNotFound(t *testing.T) {
	pm := NewPostfixManager("/nonexistent/postconf")
	pm.AddPostconf(context.Background(), "myhostname", "mail.example.com")

	err := pm.FlushPostconf(context.Background())
	if err == nil {
		t.Error("Expected error when postconf command not found")
	}

	// Should be exec.Error
	if _, ok := err.(*exec.Error); !ok {
		// Or wrapped error containing exec info
		if !strings.Contains(err.Error(), "postconf") {
			t.Errorf("Expected exec error, got: %v", err)
		}
	}
}

// TestOverwritePostconfKey verifies behavior when same key is added multiple times.
func TestOverwritePostconfKey(t *testing.T) {
	pm := NewPostfixManager("")

	// Add same key multiple times
	pm.AddPostconf(context.Background(), "myhostname", "mail1.example.com")
	pm.AddPostconf(context.Background(), "myhostname", "mail2.example.com")
	pm.AddPostconf(context.Background(), "myhostname", "mail3.example.com")

	postconf, _ := pm.GetPendingChanges()

	// Should only have one entry with latest value
	if len(postconf) != 1 {
		t.Errorf("Expected 1 postconf entry, got %d", len(postconf))
	}

	if postconf["myhostname"] != "mail3.example.com" {
		t.Errorf("Expected latest value 'mail3.example.com', got '%s'", postconf["myhostname"])
	}
}

// TestDuplicatePostconfdKey verifies behavior when same key is deleted multiple times.
func TestDuplicatePostconfdKey(t *testing.T) {
	pm := NewPostfixManager("")

	// Add same deletion multiple times
	pm.AddPostconfd(context.Background(), "content_filter")
	pm.AddPostconfd(context.Background(), "content_filter")
	pm.AddPostconfd(context.Background(), "content_filter")

	_, postconfd := pm.GetPendingChanges()

	// Should have multiple entries in the slice (deduplication happens during flush)
	if len(postconfd) != 3 {
		t.Errorf("Expected 3 postconfd entries in slice, got %d", len(postconfd))
	}

	// But verify that flush will deduplicate them
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mock_postconf.sh")
	logPath := filepath.Join(tmpDir, "calls.log")

	script := fmt.Sprintf(`#!/bin/bash
# Count number of -X arguments
echo "ARGS: $#" >> %s
for arg in "$@"; do
    echo "ARG: $arg" >> %s
done
exit 0
`, logPath, logPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to create mock script: %v", err)
	}

	pm.postconfCmd = scriptPath
	if err := pm.FlushPostconfd(context.Background()); err != nil {
		t.Fatalf("FlushPostconfd failed: %v", err)
	}

	// Read log to verify deduplication
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log: %v", err)
	}

	logStr := string(logData)
	// Should have 2 total arguments: -X and content_filter (deduplicated to 1 key)
	if !strings.Contains(logStr, "ARGS: 2") {
		t.Errorf("Expected 2 arguments (-X + 1 unique key), got: %s", logStr)
	}
}
