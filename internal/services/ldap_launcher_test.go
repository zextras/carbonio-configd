// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- buildLDAPBindURL (table-driven consolidation) ---

func TestBuildLDAPBindURL_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		lc   map[string]string
		want string
	}{
		{
			name: "explicit bind URL takes precedence",
			lc: map[string]string{
				"ldap_bind_url": "ldap://bind.example.com:389",
				"ldap_url":      "ldap://other:389",
			},
			want: "ldap://bind.example.com:389",
		},
		{
			name: "single ldap_url fallback",
			lc: map[string]string{
				"ldap_url": "ldap://single:389",
			},
			want: "ldap://single:389",
		},
		{
			name: "reconstruct with custom port",
			lc: map[string]string{
				"zimbra_server_hostname": "custom.host",
				"ldap_port":              "636",
			},
			want: "ldap://custom.host:636",
		},
		{
			name: "all defaults",
			lc:   map[string]string{},
			want: "ldap://localhost:389",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildLDAPBindURL(tt.lc); got != tt.want {
				t.Errorf("buildLDAPBindURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- ldapCustomStart (loadConfig override) ---

func TestLdapCustomStart_LoadConfigFails(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	old := loadConfig
	loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}
	defer func() { loadConfig = old }()

	err := ldapCustomStart(context.Background(), &ServiceDef{Name: "ldap"})
	if err == nil {
		t.Fatal("expected error when loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to load localconfig")
	}
}

func TestLdapCustomStart_WithFakeSlapd(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	slapdDir := filepath.Join(tmp, "libexec")
	if err := os.MkdirAll(slapdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slapdBin := filepath.Join(slapdDir, "slapd")
	if err := os.WriteFile(slapdBin, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ldapCfgDir := filepath.Join(tmp, "ldap", "config")
	if err := os.MkdirAll(ldapCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"ldap_port":              "389",
			"zimbra_server_hostname": "localhost",
		}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldCommon := commonPath
	commonPath = tmp
	defer func() { commonPath = oldCommon }()

	oldData := dataPath
	dataPath = tmp
	defer func() { dataPath = oldData }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	socketPath := expectedSocketPath("ldap")

	done := make(chan error, 1)
	go func() {
		done <- ldapCustomStart(context.Background(), &ServiceDef{Name: "ldap"})
	}()

	sendReady(t, socketPath)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ldapCustomStart returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ldapCustomStart")
	}
}

func TestLdapCustomStart_ContextTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	tmp := t.TempDir()

	logDir := filepath.Join(tmp, "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	slapdDir := filepath.Join(tmp, "libexec")
	if err := os.MkdirAll(slapdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slapdBin := filepath.Join(slapdDir, "slapd")
	if err := os.WriteFile(slapdBin, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ldapCfgDir := filepath.Join(tmp, "ldap", "config")
	if err := os.MkdirAll(ldapCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldLC := loadConfig
	loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}
	defer func() { loadConfig = oldLC }()

	oldCommon := commonPath
	commonPath = tmp
	defer func() { commonPath = oldCommon }()

	oldData := dataPath
	dataPath = tmp
	defer func() { dataPath = oldData }()

	oldLog := logPath
	logPath = logDir
	defer func() { logPath = oldLog }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ldapCustomStart(ctx, &ServiceDef{Name: "ldap"})
	if err == nil {
		t.Fatal("expected error on context timeout")
	}
}

// --- renderLDAPTable ---

func TestRenderLDAPTable(t *testing.T) {
	got := renderLDAPTable("ldap://test", "389", "yes", "secret", "query_filter = (cn=%s)\nresult_attribute = cn\n", "extra = line\n")

	want := `server_host = ldap://test
server_port = 389
search_base =
query_filter = (cn=%s)
result_attribute = cn
version = 3
start_tls = yes
tls_ca_cert_dir = /opt/zextras/conf/ca
bind = yes
bind_dn = uid=zmpostfix,cn=appaccts,cn=zimbra
bind_pw = secret
timeout = 30
extra = line
`

	if got != want {
		t.Errorf("renderLDAPTable mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderLDAPTable_NoExtra(t *testing.T) {
	got := renderLDAPTable("ldap://test", "389", "no", "pw", "query_filter = (cn=%s)\n", "")

	if !strings.Contains(got, "start_tls = no") {
		t.Error("expected start_tls = no")
	}

	if strings.HasSuffix(got, "\n\n") {
		t.Error("unexpected trailing newline from empty extra")
	}
}

func TestRenderLDAPTable_AllFields(t *testing.T) {
	got := renderLDAPTable("ldap://host", "636", "yes", "secret123",
		"query_filter = (uid=%s)\nresult_attribute = uid\n",
		"special_result_attribute = member\n")

	if !strings.Contains(got, "server_host = ldap://host") {
		t.Error("missing server_host")
	}
	if !strings.Contains(got, "server_port = 636") {
		t.Error("missing server_port")
	}
	if !strings.Contains(got, "start_tls = yes") {
		t.Error("missing start_tls")
	}
	if !strings.Contains(got, "bind_pw = secret123") {
		t.Error("missing bind_pw")
	}
	if !strings.Contains(got, "special_result_attribute = member") {
		t.Error("missing special_result_attribute")
	}
}
