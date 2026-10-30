// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestExitWith(t *testing.T) {
	if err := exitWith(0); err != nil {
		t.Errorf("exitWith(0) = %v, want nil", err)
	}

	err := exitWith(3)

	var status exitStatus
	if !errors.As(err, &status) || status != 3 {
		t.Errorf("exitWith(3) = %v, want exitStatus(3)", err)
	}
}

// TestExitCode pins the process-level contract of every Kong command: success
// is 0; a command that already reported on stderr chooses its own status and
// adds nothing; any other error is "Error: <err>" on stderr with status 1.
func TestExitCode(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantStderr string
	}{
		{"nil", nil, 0, ""},
		{"own status", exitStatus(80), 80, ""},
		{"wrapped own status", fmt.Errorf("wrapped: %w", exitStatus(2)), 2, ""},
		{"plain error", errors.New("boom"), 1, "Error: boom\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			if got := exitCode(tt.err, &stderr); got != tt.wantCode {
				t.Errorf("exitCode = %d, want %d", got, tt.wantCode)
			}

			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
