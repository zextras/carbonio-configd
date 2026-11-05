// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
	"github.com/zextras/carbonio-configd/internal/testutil/pki"
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

// --- startDirect (ctl.loadConfig override) ---

func TestLdapCustomStart_LoadConfigFails(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	ctl.loadConfig = func() (map[string]string, error) {
		return nil, fmt.Errorf("test config error")
	}

	ctl.registry = ctl.defaultRegistry()

	err := ctl.startDirect(context.Background(), svcLdap, ctl.registry[svcLdap])
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
	writeExecutable(t, slapdBin, []byte("#!/bin/sh\nsleep 5\n"))

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
	// Stub the probe to succeed so startDirect returns once the fake
	// slapd is launched.
	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error { return nil })

	done := make(chan error, 1)
	ctl.registry = ctl.defaultRegistry()

	go func() {
		done <- ctl.startDirect(context.Background(), svcLdap, ctl.registry[svcLdap])
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("startDirect returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startDirect")
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
	writeExecutable(t, slapdBin, []byte("#!/bin/sh\nsleep 60\n"))

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

	ctl.registry = ctl.defaultRegistry()

	err := ctl.startDirect(ctx, svcLdap, ctl.registry[svcLdap])
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

// --- readiness probe of the detached slapd launch ---

// startSlapd launches a stand-in slapd the way the registry's ldap entry does:
// detached, then held until the LDAP readiness probe passes.
func startSlapd(ctx context.Context, ctl *Controller) error {
	def := &ServiceDef{Name: "ldap", BinaryPath: "slapd", LogFile: "/slapd.out", Ready: ReadyLDAP}

	return ctl.startDetached(ctx, "ldap", def, nil)
}

func withLdapProbe(t *testing.T, ctl *Controller, fn func(context.Context, []string) error) {
	t.Helper()

	ctl.ldapProbe = fn
}

// TestStartSlapd_ReadyOnProbe verifies a fast return once the probe
// succeeds, without waiting for sd_notify or the full timeout.
func TestStartSlapd_ReadyOnProbe(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error { return nil })

	err := startSlapd(context.Background(), ctl)
	if err != nil {
		t.Fatalf("startSlapd returned error: %v", err)
	}

	if got := f.Rec.Of("exec.start"); len(got) != 1 {
		t.Errorf("expected slapd to be started exactly once, got %q", got)
	}
}

// TestStartSlapd_ChildExitFailsFast verifies that if the child exits before
// the probe ever succeeds, we fail immediately rather than polling to timeout.
func TestStartSlapd_ChildExitFailsFast(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)

	// Probe never succeeds.
	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error {
		return fmt.Errorf("not ready")
	})

	// Child exits immediately with a failure.
	f.Exec.OnPath("slapd", systest.Fail(1, ""))

	err := startSlapd(context.Background(), ctl)
	if err == nil {
		t.Fatal("expected error when child exits before readiness")
	}
	if !strings.Contains(err.Error(), "exited during startup") {
		t.Errorf("error = %v, want 'exited during startup'", err)
	}
}

// TestStartSlapd_CtxCancel verifies ctx cancellation aborts the wait.
func TestStartSlapd_CtxCancel(t *testing.T) {
	t.Parallel()

	ctl, _ := newFakeController(t)
	ctl.withBlockingStart()

	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error {
		return fmt.Errorf("not ready")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := startSlapd(ctx, ctl)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// TestStartSlapd_Timeout verifies the readiness wait gives up once
// ldapReadyTimeout of (manual) clock time has elapsed without a successful probe.
func TestStartSlapd_Timeout(t *testing.T) {
	t.Parallel()

	ctl, f := newFakeController(t)
	ctl.withBlockingStart()

	withLdapProbe(t, ctl, func(_ context.Context, _ []string) error {
		return fmt.Errorf("not ready")
	})

	pumpClock(t, f.Clock, ldapProbeInterval)

	err := startSlapd(context.Background(), ctl)
	if err == nil || !strings.Contains(err.Error(), "did not become ready within") {
		t.Fatalf("error = %v, want readiness timeout", err)
	}
}

// TestProbeLDAPReady_EmptyURLs verifies that an empty URL list causes an error.
func TestProbeLDAPReady_EmptyURLs(t *testing.T) {
	t.Parallel()

	err := probeLDAPReady(context.Background(), []string{}, nil)
	if err == nil {
		t.Error("expected non-nil error for empty URL list")
	}
}

// TestProbeLDAPReady_UnreachableURL verifies that an unreachable host returns
// a non-nil error.
func TestProbeLDAPReady_UnreachableURL(t *testing.T) {
	t.Parallel()

	err := probeLDAPReady(context.Background(), []string{"ldap://127.0.0.1:1"}, nil)
	if err == nil {
		t.Error("expected non-nil error for unreachable LDAP URL")
	}
}

// --- readiness probe TLS (S12) ---

// ldapsHost starts an in-process ldaps:// server with a certificate issued by
// a private CA and returns the probe URL plus that CA's PEM.
func ldapsHost(t *testing.T) (url string, caPEM []byte) {
	t.Helper()

	pk := pki.NewServer(t, "localhost", "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{
		TLS:         &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
		ImplicitTLS: true,
	})

	return srv.URL("ldaps", "localhost"), pk.CAPEM
}

// TestProbeLDAP_DefaultAcceptsUntrustedCertificate: with ssl_allow_untrusted_certs
// unset (legacy default: true) a healthy slapd behind a certificate no CA
// vouches for is still ready.
func TestProbeLDAP_DefaultAcceptsUntrustedCertificate(t *testing.T) {
	t.Parallel()

	url, _ := ldapsHost(t)
	ctl := newRealController(t)

	if err := ctl.probeLDAP(context.Background(), []string{url}); err != nil {
		t.Fatalf("probe of a healthy slapd with an untrusted certificate: %v", err)
	}
}

// TestProbeLDAP_VerifiesWhenUntrustedCertsAreNotAllowed: with
// ssl_allow_untrusted_certs=false the probe verifies the certificate against
// the Carbonio CA, so an unknown CA fails the probe and the CA under
// conf/ca/ca.pem makes it pass.
func TestProbeLDAP_VerifiesWhenUntrustedCertsAreNotAllowed(t *testing.T) {
	t.Parallel()

	url, caPEM := ldapsHost(t)
	home := t.TempDir()

	ctl := newRealController(t, WithConfigLoader(func() (map[string]string, error) {
		return map[string]string{"ssl_allow_untrusted_certs": "false", "zimbra_home": home}, nil
	}))

	if err := ctl.probeLDAP(context.Background(), []string{url}); err == nil {
		t.Fatal("a certificate of an unknown CA must fail a verifying probe")
	}

	require.NoError(t, os.MkdirAll(filepath.Join(home, "conf", "ca"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "conf", "ca", "ca.pem"), caPEM, 0o644))

	if err := ctl.probeLDAP(context.Background(), []string{url}); err != nil {
		t.Fatalf("a certificate of the Carbonio CA must pass: %v", err)
	}
}
