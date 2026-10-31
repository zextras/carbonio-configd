// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package security

import (
	"bytes"
	"os/user"
	"strings"
	"testing"
)

func TestCheckUserPermissions(t *testing.T) {
	// Get the current user for testing
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("Failed to get current user: %v", err)
	}

	tests := []struct {
		name        string
		wantErr     bool
		description string
	}{
		{
			name:        "Check current user",
			wantErr:     currentUser.Username != RequiredUser,
			description: "Should pass if running as zextras, fail otherwise",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckUserPermissions(OSUser)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckUserPermissions() error = %v, wantErr %v", err, tt.wantErr)
				t.Logf("Current user: %s, Required user: %s", currentUser.Username, RequiredUser)
			}

			if err != nil {
				t.Logf("Expected error (not running as %s): %v", RequiredUser, err)
			}
		})
	}
}

func TestCheckUserPermissions_ErrorMessages(t *testing.T) {
	// Get the current user
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("Failed to get current user: %v", err)
	}

	// Test when not running as required user
	if currentUser.Username != RequiredUser {
		err := CheckUserPermissions(OSUser)
		if err == nil {
			t.Error("CheckUserPermissions() should return error when not running as required user")
		}

		// Verify error message contains both usernames
		errMsg := err.Error()
		if !strings.Contains(errMsg, RequiredUser) {
			t.Errorf("Error message should contain required user '%s': %v", RequiredUser, errMsg)
		}
		if !strings.Contains(errMsg, currentUser.Username) {
			t.Errorf("Error message should contain current user '%s': %v", currentUser.Username, errMsg)
		}
	}
}

func TestMustCheckUserPermissions(t *testing.T) {
	var stderr bytes.Buffer

	// Get the current user
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("Failed to get current user: %v", err)
	}

	// Call MustCheckUserPermissions
	err = MustCheckUserPermissions(OSUser, &stderr)
	stderrOutput := stderr.String()

	// Verify behavior based on current user
	if currentUser.Username == RequiredUser {
		// Should succeed with no error
		if err != nil {
			t.Errorf("MustCheckUserPermissions() should not error when running as '%s': %v", RequiredUser, err)
		}
		// No stderr output expected
		if stderrOutput != "" {
			t.Errorf("MustCheckUserPermissions() should not write to stderr when running as '%s', got: %s", RequiredUser, stderrOutput)
		}
	} else {
		// Should return error
		if err == nil {
			t.Errorf("MustCheckUserPermissions() should return error when not running as '%s'", RequiredUser)
		}

		// Verify stderr output
		if !strings.Contains(stderrOutput, RequiredUser) {
			t.Errorf("stderr should contain required user '%s', got: %s", RequiredUser, stderrOutput)
		}
		if !strings.Contains(stderrOutput, "Please run this program as") {
			t.Errorf("stderr should contain usage message, got: %s", stderrOutput)
		}
	}
}

func TestMustCheckUserPermissions_StderrFormat(t *testing.T) {
	// Get the current user
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("Failed to get current user: %v", err)
	}

	// Only test stderr format if NOT running as required user
	if currentUser.Username == RequiredUser {
		t.Skipf("Skipping stderr format test - running as required user '%s'", RequiredUser)
	}

	var stderr bytes.Buffer

	_ = MustCheckUserPermissions(OSUser, &stderr)
	stderrOutput := stderr.String()

	// Verify stderr contains the expected message
	if !strings.Contains(stderrOutput, "Please run this program as") {
		t.Errorf("stderr should contain 'Please run this program as', got: %s", stderrOutput)
	}

	// Verify it contains the required user
	if !strings.Contains(stderrOutput, RequiredUser) {
		t.Errorf("stderr should contain required user '%s', got: %s", RequiredUser, stderrOutput)
	}
}

func TestRequiredUserConstant(t *testing.T) {
	// Verify the required user constant is set correctly
	if RequiredUser != "zextras" {
		t.Errorf("RequiredUser constant should be 'zextras', got '%s'", RequiredUser)
	}
}
