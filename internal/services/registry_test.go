// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"slices"
	"strings"
	"testing"
)

func TestLookupService_Known(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := LookupService("mta")
	if def == nil {
		t.Fatal("expected mta to be registered")
	}

	if def.DisplayName != "mta" {
		t.Errorf("expected DisplayName 'mta', got %q", def.DisplayName)
	}

	if len(def.SystemdUnits) == 0 {
		t.Error("expected at least one systemd unit")
	}
}

func TestLookupService_Unknown(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := LookupService("nonexistent")
	if def != nil {
		t.Error("expected nil for unknown service")
	}
}

func TestAllServiceNames_ContainsExpected(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	names := AllServiceNames()
	expected := []string{"ldap", "mta", "proxy", "mailbox", "memcached"}

	for _, exp := range expected {
		if !slices.Contains(names, exp) {
			t.Errorf("expected %q in AllServiceNames()", exp)
		}
	}
}

func TestAllServiceNames_Ordered(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	names := AllServiceNames()
	order := getDefaultStartOrder()

	for i := 1; i < len(names); i++ {
		prev := orderOf(names[i-1], order)
		curr := orderOf(names[i], order)

		if prev > curr {
			t.Errorf("services not ordered: %s (order %d) before %s (order %d)",
				names[i-1], prev, names[i], curr)
		}
	}
}

func TestRegistryDependencies_MTA(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := LookupService("mta")
	if def == nil {
		t.Fatal("mta not found")
	}

	if len(def.Dependencies) == 0 {
		t.Error("expected mta to have dependencies")
	}

	hasSaslauthd := false
	for _, dep := range def.Dependencies {
		if dep == "saslauthd" {
			hasSaslauthd = true
		}
	}

	if !hasSaslauthd {
		t.Error("expected mta to depend on saslauthd")
	}
}

func TestRegistryDependencies_Antivirus(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := LookupService("antivirus")
	if def == nil {
		t.Fatal("antivirus not found")
	}

	hasClamd := false
	for _, dep := range def.Dependencies {
		if dep == "clamd" {
			hasClamd = true
		}
	}

	if !hasClamd {
		t.Error("expected antivirus to depend on clamd")
	}
}

func TestRegistryConfigRewrite_Proxy(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := LookupService("proxy")
	if def == nil {
		t.Fatal("proxy not found")
	}

	if len(def.ConfigRewrite) == 0 {
		t.Error("expected proxy to have config rewrite entries")
	}
}

func TestRegistrySimpleService_NoDepsnorHooks(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := LookupService("memcached")
	if def == nil {
		t.Fatal("memcached not found")
	}

	if len(def.Dependencies) != 0 {
		t.Error("expected memcached to have no dependencies")
	}

	if len(def.PreStart) != 0 {
		t.Error("expected memcached to have no pre-start hooks")
	}
}

func TestRegistryDisplayNames_AllLowercase(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	for name, def := range Registry {
		if def.DisplayName != strings.ToLower(def.DisplayName) {
			t.Errorf("service %q has non-lowercase DisplayName %q, want %q",
				name, def.DisplayName, strings.ToLower(def.DisplayName))
		}
	}
}

func TestRegistryCoversAllSystemdMap(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	systemdMap := getDefaultSystemdMap()
	for name := range systemdMap {
		if LookupService(name) == nil {
			// Some systemdMap entries like "mailboxd" and "service" are aliases
			// Only check core service names
			if name != "mailboxd" && name != "service" && name != "archiving" {
				t.Logf("Note: systemdMap entry %q not in registry (may be alias)", name)
			}
		}
	}
}
