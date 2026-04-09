// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systemd

import (
	"strings"
	"testing"
)

// TestBoolToYesNo verifies the boolToYesNo helper returns correct strings.
func TestBoolToYesNo(t *testing.T) {
	if got := boolToYesNo(true); got != "yes" {
		t.Errorf("boolToYesNo(true) = %q, want %q", got, "yes")
	}

	if got := boolToYesNo(false); got != "no" {
		t.Errorf("boolToYesNo(false) = %q, want %q", got, "no")
	}
}

// TestGetStrictProfile verifies the strict security profile structure.
func TestGetStrictProfile(t *testing.T) {
	p := GetStrictProfile()

	if p == nil {
		t.Fatal("GetStrictProfile() returned nil")
	}

	if p.Level != SecurityLevelStrict {
		t.Errorf("Level = %q, want %q", p.Level, SecurityLevelStrict)
	}

	if p.ProtectSystem != "strict" {
		t.Errorf("ProtectSystem = %q, want %q", p.ProtectSystem, "strict")
	}

	if p.ProtectHome != "yes" {
		t.Errorf("ProtectHome = %q, want %q", p.ProtectHome, "yes")
	}

	if !p.PrivateTmp {
		t.Error("PrivateTmp should be true for strict profile")
	}

	if !p.PrivateDevices {
		t.Error("PrivateDevices should be true for strict profile")
	}

	if !p.NoNewPrivileges {
		t.Error("NoNewPrivileges should be true for strict profile")
	}

	if !p.ProtectKernelModules {
		t.Error("ProtectKernelModules should be true for strict profile")
	}

	if !p.ProtectKernelTunables {
		t.Error("ProtectKernelTunables should be true for strict profile")
	}

	if !p.ProtectControlGroups {
		t.Error("ProtectControlGroups should be true for strict profile")
	}

	if !p.MemoryDenyWriteExecute {
		t.Error("MemoryDenyWriteExecute should be true for strict profile")
	}

	if !p.RestrictNamespaces {
		t.Error("RestrictNamespaces should be true for strict profile")
	}

	if !p.RestrictRealtime {
		t.Error("RestrictRealtime should be true for strict profile")
	}

	if len(p.RestrictAddressFamilies) == 0 {
		t.Error("RestrictAddressFamilies should not be empty for strict profile")
	}

	// Strict drops all capabilities (empty slice)
	if len(p.CapabilityBoundingSet) != 0 {
		t.Errorf("CapabilityBoundingSet should be empty for strict profile, got %v", p.CapabilityBoundingSet)
	}

	if len(p.SystemCallFilter) == 0 {
		t.Error("SystemCallFilter should not be empty for strict profile")
	}

	if p.SystemCallErrorNumber != "EPERM" {
		t.Errorf("SystemCallErrorNumber = %q, want %q", p.SystemCallErrorNumber, "EPERM")
	}

	if !p.ProtectHostname {
		t.Error("ProtectHostname should be true for strict profile")
	}

	if !p.ProtectClock {
		t.Error("ProtectClock should be true for strict profile")
	}

	if !p.LockPersonality {
		t.Error("LockPersonality should be true for strict profile")
	}

	if !p.RestrictSUIDSGID {
		t.Error("RestrictSUIDSGID should be true for strict profile")
	}
}

// TestGetStandardProfile verifies the standard security profile structure.
func TestGetStandardProfile(t *testing.T) {
	p := GetStandardProfile()

	if p == nil {
		t.Fatal("GetStandardProfile() returned nil")
	}

	if p.Level != SecurityLevelStandard {
		t.Errorf("Level = %q, want %q", p.Level, SecurityLevelStandard)
	}

	if p.ProtectSystem != "full" {
		t.Errorf("ProtectSystem = %q, want %q", p.ProtectSystem, "full")
	}

	if p.ProtectHome != "yes" {
		t.Errorf("ProtectHome = %q, want %q", p.ProtectHome, "yes")
	}

	if !p.PrivateTmp {
		t.Error("PrivateTmp should be true for standard profile")
	}

	if !p.PrivateDevices {
		t.Error("PrivateDevices should be true for standard profile")
	}

	if !p.NoNewPrivileges {
		t.Error("NoNewPrivileges should be true for standard profile")
	}

	// Standard allows JIT, so MemoryDenyWriteExecute is false
	if p.MemoryDenyWriteExecute {
		t.Error("MemoryDenyWriteExecute should be false for standard profile")
	}

	// Standard allows binding privileged ports
	if len(p.CapabilityBoundingSet) == 0 {
		t.Error("CapabilityBoundingSet should not be empty for standard profile")
	}

	found := false
	for _, cap := range p.CapabilityBoundingSet {
		if cap == "CAP_NET_BIND_SERVICE" {
			found = true
			break
		}
	}

	if !found {
		t.Error("CapabilityBoundingSet should contain CAP_NET_BIND_SERVICE for standard profile")
	}

	// Standard includes AF_NETLINK for network operations
	hasNetlink := false
	for _, af := range p.RestrictAddressFamilies {
		if af == "AF_NETLINK" {
			hasNetlink = true
			break
		}
	}

	if !hasNetlink {
		t.Error("RestrictAddressFamilies should contain AF_NETLINK for standard profile")
	}

	if !p.RestrictSUIDSGID {
		t.Error("RestrictSUIDSGID should be true for standard profile")
	}
}

// TestGetMinimalProfile verifies the minimal security profile structure.
func TestGetMinimalProfile(t *testing.T) {
	p := GetMinimalProfile()

	if p == nil {
		t.Fatal("GetMinimalProfile() returned nil")
	}

	if p.Level != SecurityLevelMinimal {
		t.Errorf("Level = %q, want %q", p.Level, SecurityLevelMinimal)
	}

	if p.ProtectSystem != "full" {
		t.Errorf("ProtectSystem = %q, want %q", p.ProtectSystem, "full")
	}

	if p.ProtectHome != "read-only" {
		t.Errorf("ProtectHome = %q, want %q", p.ProtectHome, "read-only")
	}

	// Minimal allows shared tmp and device access
	if p.PrivateTmp {
		t.Error("PrivateTmp should be false for minimal profile")
	}

	if p.PrivateDevices {
		t.Error("PrivateDevices should be false for minimal profile")
	}

	if !p.NoNewPrivileges {
		t.Error("NoNewPrivileges should be true for minimal profile")
	}

	// Minimal allows namespace creation
	if p.RestrictNamespaces {
		t.Error("RestrictNamespaces should be false for minimal profile")
	}

	// Minimal has broader capability set
	expectedCaps := []string{
		"CAP_NET_BIND_SERVICE",
		"CAP_SETGID",
		"CAP_SETUID",
		"CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
	}

	for _, cap := range expectedCaps {
		found := false
		for _, got := range p.CapabilityBoundingSet {
			if got == cap {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("CapabilityBoundingSet should contain %s for minimal profile", cap)
		}
	}

	// Minimal has no syscall filter
	if len(p.SystemCallFilter) != 0 {
		t.Errorf("SystemCallFilter should be empty for minimal profile, got %v", p.SystemCallFilter)
	}

	// Minimal allows SUID/SGID files
	if p.RestrictSUIDSGID {
		t.Error("RestrictSUIDSGID should be false for minimal profile")
	}
}

// TestGetProfileForLevel verifies dispatch to correct profiles by level.
func TestGetProfileForLevel(t *testing.T) {
	tests := []struct {
		level         SecurityLevel
		expectedLevel SecurityLevel
	}{
		{SecurityLevelStrict, SecurityLevelStrict},
		{SecurityLevelStandard, SecurityLevelStandard},
		{SecurityLevelMinimal, SecurityLevelMinimal},
		{"unknown", SecurityLevelMinimal}, // unknown falls back to minimal
		{"", SecurityLevelMinimal},        // empty falls back to minimal
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			p := GetProfileForLevel(tt.level)

			if p == nil {
				t.Fatalf("GetProfileForLevel(%q) returned nil", tt.level)
			}

			if p.Level != tt.expectedLevel {
				t.Errorf("GetProfileForLevel(%q).Level = %q, want %q", tt.level, p.Level, tt.expectedLevel)
			}
		})
	}
}

// TestGetProfileForService verifies service-to-profile lookup.
func TestGetProfileForService(t *testing.T) {
	tests := []struct {
		service       string
		expectedLevel SecurityLevel
	}{
		// Strict services
		{"carbonio-memcached.service", SecurityLevelStrict},
		{"carbonio-opendkim.service", SecurityLevelStrict},
		{"carbonio-freshclam.service", SecurityLevelStrict},
		{"carbonio-saslauthd.service", SecurityLevelStrict},
		{"carbonio-stats.service", SecurityLevelStrict},
		// Standard services
		{"carbonio-nginx.service", SecurityLevelStandard},
		{"carbonio-postfix.service", SecurityLevelStandard},
		// Minimal services
		{"carbonio-configd.service", SecurityLevelMinimal},
		{"carbonio-appserver.service", SecurityLevelMinimal},
		{"carbonio-openldap.service", SecurityLevelMinimal},
		// Unknown service defaults to minimal
		{"carbonio-unknown.service", SecurityLevelMinimal},
		{"", SecurityLevelMinimal},
	}

	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			p := GetProfileForService(tt.service)

			if p == nil {
				t.Fatalf("GetProfileForService(%q) returned nil", tt.service)
			}

			if p.Level != tt.expectedLevel {
				t.Errorf("GetProfileForService(%q).Level = %q, want %q", tt.service, p.Level, tt.expectedLevel)
			}
		})
	}
}

// TestToDropInContent verifies drop-in file content generation.
func TestToDropInContent(t *testing.T) {
	t.Run("strict profile contains [Service] header", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.HasPrefix(content, "[Service]\n") {
			t.Errorf("content should start with [Service], got: %q", content[:minInt(len(content), 30)])
		}
	})

	t.Run("contains security profile comment", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "# Security profile: strict") {
			t.Errorf("content should contain profile level comment, got:\n%s", content)
		}
	})

	t.Run("contains generated-by comment", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "# Generated by configd") {
			t.Errorf("content should contain generated-by comment, got:\n%s", content)
		}
	})

	t.Run("ends with newline", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.HasSuffix(content, "\n") {
			t.Error("content should end with newline")
		}
	})

	t.Run("strict profile has ProtectSystem=strict", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "ProtectSystem=strict") {
			t.Errorf("strict content should contain ProtectSystem=strict, got:\n%s", content)
		}
	})

	t.Run("strict profile drops all capabilities", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "CapabilityBoundingSet=") {
			t.Errorf("strict content should contain CapabilityBoundingSet= (empty drop), got:\n%s", content)
		}
	})

	t.Run("strict profile has MemoryDenyWriteExecute=yes", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "MemoryDenyWriteExecute=yes") {
			t.Errorf("strict content should contain MemoryDenyWriteExecute=yes, got:\n%s", content)
		}
	})

	t.Run("strict profile has RestrictNamespaces=yes", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "RestrictNamespaces=yes") {
			t.Errorf("strict content should contain RestrictNamespaces=yes, got:\n%s", content)
		}
	})

	t.Run("strict profile has SystemCallFilter and error number", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "SystemCallFilter=@system-service") {
			t.Errorf("strict content should contain SystemCallFilter=@system-service, got:\n%s", content)
		}

		if !strings.Contains(content, "SystemCallErrorNumber=EPERM") {
			t.Errorf("strict content should contain SystemCallErrorNumber=EPERM, got:\n%s", content)
		}
	})

	t.Run("strict profile has RestrictSUIDSGID=yes", func(t *testing.T) {
		p := GetStrictProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "RestrictSUIDSGID=yes") {
			t.Errorf("strict content should contain RestrictSUIDSGID=yes, got:\n%s", content)
		}
	})

	t.Run("standard profile has ProtectSystem=full", func(t *testing.T) {
		p := GetStandardProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "ProtectSystem=full") {
			t.Errorf("standard content should contain ProtectSystem=full, got:\n%s", content)
		}
	})

	t.Run("standard profile does not have MemoryDenyWriteExecute", func(t *testing.T) {
		p := GetStandardProfile()
		content := p.ToDropInContent()

		if strings.Contains(content, "MemoryDenyWriteExecute") {
			t.Errorf("standard content should not contain MemoryDenyWriteExecute (JIT allowed), got:\n%s", content)
		}
	})

	t.Run("standard profile has CAP_NET_BIND_SERVICE", func(t *testing.T) {
		p := GetStandardProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "CAP_NET_BIND_SERVICE") {
			t.Errorf("standard content should contain CAP_NET_BIND_SERVICE, got:\n%s", content)
		}
	})

	t.Run("minimal profile has ProtectHome=read-only", func(t *testing.T) {
		p := GetMinimalProfile()
		content := p.ToDropInContent()

		if !strings.Contains(content, "ProtectHome=read-only") {
			t.Errorf("minimal content should contain ProtectHome=read-only, got:\n%s", content)
		}
	})

	t.Run("minimal profile does not have SystemCallFilter line", func(t *testing.T) {
		p := GetMinimalProfile()
		content := p.ToDropInContent()

		if strings.Contains(content, "SystemCallFilter=") {
			t.Errorf("minimal content should not contain SystemCallFilter (no filter), got:\n%s", content)
		}
	})

	t.Run("service with PrivateNetwork outputs PrivateNetwork=yes", func(t *testing.T) {
		p := GetStrictProfile()
		p.PrivateNetwork = true
		content := p.ToDropInContent()

		if !strings.Contains(content, "PrivateNetwork=yes") {
			t.Errorf("content should contain PrivateNetwork=yes when set, got:\n%s", content)
		}
	})
}

// TestGetDirectives verifies directives map for each profile.
func TestGetDirectives(t *testing.T) {
	t.Run("strict profile returns non-empty map", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		if len(directives) == 0 {
			t.Error("GetDirectives() returned empty map for strict profile")
		}
	})

	t.Run("strict profile has expected directives", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		checks := map[string]string{
			"ProtectSystem":        "strict",
			"ProtectHome":          "yes",
			"PrivateTmp":           "yes",
			"PrivateDevices":       "yes",
			"NoNewPrivileges":      "yes",
			"ProtectKernelModules": "yes",
			"ProtectControlGroups": "yes",
			"RestrictRealtime":     "yes",
			"ProtectHostname":      "yes",
			"ProtectClock":         "yes",
			"LockPersonality":      "yes",
		}

		for key, want := range checks {
			got, ok := directives[key]
			if !ok {
				t.Errorf("directives missing key %q", key)
				continue
			}

			if got != want {
				t.Errorf("directives[%q] = %q, want %q", key, got, want)
			}
		}
	})

	t.Run("strict profile has MemoryDenyWriteExecute", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		if val, ok := directives["MemoryDenyWriteExecute"]; !ok || val != "yes" {
			t.Errorf("strict directives[MemoryDenyWriteExecute] = %q (ok=%v), want yes", directives["MemoryDenyWriteExecute"], ok)
		}
	})

	t.Run("strict profile has RestrictNamespaces", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		if val, ok := directives["RestrictNamespaces"]; !ok || val != "yes" {
			t.Errorf("strict directives[RestrictNamespaces] = %q (ok=%v), want yes", directives["RestrictNamespaces"], ok)
		}
	})

	t.Run("strict profile has RestrictSUIDSGID", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		if val, ok := directives["RestrictSUIDSGID"]; !ok || val != "yes" {
			t.Errorf("strict directives[RestrictSUIDSGID] = %q (ok=%v), want yes", directives["RestrictSUIDSGID"], ok)
		}
	})

	t.Run("strict profile has SystemCallFilter", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		if _, ok := directives["SystemCallFilter"]; !ok {
			t.Error("strict directives missing SystemCallFilter")
		}
	})

	t.Run("strict profile has SystemCallErrorNumber", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		if val, ok := directives["SystemCallErrorNumber"]; !ok || val != "EPERM" {
			t.Errorf("strict directives[SystemCallErrorNumber] = %q (ok=%v), want EPERM", directives["SystemCallErrorNumber"], ok)
		}
	})

	t.Run("strict profile has RestrictAddressFamilies", func(t *testing.T) {
		p := GetStrictProfile()
		directives := p.GetDirectives()

		if _, ok := directives["RestrictAddressFamilies"]; !ok {
			t.Error("strict directives missing RestrictAddressFamilies")
		}
	})

	t.Run("standard profile does not have MemoryDenyWriteExecute", func(t *testing.T) {
		p := GetStandardProfile()
		directives := p.GetDirectives()

		if _, ok := directives["MemoryDenyWriteExecute"]; ok {
			t.Error("standard directives should not contain MemoryDenyWriteExecute")
		}
	})

	t.Run("standard profile has CapabilityBoundingSet", func(t *testing.T) {
		p := GetStandardProfile()
		directives := p.GetDirectives()

		if _, ok := directives["CapabilityBoundingSet"]; !ok {
			t.Error("standard directives missing CapabilityBoundingSet")
		}
	})

	t.Run("minimal profile does not have RestrictNamespaces", func(t *testing.T) {
		p := GetMinimalProfile()
		directives := p.GetDirectives()

		if _, ok := directives["RestrictNamespaces"]; ok {
			t.Error("minimal directives should not contain RestrictNamespaces")
		}
	})

	t.Run("minimal profile does not have SystemCallFilter", func(t *testing.T) {
		p := GetMinimalProfile()
		directives := p.GetDirectives()

		if _, ok := directives["SystemCallFilter"]; ok {
			t.Error("minimal directives should not contain SystemCallFilter")
		}
	})

	t.Run("minimal profile does not have RestrictSUIDSGID", func(t *testing.T) {
		p := GetMinimalProfile()
		directives := p.GetDirectives()

		if _, ok := directives["RestrictSUIDSGID"]; ok {
			t.Error("minimal directives should not contain RestrictSUIDSGID")
		}
	})

	t.Run("PrivateNetwork included only when true", func(t *testing.T) {
		p := GetStrictProfile()
		p.PrivateNetwork = true
		directives := p.GetDirectives()

		if val, ok := directives["PrivateNetwork"]; !ok || val != "yes" {
			t.Errorf("directives[PrivateNetwork] = %q (ok=%v), want yes", directives["PrivateNetwork"], ok)
		}

		p2 := GetStrictProfile()
		p2.PrivateNetwork = false
		directives2 := p2.GetDirectives()

		if _, ok := directives2["PrivateNetwork"]; ok {
			t.Error("directives should not contain PrivateNetwork when false")
		}
	})
}

// TestGetAllServiceSecurityLevels verifies the grouped service levels map.
func TestGetAllServiceSecurityLevels(t *testing.T) {
	result := GetAllServiceSecurityLevels()

	if len(result) == 0 {
		t.Fatal("GetAllServiceSecurityLevels() returned empty map")
	}

	// All three levels must be present as keys
	for _, level := range []SecurityLevel{SecurityLevelStrict, SecurityLevelStandard, SecurityLevelMinimal} {
		services, ok := result[level]
		if !ok {
			t.Errorf("result missing level %q", level)
			continue
		}

		if len(services) == 0 {
			t.Errorf("result[%q] is empty, expected services", level)
		}
	}

	// Verify specific services appear in the right buckets
	strictServices := result[SecurityLevelStrict]
	standardServices := result[SecurityLevelStandard]
	minimalServices := result[SecurityLevelMinimal]

	checkContains := func(t *testing.T, slice []string, service string) {
		t.Helper()

		for _, s := range slice {
			if s == service {
				return
			}
		}

		t.Errorf("expected %q in slice, got %v", service, slice)
	}

	checkContains(t, strictServices, "carbonio-memcached.service")
	checkContains(t, strictServices, "carbonio-opendkim.service")
	checkContains(t, standardServices, "carbonio-nginx.service")
	checkContains(t, standardServices, "carbonio-postfix.service")
	checkContains(t, minimalServices, "carbonio-configd.service")
	checkContains(t, minimalServices, "carbonio-appserver.service")
}

// TestGetAllServiceSecurityLevels_SortedOutput verifies that each slice is sorted.
func TestGetAllServiceSecurityLevels_SortedOutput(t *testing.T) {
	result := GetAllServiceSecurityLevels()

	for level, services := range result {
		for i := 1; i < len(services); i++ {
			if services[i] < services[i-1] {
				t.Errorf("level %q: services not sorted at index %d: %q > %q",
					level, i, services[i-1], services[i])
			}
		}
	}
}

// TestGetAllServiceSecurityLevels_TotalCount verifies total count matches ServiceSecurityMapping.
func TestGetAllServiceSecurityLevels_TotalCount(t *testing.T) {
	result := GetAllServiceSecurityLevels()

	total := 0
	for _, services := range result {
		total += len(services)
	}

	if total != len(ServiceSecurityMapping) {
		t.Errorf("total services in result = %d, want %d (len of ServiceSecurityMapping)",
			total, len(ServiceSecurityMapping))
	}
}

// minInt returns the smaller of two ints (helper for error messages).
func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
