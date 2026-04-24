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

	hasFreshclam := false
	for _, dep := range def.Dependencies {
		if dep == "freshclam" {
			hasFreshclam = true
		}
		if dep == "clamd" {
			t.Error("antivirus must not depend on clamd; carbonio-antivirus.service IS clamd")
		}
	}

	if !hasFreshclam {
		t.Error("expected antivirus to depend on freshclam")
	}

	// Verify ConfigRewrite contains "antivirus"
	hasAntivirusConfig := false
	for _, cfg := range def.ConfigRewrite {
		if cfg == "antivirus" {
			hasAntivirusConfig = true
			break
		}
	}
	if !hasAntivirusConfig {
		t.Error("expected antivirus ConfigRewrite to contain 'antivirus'")
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
			if name != "mailboxd" && name != "service" {
				t.Logf("Note: systemdMap entry %q not in registry (may be alias)", name)
			}
		}
	}
}

// TestRegistryAntivirusLegacyFields verifies the antivirus service definition
// has all the expected fields for clamd integration.
func TestRegistryAntivirusLegacyFields(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	def := LookupService("antivirus")
	if def == nil {
		t.Fatal("antivirus not found")
	}

	// BinaryPath should be commonPath + "/sbin/clamd"
	expectedBinaryPath := commonPath + "/sbin/clamd"
	if def.BinaryPath != expectedBinaryPath {
		t.Errorf("BinaryPath = %q, want %q", def.BinaryPath, expectedBinaryPath)
	}

	// BinaryArgs should have exactly one entry: "--config-file=<confPath>/clamd.conf"
	if len(def.BinaryArgs) != 1 {
		t.Errorf("len(BinaryArgs) = %d, want 1", len(def.BinaryArgs))
	}
	expectedArg := "--config-file=" + confPath + "/clamd.conf"
	if len(def.BinaryArgs) > 0 && def.BinaryArgs[0] != expectedArg {
		t.Errorf("BinaryArgs[0] = %q, want %q", def.BinaryArgs[0], expectedArg)
	}

	// Detached should be true
	if !def.Detached {
		t.Error("Detached = false, want true")
	}

	// PidFile should be pidDir + "/clamd.pid"
	expectedPidFile := pidDir + "/clamd.pid"
	if def.PidFile != expectedPidFile {
		t.Errorf("PidFile = %q, want %q", def.PidFile, expectedPidFile)
	}

	// UseSDNotify should be true
	if !def.UseSDNotify {
		t.Error("UseSDNotify = false, want true")
	}

	// ConfigRewrite should contain "antivirus"
	hasAntivirusConfig := false
	for _, cfg := range def.ConfigRewrite {
		if cfg == "antivirus" {
			hasAntivirusConfig = true
			break
		}
	}
	if !hasAntivirusConfig {
		t.Error("ConfigRewrite does not contain 'antivirus'")
	}

	// PreStart should have exactly one hook (clamdDirInit)
	if len(def.PreStart) != 1 {
		t.Errorf("len(PreStart) = %d, want 1", len(def.PreStart))
	}
}

// TestLookupService_ClamdAlias verifies that the "clamd" alias
// resolves to the antivirus service definition.
func TestLookupService_ClamdAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}
	clamdDef := LookupService("clamd")
	if clamdDef == nil {
		t.Fatal("expected clamd alias to resolve to a service definition")
	}

	antivirusDef := LookupService("antivirus")
	if antivirusDef == nil {
		t.Fatal("antivirus not found")
	}

	// The alias should return the same pointer as the canonical service
	if clamdDef != antivirusDef {
		t.Error("clamd alias does not resolve to the same antivirus definition")
	}
}

// TestServiceAliasesNotInRegistry verifies that alias names
// do not appear in the Registry or AllServiceNames.
func TestServiceAliasesNotInRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	// "clamd" should not be a direct Registry key
	if _, ok := Registry["clamd"]; ok {
		t.Error("clamd should not be a direct Registry entry (it's an alias)")
	}

	// "clamd" should not appear in AllServiceNames
	names := AllServiceNames()
	for _, name := range names {
		if name == "clamd" {
			t.Error("clamd should not appear in AllServiceNames (it's an alias)")
		}
	}
}

// TestServiceStartClamdAlias verifies that the clamd alias
// resolves to the antivirus definition with the correct fields.
// Note: We do NOT call ServiceStart directly as that would attempt to
// launch the actual clamd process. Instead, we verify the alias resolution
// and that the definition has the necessary fields for start code to use.
func TestServiceStartClamdAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: may invoke real system commands")
	}

	// Verify the alias resolves to antivirus
	def := LookupService("clamd")
	if def == nil {
		t.Fatal("clamd alias should resolve to a service definition")
	}

	// Verify it's the antivirus definition
	if def.Name != "antivirus" {
		t.Errorf("clamd alias resolved to Name=%q, want 'antivirus'", def.Name)
	}

	// Verify BinaryPath is set (required for start code)
	if def.BinaryPath == "" {
		t.Error("BinaryPath is empty; start code would fail")
	}

	// Verify it's the same as the antivirus definition
	antivirusDef := LookupService("antivirus")
	if def != antivirusDef {
		t.Error("clamd alias does not resolve to the antivirus definition")
	}
}
