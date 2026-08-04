// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"testing"
)

func TestGeneratePassword_Length(t *testing.T) {
	pw, err := GeneratePassword(64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pw) != 64 {
		t.Errorf("expected 64 chars, got %d", len(pw))
	}
}

func TestGeneratePassword_Uniqueness(t *testing.T) {
	pw1, err := GeneratePassword(64)
	if err != nil {
		t.Fatal(err)
	}

	pw2, err := GeneratePassword(64)
	if err != nil {
		t.Fatal(err)
	}

	if pw1 == pw2 {
		t.Error("two generated passwords should not be identical")
	}
}

func TestGeneratePassword_ZeroLength(t *testing.T) {
	_, err := GeneratePassword(0)
	if err == nil {
		t.Error("expected error for zero length")
	}
}

func TestGeneratePassword_NegativeLength(t *testing.T) {
	_, err := GeneratePassword(-1)
	if err == nil {
		t.Error("expected error for negative length")
	}
}

func TestGeneratePassword_ShortLength(t *testing.T) {
	pw, err := GeneratePassword(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pw) != 1 {
		t.Errorf("expected 1 char, got %d", len(pw))
	}
}

func TestGeneratePassword_UsesCharset(t *testing.T) {
	pw, err := GeneratePassword(1000)
	if err != nil {
		t.Fatal(err)
	}

	// With 1000 chars from a 64 char charset, we should see variety
	unique := make(map[byte]bool)
	for i := range len(pw) {
		unique[pw[i]] = true
	}

	// Should have at least 20 unique characters in 1000 random picks
	if len(unique) < 20 {
		t.Errorf("expected diverse charset usage, only got %d unique chars", len(unique))
	}
}

// TestGeneratePassword_ShellSafe guards the contract that broke zmmyinit: the
// generated value is interpolated unquoted into `su - zextras -c "..."` by
// several libexec scripts, so it must contain nothing a shell would treat as
// syntax.
func TestGeneratePassword_ShellSafe(t *testing.T) {
	pw, err := GeneratePassword(4096)
	if err != nil {
		t.Fatal(err)
	}

	for i := range len(pw) {
		c := pw[i]

		alnum := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !alnum && c != '_' && c != '.' {
			t.Fatalf("password contains shell-unsafe byte %q: %q", c, pw)
		}
	}
}
