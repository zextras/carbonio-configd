// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// --- buildLDAPBindURL (table-driven consolidation) ---

func TestBuildLDAPBindURL_TableDriven(t *testing.T) {
	t.Parallel()

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

// --- ldapCustomStart (ctl.loadConfig override) ---

func TestLdapCustomStart_LoadConfigFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}

	err := ctl.ldapCustomStart(context.Background(), &ServiceDef{Name: "ldap"})
	if err == nil {
		t.Fatal("expected error when ctl.loadConfig fails")
	}
	if !strings.Contains(err.Error(), "failed to load localconfig") {
		t.Errorf("error = %q, want it to contain %q", err, "failed to load localconfig")
	}
}

func TestLdapCustomStart_WithFakeSlapd(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

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

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{
			"ldap_port":              "389",
			"zimbra_server_hostname": "localhost",
		}, nil
	}

	ctl.p.common = tmp

	ctl.p.data = tmp

	ctl.p.log = logDir

	// slapd has no sd_notify; readiness is determined by the LDAP probe.
	// Stub the probe to succeed so ldapCustomStart returns once the fake
	// slapd is launched.
	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error { return nil })

	done := make(chan error, 1)
	go func() {
		done <- ctl.ldapCustomStart(context.Background(), &ServiceDef{Name: "ldap"})
	}()

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
	t.Parallel()

	ctl := newRealController(t)

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

	ctl.loadConfig = func() (map[string]string, error) {
		return map[string]string{}, nil
	}

	ctl.p.common = tmp

	ctl.p.data = tmp

	ctl.p.log = logDir

	// Probe never succeeds, so readiness depends solely on ctx/timeout.
	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error {
		return fmt.Errorf("not ready")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ctl.ldapCustomStart(ctx, &ServiceDef{Name: "ldap"})
	if err == nil {
		t.Fatal("expected error on context timeout")
	}
}

// --- renderLDAPTable ---

func TestRenderLDAPTable(t *testing.T) {
	t.Parallel()

	got := renderLDAPTable("ldap://test", "389", startTLSYes, "secret", "query_filter = (cn=%s)\nresult_attribute = cn\n", "extra = line\n")

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
	t.Parallel()

	got := renderLDAPTable("ldap://test", "389", "no", "pw", "query_filter = (cn=%s)\n", "")

	if !strings.Contains(got, "start_tls = no") {
		t.Error("expected start_tls = no")
	}

	if strings.HasSuffix(got, "\n\n") {
		t.Error("unexpected trailing newline from empty extra")
	}
}

func TestRenderLDAPTable_AllFields(t *testing.T) {
	t.Parallel()

	got := renderLDAPTable("ldap://host", "636", startTLSYes, "secret123",
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

// --- startThenProbe ---

func withLdapProbe(t *testing.T, ctl *Controller, fn func(context.Context, []string) error) {
	t.Helper()

	ctl.ldapProbe = fn
}

// TestStartThenProbe_ReadyOnProbe verifies a fast return once the probe
// succeeds, without waiting for sd_notify or the full timeout.
func TestStartThenProbe_ReadyOnProbe(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error { return nil })

	err := ctl.startThenProbe(context.Background(), &sys.Cmd{Path: "slapd"}, "ldap://localhost:389")
	if err != nil {
		t.Fatalf("startThenProbe returned error: %v", err)
	}

	if got := f.Rec.Of("exec.start"); len(got) != 1 {
		t.Errorf("expected slapd to be started exactly once, got %q", got)
	}
}

// TestStartThenProbe_ChildExitFailsFast verifies that if the child exits before
// the probe ever succeeds, we fail immediately rather than polling to timeout.
func TestStartThenProbe_ChildExitFailsFast(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	// Probe never succeeds.
	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error {
		return fmt.Errorf("not ready")
	})

	// Child exits immediately with a failure.
	f.Exec.OnPath("slapd", systest.Fail(1, ""))

	err := ctl.startThenProbe(context.Background(), &sys.Cmd{Path: "slapd"}, "ldap://localhost:389")
	if err == nil {
		t.Fatal("expected error when child exits before readiness")
	}
	if !strings.Contains(err.Error(), "exited during startup") {
		t.Errorf("error = %v, want 'exited during startup'", err)
	}
}

// TestStartThenProbe_CtxCancel verifies ctx cancellation aborts the wait.
func TestStartThenProbe_CtxCancel(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	ctl.withBlockingStart()

	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error {
		return fmt.Errorf("not ready")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ctl.startThenProbe(ctx, &sys.Cmd{Path: "slapd"}, "ldap://localhost:389")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// TestStartThenProbe_Timeout verifies the readiness wait gives up once
// ldapReadyTimeout of (manual) clock time has elapsed without a successful probe.
func TestStartThenProbe_Timeout(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctl.withBlockingStart()

	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error {
		return fmt.Errorf("not ready")
	})

	pumpClock(t, f.Clock, ldapProbeInterval)

	err := ctl.startThenProbe(context.Background(), &sys.Cmd{Path: "slapd"}, "ldap://localhost:389")
	if err == nil || !strings.Contains(err.Error(), "did not become ready within") {
		t.Fatalf("error = %v, want readiness timeout", err)
	}
}

// TestProbeLDAPReady_EmptyURLs verifies that an empty URL list causes an error.
func TestProbeLDAPReady_EmptyURLs(t *testing.T) {
	t.Parallel()

	err := probeLDAPReady(context.Background(), []string{})
	if err == nil {
		t.Error("expected non-nil error for empty URL list")
	}
}

// TestProbeLDAPReady_UnreachableURL verifies that an unreachable host returns
// a non-nil error.
func TestProbeLDAPReady_UnreachableURL(t *testing.T) {
	t.Parallel()

	err := probeLDAPReady(context.Background(), []string{"ldap://127.0.0.1:1"})
	if err == nil {
		t.Error("expected non-nil error for unreachable LDAP URL")
	}
}
