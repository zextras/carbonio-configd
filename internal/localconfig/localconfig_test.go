// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadLocalConfigFromFile_Success tests successful parsing of a valid localconfig.xml
func TestLoadLocalConfigFromFile_Success(t *testing.T) {
	// Create temporary test file
	xmlContent := `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<localconfig>
  <key name="zimbra_home">
    <value>/opt/zextras</value>
  </key>
  <key name="ldap_host">
    <value>localhost</value>
  </key>
  <key name="ldap_port">
    <value>389</value>
  </key>
  <key name="zimbra_server_hostname">
    <value>mail.example.com</value>
  </key>
  <key name="ldap_root_password">
    <value>secret123</value>
  </key>
</localconfig>`

	tmpFile := createTempFile(t, "localconfig-*.xml", xmlContent)
	defer os.Remove(tmpFile)

	// Test parsing
	config, err := LoadLocalConfigFromFile(tmpFile)
	if err != nil {
		t.Fatalf("LoadLocalConfigFromFile() failed: %v", err)
	}

	// Verify expected keys
	expectedKeys := map[string]string{
		"zimbra_home":            "/opt/zextras",
		"ldap_host":              "localhost",
		"ldap_port":              "389",
		"zimbra_server_hostname": "mail.example.com",
		"ldap_root_password":     "secret123",
	}

	if len(config) != len(expectedKeys) {
		t.Errorf("Expected %d keys, got %d", len(expectedKeys), len(config))
	}

	for key, expectedValue := range expectedKeys {
		actualValue, exists := config[key]
		if !exists {
			t.Errorf("Key %s not found in parsed config", key)
			continue
		}
		if actualValue != expectedValue {
			t.Errorf("Key %s: expected %q, got %q", key, expectedValue, actualValue)
		}
	}
}

// TestLoadLocalConfigFromFile_WhitespaceHandling tests that whitespace is properly trimmed
func TestLoadLocalConfigFromFile_WhitespaceHandling(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
  <key name="test_key">
    <value>
      value_with_whitespace
    </value>
  </key>
  <key name="another_key">
    <value>no_whitespace</value>
  </key>
</localconfig>`

	tmpFile := createTempFile(t, "localconfig-whitespace-*.xml", xmlContent)
	defer os.Remove(tmpFile)

	config, err := LoadLocalConfigFromFile(tmpFile)
	if err != nil {
		t.Fatalf("LoadLocalConfigFromFile() failed: %v", err)
	}

	// Verify whitespace is trimmed
	if config["test_key"] != "value_with_whitespace" {
		t.Errorf("Expected 'value_with_whitespace', got %q", config["test_key"])
	}
	if config["another_key"] != "no_whitespace" {
		t.Errorf("Expected 'no_whitespace', got %q", config["another_key"])
	}
}

// TestLoadLocalConfigFromFile_EmptyValues tests handling of empty values
func TestLoadLocalConfigFromFile_EmptyValues(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
  <key name="empty_key">
    <value></value>
  </key>
  <key name="another_empty_key">
    <value/>
  </key>
  <key name="normal_key">
    <value>normal_value</value>
  </key>
</localconfig>`

	tmpFile := createTempFile(t, "localconfig-empty-*.xml", xmlContent)
	defer os.Remove(tmpFile)

	config, err := LoadLocalConfigFromFile(tmpFile)
	if err != nil {
		t.Fatalf("LoadLocalConfigFromFile() failed: %v", err)
	}

	// Verify empty values are handled
	if config["empty_key"] != "" {
		t.Errorf("Expected empty string for empty_key, got %q", config["empty_key"])
	}
	if config["another_empty_key"] != "" {
		t.Errorf("Expected empty string for another_empty_key, got %q", config["another_empty_key"])
	}
	if config["normal_key"] != "normal_value" {
		t.Errorf("Expected 'normal_value', got %q", config["normal_key"])
	}
}

// TestLoadLocalConfigFromFile_FileNotFound tests error handling for missing file
func TestLoadLocalConfigFromFile_FileNotFound(t *testing.T) {
	config, err := LoadLocalConfigFromFile("/nonexistent/path/localconfig.xml")
	if err == nil {
		t.Fatal("Expected error for nonexistent file, got nil")
	}
	if config != nil {
		t.Errorf("Expected nil config for error case, got %v", config)
	}
}

// TestLoadLocalConfigFromFile_InvalidXML tests error handling for malformed XML
func TestLoadLocalConfigFromFile_InvalidXML(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
  <key name="test">
    <value>unclosed_tag
  </key>
</localconfig`

	tmpFile := createTempFile(t, "localconfig-invalid-*.xml", xmlContent)
	defer os.Remove(tmpFile)

	config, err := LoadLocalConfigFromFile(tmpFile)
	if err == nil {
		t.Fatal("Expected error for invalid XML, got nil")
	}
	if config != nil {
		t.Errorf("Expected nil config for error case, got %v", config)
	}
}

// TestFormatAsKeyValue tests conversion to key=value format
func TestFormatAsKeyValue(t *testing.T) {
	config := map[string]string{
		"ldap_host":       "localhost",
		"ldap_port":       "389",
		"zimbra_home":     "/opt/zextras",
		"empty_value_key": "",
	}

	output := FormatAsKeyValue(config)

	// Verify all keys are present in output
	for key, value := range config {
		expectedLine := key + " = " + value
		if !strings.Contains(output, expectedLine) {
			t.Errorf("Expected output to contain %q, but it didn't.\nOutput:\n%s", expectedLine, output)
		}
	}

	// Verify each line has correct format
	lines := strings.Split(output, "\n")
	nonEmptyLines := 0
	for _, line := range lines {
		if line == "" {
			continue // Skip empty lines
		}
		nonEmptyLines++
		if !strings.Contains(line, " = ") {
			t.Errorf("Line %q does not match 'key = value' format", line)
		}
	}

	if nonEmptyLines != len(config) {
		t.Errorf("Expected %d non-empty lines, got %d", len(config), nonEmptyLines)
	}
}

// TestFormatAsKeyValue_EmptyMap tests formatting of empty config
func TestFormatAsKeyValue_EmptyMap(t *testing.T) {
	config := map[string]string{}
	output := FormatAsKeyValue(config)

	if output != "" {
		t.Errorf("Expected empty string for empty config, got %q", output)
	}
}

// TestLoadLocalConfig_UsesDefaultPath tests that LoadLocalConfig uses the default path
func TestLoadLocalConfig_UsesDefaultPath(t *testing.T) {
	// This test will only pass if running in actual Carbonio environment
	// In test environment, it should fail gracefully
	_, err := LoadLocalConfig()

	// We expect an error in test environment (file doesn't exist)
	// This is fine - we're just testing that it tries to read from the right path
	if err != nil && !strings.Contains(err.Error(), "failed to read localconfig file") {
		t.Errorf("Expected 'failed to read localconfig file' error, got: %v", err)
	}
}

// TestLoadLocalConfigFromFile_SpecialCharacters tests handling of special characters in values
func TestLoadLocalConfigFromFile_SpecialCharacters(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
  <key name="password_with_special">
    <value>P@ssw0rd!&lt;&gt;&amp;</value>
  </key>
  <key name="path_with_spaces">
    <value>/path with spaces/file.txt</value>
  </key>
  <key name="unicode_value">
    <value>日本語テスト</value>
  </key>
</localconfig>`

	tmpFile := createTempFile(t, "localconfig-special-*.xml", xmlContent)
	defer os.Remove(tmpFile)

	config, err := LoadLocalConfigFromFile(tmpFile)
	if err != nil {
		t.Fatalf("LoadLocalConfigFromFile() failed: %v", err)
	}

	// Verify XML entities are decoded
	if config["password_with_special"] != "P@ssw0rd!<>&" {
		t.Errorf("Expected XML entities to be decoded, got %q", config["password_with_special"])
	}

	// Verify spaces are preserved
	if config["path_with_spaces"] != "/path with spaces/file.txt" {
		t.Errorf("Expected spaces to be preserved, got %q", config["path_with_spaces"])
	}

	// Verify Unicode is handled
	if config["unicode_value"] != "日本語テスト" {
		t.Errorf("Expected Unicode to be preserved, got %q", config["unicode_value"])
	}
}

// Helper function to create temporary test file
func createTempFile(t *testing.T, pattern, content string) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	tmpFile.Close()
	return tmpFile.Name()
}

// BenchmarkLoadLocalConfigFromFile benchmarks XML parsing performance
func BenchmarkLoadLocalConfigFromFile(b *testing.B) {
	// Create a realistic test file with 100 keys
	var xmlBuilder strings.Builder
	xmlBuilder.WriteString(`<?xml version="1.0" encoding="UTF-8"?><localconfig>`)

	for i := 0; i < 100; i++ {
		xmlBuilder.WriteString(`<key name="test_key_`)
		xmlBuilder.WriteString(strings.Repeat("a", i%10)) // Variable length keys
		xmlBuilder.WriteString(`"><value>test_value_`)
		xmlBuilder.WriteString(strings.Repeat("b", i%20)) // Variable length values
		xmlBuilder.WriteString(`</value></key>`)
	}
	xmlBuilder.WriteString(`</localconfig>`)

	tmpFile := filepath.Join(b.TempDir(), "benchmark-localconfig.xml")
	if err := os.WriteFile(tmpFile, []byte(xmlBuilder.String()), 0600); err != nil {
		b.Fatalf("Failed to create benchmark file: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadLocalConfigFromFile(tmpFile)
		if err != nil {
			b.Fatalf("LoadLocalConfigFromFile() failed: %v", err)
		}
	}
}

// TestLoadResolvedConfigFromFile_Basic tests the LoadResolvedConfigFromFile function
func TestLoadResolvedConfigFromFile_Basic(t *testing.T) {
	t.Run("resolves variable references", func(t *testing.T) {
		xmlContent := `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<localconfig>
  <key name="zimbra_home">
    <value>/opt/zextras</value>
  </key>
  <key name="zimbra_log_directory">
    <value>${zimbra_home}/log</value>
  </key>
</localconfig>`

		tmpFile := filepath.Join(t.TempDir(), "localconfig.xml")
		if err := os.WriteFile(tmpFile, []byte(xmlContent), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		result, err := LoadResolvedConfigFromFile(tmpFile)
		if err != nil {
			t.Fatalf("LoadResolvedConfigFromFile() failed: %v", err)
		}

		// Variable reference should be resolved
		if result["zimbra_log_directory"] != "/opt/zextras/log" {
			t.Errorf("zimbra_log_directory = %q, expected %q", result["zimbra_log_directory"], "/opt/zextras/log")
		}
	})

	t.Run("merges defaults for missing keys", func(t *testing.T) {
		xmlContent := `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<localconfig>
  <key name="zimbra_home">
    <value>/opt/zextras</value>
  </key>
</localconfig>`

		tmpFile := filepath.Join(t.TempDir(), "localconfig.xml")
		if err := os.WriteFile(tmpFile, []byte(xmlContent), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		result, err := LoadResolvedConfigFromFile(tmpFile)
		if err != nil {
			t.Fatalf("LoadResolvedConfigFromFile() failed: %v", err)
		}

		// Result should contain the explicit key
		if result["zimbra_home"] != "/opt/zextras" {
			t.Errorf("zimbra_home = %q, expected %q", result["zimbra_home"], "/opt/zextras")
		}

		// Should also have defaults merged in
		if len(result) <= 1 {
			t.Error("Expected defaults to be merged, but result only has 1 entry")
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := LoadResolvedConfigFromFile("/nonexistent/path/localconfig.xml")
		if err == nil {
			t.Error("Expected error for missing file")
		}
	})

	t.Run("returns error for invalid XML", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "localconfig.xml")
		if err := os.WriteFile(tmpFile, []byte("not valid xml <<<"), 0644); err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		_, err := LoadResolvedConfigFromFile(tmpFile)
		if err == nil {
			t.Error("Expected error for invalid XML")
		}
	})
}

// TestLoadResolvedConfig_DefaultPathErrors tests that LoadResolvedConfig errors gracefully in test env
func TestLoadResolvedConfig_DefaultPathErrors(t *testing.T) {
	// In the test environment, the default path does not exist.
	// The function body (calling LoadResolvedConfigFromFile) still executes and returns an error.
	_, err := LoadResolvedConfig()
	if err == nil {
		// In a real Carbonio installation the file exists and this succeeds; skip gracefully.
		t.Skip("Default localconfig.xml exists — skipping error path test")
	}
	// Error is expected in test environments where /opt/zextras is not present.
	if !strings.Contains(err.Error(), "failed to read localconfig file") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// BenchmarkFormatAsKeyValue benchmarks key=value formatting
func BenchmarkFormatAsKeyValue(b *testing.B) {
	config := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		config[strings.Repeat("key", i%10)] = strings.Repeat("value", i%20)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FormatAsKeyValue(config)
	}
}
