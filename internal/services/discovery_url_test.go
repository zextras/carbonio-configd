// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
