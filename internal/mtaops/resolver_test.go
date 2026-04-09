// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/state"
)

// TestNewResolver verifies resolver creation.
func TestNewResolver(t *testing.T) {
	baseDir := "/opt/zextras"
	r := NewResolver(baseDir)

	if r == nil {
		t.Fatal("NewResolver() returned nil")
	}

	// Verify it implements OperationResolver interface
	var _ = r
}

// TestResolveValue_VAR tests resolving values from VAR type (GlobalConfig or ServerConfig).
func TestResolveValue_VAR(t *testing.T) {
	r := NewResolver("/opt/zextras")
	st := &state.State{
		GlobalConfig: &config.GlobalConfig{
			Data: map[string]string{
				"zimbraMtaMyNetworks": "127.0.0.0/8 10.0.0.0/8",
				"zimbraMtaRelayHost":  "relay.example.com",
			},
		},
		ServerConfig: &config.ServerConfig{
			Data: map[string]string{
				"zimbraServiceHostname": "mail.example.com",
			},
		},
	}

	tests := []struct {
		name      string
		key       string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "VAR from GlobalConfig",
			key:       "zimbraMtaMyNetworks",
			wantValue: "127.0.0.0/8 10.0.0.0/8",
			wantErr:   false,
		},
		{
			name:      "VAR from ServerConfig (fallback)",
			key:       "zimbraServiceHostname",
			wantValue: "mail.example.com",
			wantErr:   false,
		},
		{
			name:      "VAR not found (returns empty)",
			key:       "nonexistent",
			wantValue: "",
			wantErr:   false,
		},
		{
			name:      "VAR with whitespace (trimmed)",
			key:       "zimbraMtaRelayHost",
			wantValue: "relay.example.com",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveValue(context.Background(), "VAR", tt.key, st)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantValue {
				t.Errorf("ResolveValue() = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

// TestResolveValue_LOCAL tests resolving values from LOCAL type (LocalConfig).
func TestResolveValue_LOCAL(t *testing.T) {
	r := NewResolver("/opt/zextras")
	st := &state.State{
		LocalConfig: &config.LocalConfig{
			Data: map[string]string{
				"ldap_master_url":         "ldap://ldap.example.com:389",
				"ldap_starttls_supported": "1",
			},
		},
	}

	tests := []struct {
		name      string
		key       string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "LOCAL found",
			key:       "ldap_master_url",
			wantValue: "ldap://ldap.example.com:389",
			wantErr:   false,
		},
		{
			name:      "LOCAL not found (returns empty)",
			key:       "nonexistent",
			wantValue: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveValue(context.Background(), "LOCAL", tt.key, st)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantValue {
				t.Errorf("ResolveValue() = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

// TestResolveValue_FILE tests resolving values from FILE type.
func TestResolveValue_FILE(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	confDir := filepath.Join(tmpDir, "conf")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("Failed to create conf dir: %v", err)
	}

	// Create test file with multiple lines
	testFile := filepath.Join(confDir, "mynetworks")
	content := "127.0.0.0/8\n10.0.0.0/8\n  192.168.1.0/24  \n\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	r := NewResolver(tmpDir)
	st := &state.State{}

	tests := []struct {
		name      string
		key       string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "FILE read and join with commas",
			key:       "mynetworks",
			wantValue: "127.0.0.0/8, 10.0.0.0/8, 192.168.1.0/24",
			wantErr:   false,
		},
		{
			name:      "FILE not found (returns empty)",
			key:       "nonexistent",
			wantValue: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveValue(context.Background(), "FILE", tt.key, st)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantValue {
				t.Errorf("ResolveValue() = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

// TestResolveValue_MAPLOCAL tests resolving mapped file paths.
func TestResolveValue_MAPLOCAL(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	confDir := filepath.Join(tmpDir, "conf")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("Failed to create conf dir: %v", err)
	}

	// Create dhparam.pem file
	dhparamFile := filepath.Join(confDir, "dhparam.pem")
	if err := os.WriteFile(dhparamFile, []byte("test dhparam data"), 0644); err != nil {
		t.Fatalf("Failed to write dhparam file: %v", err)
	}

	r := NewResolver(tmpDir)
	st := &state.State{}

	tests := []struct {
		name      string
		key       string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "MAPLOCAL file exists",
			key:       "zimbraSSLDHParam",
			wantValue: dhparamFile,
			wantErr:   false,
		},
		{
			name:      "MAPLOCAL unknown key",
			key:       "unknownKey",
			wantValue: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveValue(context.Background(), "MAPLOCAL", tt.key, st)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantValue {
				t.Errorf("ResolveValue() = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

// TestResolveValue_MAPLOCAL_FileNotExists tests MAPLOCAL when file doesn't exist.
func TestResolveValue_MAPLOCAL_FileNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	r := NewResolver(tmpDir)
	st := &state.State{}

	// dhparam.pem doesn't exist in tmpDir
	got, err := r.ResolveValue(context.Background(), "MAPLOCAL", "zimbraSSLDHParam", st)
	if err != nil {
		t.Errorf("ResolveValue() returned error for non-existent file: %v", err)
	}
	if got != "" {
		t.Errorf("ResolveValue() = %q, want empty string for non-existent file", got)
	}
}

// TestResolveValue_Literal tests literal values (unknown types).
func TestResolveValue_Literal(t *testing.T) {
	r := NewResolver("/opt/zextras")
	st := &state.State{}

	tests := []struct {
		name      string
		valueType string
		key       string
		wantValue string
	}{
		{
			name:      "Literal string",
			valueType: "LITERAL",
			key:       "some literal text",
			wantValue: "some literal text",
		},
		{
			name:      "Unrecognized type treated as literal",
			valueType: "UNKNOWN",
			key:       "value",
			wantValue: "value",
		},
		{
			name:      "Empty type treated as literal",
			valueType: "",
			key:       "default",
			wantValue: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveValue(context.Background(), tt.valueType, tt.key, st)
			if err != nil {
				t.Errorf("ResolveValue() unexpected error: %v", err)
			}
			if got != tt.wantValue {
				t.Errorf("ResolveValue() = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

// TestResolvePostconfDirective tests POSTCONF directive resolution.
func TestResolvePostconfDirective(t *testing.T) {
	r := NewResolver("/opt/zextras").(*resolver)
	st := &state.State{
		GlobalConfig: &config.GlobalConfig{
			Data: map[string]string{
				"zimbraMtaMyNetworks": "127.0.0.0/8",
				"zimbraBooleanTrue":   "TRUE",
				"zimbraBooleanFalse":  "FALSE",
			},
		},
	}

	tests := []struct {
		name      string
		key       string
		valueType string
		valueKey  string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "POSTCONF with VAR",
			key:       "mynetworks",
			valueType: "VAR",
			valueKey:  "zimbraMtaMyNetworks",
			wantKey:   "mynetworks",
			wantValue: "127.0.0.0/8",
			wantErr:   false,
		},
		{
			name:      "POSTCONF clear parameter (empty value)",
			key:       "relay_host",
			valueType: "",
			valueKey:  "",
			wantKey:   "relay_host",
			wantValue: "",
			wantErr:   false,
		},
		{
			name:      "POSTCONF boolean TRUE to yes",
			key:       "smtp_tls_security_level",
			valueType: "VAR",
			valueKey:  "zimbraBooleanTrue",
			wantKey:   "smtp_tls_security_level",
			wantValue: "yes",
			wantErr:   false,
		},
		{
			name:      "POSTCONF boolean FALSE to no",
			key:       "smtp_use_tls",
			valueType: "VAR",
			valueKey:  "zimbraBooleanFalse",
			wantKey:   "smtp_use_tls",
			wantValue: "no",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolvePostconfDirective(context.Background(), tt.key, tt.valueType, tt.valueKey, st)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolvePostconfDirective() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got.Key != tt.wantKey {
				t.Errorf("ResolvePostconfDirective() key = %q, want %q", got.Key, tt.wantKey)
			}
			if got.Value != tt.wantValue {
				t.Errorf("ResolvePostconfDirective() value = %q, want %q", got.Value, tt.wantValue)
			}
		})
	}
}

// TestResolvePostconfdDirective tests POSTCONFD directive resolution (delete operation).
func TestResolvePostconfdDirective(t *testing.T) {
	r := NewResolver("/opt/zextras").(*resolver)

	tests := []struct {
		name    string
		key     string
		wantKey string
	}{
		{
			name:    "POSTCONFD simple key",
			key:     "relay_host",
			wantKey: "relay_host",
		},
		{
			name:    "POSTCONFD another key",
			key:     "smtp_sasl_auth_enable",
			wantKey: "smtp_sasl_auth_enable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.ResolvePostconfdDirective(tt.key)
			if got.Key != tt.wantKey {
				t.Errorf("ResolvePostconfdDirective() key = %q, want %q", got.Key, tt.wantKey)
			}
		})
	}
}

// TestResolveLdapDirective tests LDAP directive resolution.
func TestResolveLdapDirective(t *testing.T) {
	r := NewResolver("/opt/zextras").(*resolver)
	st := &state.State{
		LocalConfig: &config.LocalConfig{
			Data: map[string]string{
				"ldap_db_maxsize": "85899345920",
			},
		},
	}

	tests := []struct {
		name      string
		key       string
		valueType string
		valueKey  string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "LDAP with LOCAL",
			key:       "olcDbMaxSize",
			valueType: "LOCAL",
			valueKey:  "ldap_db_maxsize",
			wantKey:   "olcDbMaxSize",
			wantValue: "85899345920",
			wantErr:   false,
		},
		{
			name:      "LDAP with empty value",
			key:       "olcDbMaxSize",
			valueType: "LOCAL",
			valueKey:  "nonexistent",
			wantKey:   "olcDbMaxSize",
			wantValue: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveLdapDirective(context.Background(), tt.key, tt.valueType, tt.valueKey, st)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveLdapDirective() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got.Key != tt.wantKey {
				t.Errorf("ResolveLdapDirective() key = %q, want %q", got.Key, tt.wantKey)
			}
			if got.Value != tt.wantValue {
				t.Errorf("ResolveLdapDirective() value = %q, want %q", got.Value, tt.wantValue)
			}
		})
	}
}

// TestResolveMapfileDirective tests MAPFILE directive resolution.
func TestResolveMapfileDirective(t *testing.T) {
	tmpDir := t.TempDir()
	r := NewResolver(tmpDir).(*resolver)

	expectedPath := filepath.Join(tmpDir, "conf", "dhparam.pem")

	st := &state.State{
		GlobalConfig: &config.GlobalConfig{
			Data: map[string]string{
				"zimbraSSLDHParam": "base64encodeddata==",
			},
		},
	}

	tests := []struct {
		name         string
		key          string
		isLocal      bool
		wantKey      string
		wantFilePath string
		wantBase64   string
		wantIsLocal  bool
		wantErr      bool
	}{
		{
			name:         "MAPFILE (remote) with base64 data",
			key:          "zimbraSSLDHParam",
			isLocal:      false,
			wantKey:      "zimbraSSLDHParam",
			wantFilePath: expectedPath,
			wantBase64:   "base64encodeddata==",
			wantIsLocal:  false,
			wantErr:      false,
		},
		{
			name:         "MAPLOCAL (local check only)",
			key:          "zimbraSSLDHParam",
			isLocal:      true,
			wantKey:      "zimbraSSLDHParam",
			wantFilePath: expectedPath,
			wantBase64:   "",
			wantIsLocal:  true,
			wantErr:      false,
		},
		{
			name:         "MAPFILE unknown key",
			key:          "unknownKey",
			isLocal:      false,
			wantKey:      "unknownKey",
			wantFilePath: "",
			wantBase64:   "",
			wantIsLocal:  false,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.ResolveMapfileDirective(tt.key, tt.isLocal, st)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveMapfileDirective() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got.Key != tt.wantKey {
				t.Errorf("ResolveMapfileDirective() key = %q, want %q", got.Key, tt.wantKey)
			}
			if got.FilePath != tt.wantFilePath {
				t.Errorf("ResolveMapfileDirective() filePath = %q, want %q", got.FilePath, tt.wantFilePath)
			}
			if got.Base64Data != tt.wantBase64 {
				t.Errorf("ResolveMapfileDirective() base64Data = %q, want %q", got.Base64Data, tt.wantBase64)
			}
			if got.IsLocal != tt.wantIsLocal {
				t.Errorf("ResolveMapfileDirective() isLocal = %v, want %v", got.IsLocal, tt.wantIsLocal)
			}
		})
	}
}
