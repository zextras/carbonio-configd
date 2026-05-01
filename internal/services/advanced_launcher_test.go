// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/zxadmin"
)

const goodAuthResponse = `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <AuthResponse xmlns="urn:zimbraAdmin" lifetime="43200000">
      <authToken>fake-token-xyz</authToken>
    </AuthResponse>
  </soap:Body>
</soap:Envelope>`

const hookSoapPath = "/service/admin/soap"
const hookStatusPath = "/service/extension/zextras"

func TestAdvancedRunning_True(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hookSoapPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=UTF-8")
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(hookStatusPath, func(w http.ResponseWriter, _ *http.Request) {
		statusResp := `{"ZxCore":{"running":true,"command":"ZxCore","commercial":"Core"}}`
		_, _ = w.Write([]byte(statusResp))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := zxadmin.NewWithBaseURL(srv.URL, map[string]string{
		"zimbra_ldap_user":     "zimbra",
		"zimbra_ldap_password": "x",
	})

	result := advancedRunning(context.Background(), client)
	if !result {
		t.Errorf("advancedRunning returned false, expected true")
	}
}

func TestAdvancedRunning_False(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hookSoapPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=UTF-8")
		_, _ = w.Write([]byte(goodAuthResponse))
	})
	mux.HandleFunc(hookStatusPath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := zxadmin.NewWithBaseURL(srv.URL, map[string]string{
		"zimbra_ldap_user":     "zimbra",
		"zimbra_ldap_password": "x",
	})

	result := advancedRunning(context.Background(), client)
	if result {
		t.Errorf("advancedRunning returned true, expected false")
	}
}

func TestAdvancedRunning_AuthFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(hookSoapPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "text/xml; charset=UTF-8")
		faultResp := `<?xml version="1.0"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Body>
    <soap:Fault>
      <faultcode>soap:Client</faultcode>
      <faultstring>authentication failed</faultstring>
    </soap:Fault>
  </soap:Body>
</soap:Envelope>`
		_, _ = w.Write([]byte(faultResp))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := zxadmin.NewWithBaseURL(srv.URL, map[string]string{
		"zimbra_ldap_user":     "zimbra",
		"zimbra_ldap_password": "x",
	})

	result := advancedRunning(context.Background(), client)
	if result {
		t.Errorf("advancedRunning returned true, expected false on auth failure")
	}
}

func TestMailboxAdvancedStatusHook_LocalConfigMissing(t *testing.T) {
	origDir := advancedJARDir
	t.Cleanup(func() { advancedJARDir = origDir })

	tmpDir := t.TempDir()
	advancedJARDir = tmpDir

	jarPath := filepath.Join(tmpDir, "carbonio-advanced-1.0.jar")
	if err := os.WriteFile(jarPath, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("failed to create dummy JAR: %v", err)
	}

	err := MailboxAdvancedStatusHook(context.Background(), &ServiceManager{})
	if err != nil {
		t.Errorf("MailboxAdvancedStatusHook returned error: %v", err)
	}
}

// --- advancedInstalled ---

func TestAdvancedInstalled(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) string // returns dir path
		want  bool
	}{
		{
			name: "no directory exists",
			setup: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			want: false,
		},
		{
			name: "empty directory",
			setup: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			want: false,
		},
		{
			name: "directory with unrelated jar",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "other-lib.jar"), []byte("fake"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: false,
		},
		{
			name: "directory with matching jar",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-1.0.jar"), []byte("fake"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: true,
		},
		{
			name: "file with matching prefix but wrong extension",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-config.xml"), []byte("fake"), 0o644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			want: false,
		},
		{
			name: "multiple jars with one matching",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				for _, name := range []string{"util.jar", "carbonio-advanced-2.5.jar", "readme.txt"} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return dir
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)

			old := advancedJARDir
			advancedJARDir = dir
			defer func() { advancedJARDir = old }()

			if got := advancedInstalled(); got != tt.want {
				t.Errorf("advancedInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- MailboxAdvancedStatusHook ---

func TestMailboxAdvancedStatusHook_NotInstalled(t *testing.T) {
	// When no advanced jars exist, the hook should return nil immediately.
	old := advancedJARDir
	advancedJARDir = filepath.Join(t.TempDir(), "nonexistent")
	defer func() { advancedJARDir = old }()

	err := MailboxAdvancedStatusHook(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil when advanced not installed, got: %v", err)
	}
}

func TestMailboxAdvancedStatusHook_ContextCancelled(t *testing.T) {
	// When context is cancelled, hook should return nil without hanging.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "carbonio-advanced-1.0.jar"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldJAR := advancedJARDir
	advancedJARDir = dir
	defer func() { advancedJARDir = oldJAR }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := MailboxAdvancedStatusHook(ctx, nil)
	if err != nil {
		t.Errorf("expected nil on cancelled context, got: %v", err)
	}
}
