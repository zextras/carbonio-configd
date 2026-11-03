// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/zextras/carbonio-configd/internal/testutil/ldapfake"
	"github.com/zextras/carbonio-configd/internal/testutil/pki"
)

func withLoadConfig(t *testing.T, ctl *Controller, fn func() (map[string]string, error)) {
	t.Helper()

	ctl.loadConfig = fn
}

func withCacheFile(t *testing.T, ctl *Controller, contents string) {
	t.Helper()

	dir := t.TempDir()

	ctl.p.cacheFile = filepath.Join(dir, "zmcontrol.cache")

	if contents != "" {
		if err := os.WriteFile(ctl.p.cacheFile, []byte(contents), 0o600); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
	}
}

func TestDiscoverEnabledServices_MissingHostname(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	withLoadConfig(t, ctl, func() (map[string]string, error) {
		return map[string]string{
			"ldap_master_url": "ldap://srv1:389",
		}, nil
	})

	_, err := ctl.DiscoverEnabledServices(context.Background())
	if err == nil {
		t.Fatal("expected error when zimbra_server_hostname is missing")
	}
}

func TestDiscoverEnabledServices_EmptyMasterURL_FallsBackToCache(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	withLoadConfig(t, ctl, func() (map[string]string, error) {
		return map[string]string{
			"zimbra_server_hostname": "host.example.com",
			"ldap_master_url":        "   ",
		}, nil
	})

	withCacheFile(t, ctl, "amavis\nproxy\nmta\nzmconfigd\n")

	got, err := ctl.DiscoverEnabledServices(context.Background())
	if err != nil {
		t.Fatalf("expected cache fallback to succeed, got err=%v", err)
	}

	want := map[string]bool{"amavis": true, "proxy": true, "mta": true, "zmconfigd": true}
	if len(got) != len(want) {
		t.Fatalf("got %v services, want %d", got, len(want))
	}

	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected service %q", s)
		}
	}
}

func TestDiscoverEnabledServices_EmptyMasterURL_NoCache(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	withLoadConfig(t, ctl, func() (map[string]string, error) {
		return map[string]string{
			"zimbra_server_hostname": "host.example.com",
			"ldap_master_url":        "",
		}, nil
	})

	withCacheFile(t, ctl, "")

	_, err := ctl.DiscoverEnabledServices(context.Background())
	if err == nil {
		t.Fatal("expected error when both LDAP and cache are unavailable")
	}
}

// TestDiscoverEnabledServices_StartTLSFollowsLegacyRule verifies discovery
// negotiates StartTLS only under the legacy Java rule (supported=1 AND
// interprocess security), then verifies the server against the Carbonio CA
// when ssl_allow_untrusted_certs=false.
func TestDiscoverEnabledServices_StartTLSFollowsLegacyRule(t *testing.T) {
	t.Parallel()

	pk := pki.NewServer(t, "127.0.0.1")
	srv := ldapfake.New(t, ldapfake.Options{
		TLS: &tls.Config{Certificates: []tls.Certificate{pk.Certificate}, MinVersion: tls.VersionTLS12},
	})

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "conf", "ca"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, "conf", "ca", "ca.pem"), pk.CAPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	lc := map[string]string{
		"zimbra_server_hostname":               "host.example.com",
		"ldap_master_url":                      srv.URL("ldap", "127.0.0.1"),
		"zimbra_ldap_userdn":                   "uid=zimbra,cn=admins,cn=zimbra",
		"zimbra_ldap_password":                 "secret",
		"ldap_starttls_supported":              "1",
		"zimbra_require_interprocess_security": "1",
		"ssl_allow_untrusted_certs":            "false",
		"zimbra_home":                          home,
	}

	ctl := newRealController(t)
	withLoadConfig(t, ctl, func() (map[string]string, error) { return lc, nil })
	// The fake server holds no server entry, so the lookup itself falls back
	// to the cache; what matters here is the transport negotiated before it.
	withCacheFile(t, ctl, "mta\n")

	if _, err := ctl.DiscoverEnabledServices(context.Background()); err != nil {
		t.Fatalf("DiscoverEnabledServices: %v", err)
	}

	if srv.StartTLSOps() != 1 || srv.BindsInTLS() != 1 {
		t.Errorf("startTLS=%d bindsInTLS=%d, want 1/1", srv.StartTLSOps(), srv.BindsInTLS())
	}
}
