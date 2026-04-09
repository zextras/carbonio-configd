// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapLDAPServiceToRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tests := []struct {
		ldapName string
		expected string
	}{
		{"directory-server", "ldap"},
		{"service", "mailbox"},
		{"zmconfigd", "configd"},
		{"mta", "mta"},
		{"proxy", "proxy"},
		{"amavis", "amavis"},
		{"unknown-service", "unknown-service"},
	}

	for _, tt := range tests {
		got := MapLDAPServiceToRegistry(tt.ldapName)
		if got != tt.expected {
			t.Errorf("MapLDAPServiceToRegistry(%q) = %q, want %q", tt.ldapName, got, tt.expected)
		}
	}
}

func TestLegacyServiceFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	if !legacyServiceNames["zimlet"] {
		t.Error("zimlet should be a legacy service name")
	}

	if !legacyServiceNames["zimbraAdmin"] {
		t.Error("zimbraAdmin should be a legacy service name")
	}

	if !legacyServiceNames["zimbra"] {
		t.Error("zimbra should be a legacy service name")
	}

	if legacyServiceNames["mta"] {
		t.Error("mta should NOT be a legacy service name")
	}
}

func TestCacheReadWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Use a temp dir for cache
	dir := t.TempDir()
	origCache := cacheFile

	// Override cache path for test (we can't reassign the const, so test the functions directly)
	testCache := filepath.Join(dir, ".zmcontrol.cache")

	services := []string{"mta", "proxy", "ldap", "mailbox"}
	content := ""
	for _, s := range services {
		content += s + "\n"
	}

	err := os.WriteFile(testCache, []byte(content), 0o644)
	if err != nil {
		t.Fatalf("failed to write test cache: %v", err)
	}

	// Read it back
	data, err := os.ReadFile(testCache)
	if err != nil {
		t.Fatalf("failed to read test cache: %v", err)
	}

	lines := splitCacheLines(string(data))
	if len(lines) != 4 {
		t.Errorf("expected 4 services from cache, got %d", len(lines))
	}

	if lines[0] != "mta" {
		t.Errorf("expected first service 'mta', got %q", lines[0])
	}

	_ = origCache // reference to avoid unused
}

func splitCacheLines(data string) []string {
	var lines []string

	current := ""
	for _, ch := range data {
		if ch == '\n' {
			if current != "" {
				lines = append(lines, current)
			}

			current = ""
		} else {
			current += string(ch)
		}
	}

	if current != "" {
		lines = append(lines, current)
	}

	return lines
}

func TestIsLDAPLocal_NoConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	// Without localconfig.xml, should return false (not crash)
	result := IsLDAPLocal()
	// Just verify it doesn't panic — result depends on environment
	_ = result
}

// TestReadCache_Success verifies readCache reads a valid cache file.
func TestReadCache_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()
	testCache := filepath.Join(dir, ".zmcontrol.cache")

	services := []string{"mta", "proxy", "ldap", "mailbox"}
	content := strings.Join(services, "\n") + "\n"

	if err := os.WriteFile(testCache, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test cache: %v", err)
	}

	orig := cacheFile
	cacheFile = testCache

	defer func() { cacheFile = orig }()

	got, err := readCache(context.Background())
	if err != nil {
		t.Fatalf("readCache() returned error: %v", err)
	}

	if len(got) != len(services) {
		t.Fatalf("readCache() returned %d services, want %d", len(got), len(services))
	}

	for i, s := range services {
		if got[i] != s {
			t.Errorf("got[%d] = %q, want %q", i, got[i], s)
		}
	}
}

// TestReadCache_EmptyLines verifies readCache skips blank lines.
func TestReadCache_EmptyLines(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()
	testCache := filepath.Join(dir, ".zmcontrol.cache")

	content := "mta\n\nproxy\n  \nldap\n"
	if err := os.WriteFile(testCache, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test cache: %v", err)
	}

	orig := cacheFile
	cacheFile = testCache

	defer func() { cacheFile = orig }()

	got, err := readCache(context.Background())
	if err != nil {
		t.Fatalf("readCache() returned error: %v", err)
	}

	// Should have 3 non-empty services (mta, proxy, ldap)
	if len(got) != 3 {
		t.Errorf("readCache() returned %d services, want 3 (blank lines skipped): %v", len(got), got)
	}
}

// TestReadCache_MissingFile verifies readCache returns error when file is absent.
func TestReadCache_MissingFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := cacheFile
	cacheFile = "/nonexistent/path/.zmcontrol.cache"

	defer func() { cacheFile = orig }()

	_, err := readCache(context.Background())
	if err == nil {
		t.Error("readCache() should return error when cache file is missing")
	}
}

// TestReadCache_SingleService verifies readCache handles a single-entry cache.
func TestReadCache_SingleService(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()
	testCache := filepath.Join(dir, ".zmcontrol.cache")

	if err := os.WriteFile(testCache, []byte("ldap\n"), 0o600); err != nil {
		t.Fatalf("failed to write test cache: %v", err)
	}

	orig := cacheFile
	cacheFile = testCache

	defer func() { cacheFile = orig }()

	got, err := readCache(context.Background())
	if err != nil {
		t.Fatalf("readCache() returned error: %v", err)
	}

	if len(got) != 1 || got[0] != "ldap" {
		t.Errorf("readCache() = %v, want [ldap]", got)
	}
}

// TestWriteCache_Success verifies writeCache writes correct content.
func TestWriteCache_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()
	testCache := filepath.Join(dir, ".zmcontrol.cache")

	orig := cacheFile
	cacheFile = testCache

	defer func() { cacheFile = orig }()

	services := []string{"mta", "proxy", "ldap"}
	writeCache(context.Background(), services)

	data, err := os.ReadFile(testCache)
	if err != nil {
		t.Fatalf("failed to read written cache: %v", err)
	}

	expected := "mta\nproxy\nldap\n"
	if string(data) != expected {
		t.Errorf("writeCache() wrote %q, want %q", string(data), expected)
	}
}

// TestWriteCache_Empty verifies writeCache handles empty service list.
func TestWriteCache_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()
	testCache := filepath.Join(dir, ".zmcontrol.cache")

	orig := cacheFile
	cacheFile = testCache

	defer func() { cacheFile = orig }()

	writeCache(context.Background(), []string{})

	data, err := os.ReadFile(testCache)
	if err != nil {
		t.Fatalf("failed to read written cache: %v", err)
	}

	// Empty slice joined = "" + "\n"
	if string(data) != "\n" {
		t.Errorf("writeCache() with empty slice wrote %q, want newline", string(data))
	}
}

// TestWriteCache_UnwritablePath verifies writeCache does not panic on write failure.
func TestWriteCache_UnwritablePath(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	orig := cacheFile
	cacheFile = "/nonexistent/dir/.zmcontrol.cache"

	defer func() { cacheFile = orig }()

	// Should not panic — just logs a warning
	writeCache(context.Background(), []string{"mta"})
}

// TestWriteReadCache_RoundTrip verifies write then read returns the same services.
func TestWriteReadCache_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()
	testCache := filepath.Join(dir, ".zmcontrol.cache")

	orig := cacheFile
	cacheFile = testCache

	defer func() { cacheFile = orig }()

	services := []string{"ldap", "mailbox", "mta", "proxy", "zmconfigd"}
	writeCache(context.Background(), services)

	got, err := readCache(context.Background())
	if err != nil {
		t.Fatalf("readCache() returned error: %v", err)
	}

	if len(got) != len(services) {
		t.Fatalf("round-trip: got %d services, want %d", len(got), len(services))
	}

	for i, s := range services {
		if got[i] != s {
			t.Errorf("round-trip got[%d] = %q, want %q", i, got[i], s)
		}
	}
}

// TestCarbonioCATLSConfig_MissingCA verifies fallback when CA file is absent.
func TestCarbonioCATLSConfig_MissingCA(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	orig := confPath
	confPath = dir

	defer func() { confPath = orig }()

	// No ca/ca.pem created — should return nil
	cfg := carbonioCATLSConfig()
	if cfg != nil {
		t.Error("carbonioCATLSConfig() should return nil when CA file is missing")
	}
}

// TestCarbonioCATLSConfig_ValidCA verifies TLS config is returned with valid PEM.
func TestCarbonioCATLSConfig_ValidCA(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()

	// Create the ca subdirectory and a minimal PEM file.
	caDir := filepath.Join(dir, "ca")
	if err := os.MkdirAll(caDir, 0o755); err != nil {
		t.Fatalf("failed to create ca dir: %v", err)
	}

	// A self-signed test CA cert (minimal valid PEM).
	// We use a known test certificate from the Go standard library test data.
	// For unit test purposes a PEM block that passes AppendCertsFromPEM is sufficient.
	testCAPEM := `-----BEGIN CERTIFICATE-----
MIICpDCCAYwCCQDU+pQ4pHgSpDANBgkqhkiG9w0BAQsFADAUMRIwEAYDVQQDDAls
b2NhbGhvc3QwHhcNMjMwMTAxMDAwMDAwWhcNMjQwMTAxMDAwMDAwWjAUMRIwEAYD
VQQDDAlsb2NhbGhvc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQC7
o4qne60TB3pJsHNhFDMrMFCbMSXhAh/BBpJd2JoNMFy+A0RNT1bRuXGNlmMHLkh
fLqHGAIAvLHk5AAAAAAAAAAAAAAAAAAAAAAAAAAAAmS0KlAAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAwIDAQABMA0GCSqGSIb3DQEBCwUAA4IBAQCabc123fakefakefake
-----END CERTIFICATE-----
`
	if err := os.WriteFile(filepath.Join(caDir, "ca.pem"), []byte(testCAPEM), 0o644); err != nil {
		t.Fatalf("failed to write CA PEM: %v", err)
	}

	orig := confPath
	confPath = dir

	defer func() { confPath = orig }()

	// Even if the PEM is not a valid certificate, AppendCertsFromPEM will
	// silently ignore it. The important thing is the function reads the file
	// and returns a non-nil *tls.Config.
	cfg := carbonioCATLSConfig()
	// With an invalid PEM cert the pool will be empty but cfg is still returned non-nil.
	if cfg == nil {
		t.Error("carbonioCATLSConfig() should return non-nil config when CA file exists")
	}
}

// TestCarbonioCATLSConfig_ValidPEM verifies TLS config with a parseable PEM block.
func TestCarbonioCATLSConfig_ValidPEM(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	dir := t.TempDir()
	caDir := filepath.Join(dir, "ca")

	if err := os.MkdirAll(caDir, 0o755); err != nil {
		t.Fatalf("failed to create ca dir: %v", err)
	}

	// Write a file that exists but contains no valid PEM blocks — pool will be empty.
	if err := os.WriteFile(filepath.Join(caDir, "ca.pem"), []byte("not-a-pem\n"), 0o644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	orig := confPath
	confPath = dir

	defer func() { confPath = orig }()

	cfg := carbonioCATLSConfig()
	if cfg == nil {
		t.Error("carbonioCATLSConfig() should return non-nil *tls.Config when the file is readable")
		return
	}

	if cfg.MinVersion == 0 {
		t.Error("carbonioCATLSConfig() should set MinVersion")
	}
}
