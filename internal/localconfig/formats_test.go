// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatPlain(t *testing.T) {
	config := map[string]string{"b_key": "val2", "a_key": "val1"}

	var buf bytes.Buffer
	FormatPlain(&buf, config)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	if lines[0] != "a_key = val1" {
		t.Errorf("expected sorted first line 'a_key = val1', got %q", lines[0])
	}

	if lines[1] != "b_key = val2" {
		t.Errorf("expected sorted second line 'b_key = val2', got %q", lines[1])
	}
}

func TestFormatShell(t *testing.T) {
	config := map[string]string{"zimbra_home": "/opt/zextras"}

	var buf bytes.Buffer
	FormatShell(&buf, config)

	expected := "zimbra_home='/opt/zextras';\n"
	if buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, buf.String())
	}
}

func TestFormatShell_SingleQuoteEscaping(t *testing.T) {
	config := map[string]string{"key": "it's a test"}

	var buf bytes.Buffer
	FormatShell(&buf, config)

	expected := "key='it'\\''s a test';\n"
	if buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, buf.String())
	}
}

func TestFormatExport(t *testing.T) {
	config := map[string]string{"zimbra_home": "/opt/zextras"}

	var buf bytes.Buffer
	FormatExport(&buf, config)

	expected := "export zimbra_home='/opt/zextras';\n"
	if buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, buf.String())
	}
}

func TestFormatExport_MultipleKeys(t *testing.T) {
	config := map[string]string{"b": "2", "a": "1"}

	var buf bytes.Buffer
	FormatExport(&buf, config)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	if !strings.HasPrefix(lines[0], "export a=") {
		t.Errorf("expected sorted output, first line: %q", lines[0])
	}
}

func TestFormatNokey_OrderedKeys(t *testing.T) {
	config := map[string]string{"a": "first", "b": "second", "c": "third"}

	var buf bytes.Buffer
	FormatNokey(&buf, config, []string{"c", "a"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	if lines[0] != "third" {
		t.Errorf("expected 'third' first (ordered), got %q", lines[0])
	}

	if lines[1] != "first" {
		t.Errorf("expected 'first' second (ordered), got %q", lines[1])
	}
}

func TestFormatNokey_Alphabetical(t *testing.T) {
	config := map[string]string{"b": "second", "a": "first"}

	var buf bytes.Buffer
	FormatNokey(&buf, config, nil)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if lines[0] != "first" {
		t.Errorf("expected alphabetical order, got %q first", lines[0])
	}
}

func TestFormatXML(t *testing.T) {
	config := map[string]string{"zimbra_home": "/opt/zextras"}

	var buf bytes.Buffer
	err := FormatXML(&buf, config)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Error("missing XML header")
	}

	if !strings.Contains(output, `<key name="zimbra_home">`) {
		t.Error("missing key element")
	}

	if !strings.Contains(output, `<value>/opt/zextras</value>`) {
		t.Error("missing value element")
	}
}

func TestMaskPasswords(t *testing.T) {
	config := map[string]string{
		"zimbra_home":          "/opt/zextras",
		"zimbra_ldap_password": "secret123",
		"ldap_root_secret":     "topsecret",
		"mailboxd_java_pass":   "javapass",
		"normal_key":           "normalval",
	}

	masked := MaskPasswords(config)

	if masked["zimbra_home"] != "/opt/zextras" {
		t.Error("non-sensitive key should not be masked")
	}

	if masked["normal_key"] != "normalval" {
		t.Error("non-sensitive key should not be masked")
	}

	if masked["zimbra_ldap_password"] != "**********" {
		t.Errorf("password key should be masked, got %q", masked["zimbra_ldap_password"])
	}

	if masked["ldap_root_secret"] != "**********" {
		t.Errorf("secret key should be masked, got %q", masked["ldap_root_secret"])
	}

	if masked["mailboxd_java_pass"] != "**********" {
		t.Errorf("_pass key should be masked, got %q", masked["mailboxd_java_pass"])
	}
}

func TestMaskPasswords_DoesNotMutateOriginal(t *testing.T) {
	config := map[string]string{"zimbra_ldap_password": "secret123"}

	_ = MaskPasswords(config)

	if config["zimbra_ldap_password"] != "secret123" {
		t.Error("MaskPasswords should not mutate the original map")
	}
}

func TestShellEscape(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"it's", "it'\\''s"},
		{"no quotes", "no quotes"},
		{"''", "'\\'''\\''"},
		{"", ""},
	}

	for _, tt := range tests {
		got := ShellEscape(tt.input)
		if got != tt.expected {
			t.Errorf("ShellEscape(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatPlain_Empty(t *testing.T) {
	var buf bytes.Buffer
	FormatPlain(&buf, map[string]string{})

	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty config, got %q", buf.String())
	}
}
