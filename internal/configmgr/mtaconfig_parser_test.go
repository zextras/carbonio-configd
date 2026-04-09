// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/state"
	"os"
	"path/filepath"
	"testing"
)

// TestParseMtaConfig_ValidFile tests parsing a valid zmconfigd.cf file
func TestParseMtaConfig_ValidFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	// Create a minimal valid config file
	content := `SECTION dhparam
	VAR zimbraSSLDHParam
	MAPFILE zimbraSSLDHParam

SECTION amavis
	REWRITE conf/amavisd.conf.in conf/amavisd.conf
	POSTCONF content_filter
	LOCAL av_notify_domain
	VAR zimbraAmavisMaxServers
	RESTART antivirus amavis mta

SECTION mta DEPENDS amavis
	VAR zimbraMtaMyNetworks
	POSTCONF mynetworks VAR zimbraMtaMyNetworks
	if VAR zimbraMtaAuthEnabled
		POSTCONF smtpd_sasl_auth_enable yes
	fi
	RESTART mta
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify sections were parsed
	if len(cm.State.MtaConfig.Sections) == 0 {
		t.Fatal("Expected sections to be parsed")
	}

	// Check dhparam section
	if section, ok := cm.State.MtaConfig.Sections["dhparam"]; ok {
		if section.Name != "dhparam" {
			t.Errorf("Expected section name 'dhparam', got: %s", section.Name)
		}
		if _, hasVar := section.RequiredVars["zimbraSSLDHParam"]; !hasVar {
			t.Error("Expected dhparam section to have zimbraSSLDHParam in RequiredVars")
		}
	} else {
		t.Error("Expected dhparam section to be parsed")
	}

	// Check amavis section
	if section, ok := cm.State.MtaConfig.Sections["amavis"]; ok {
		if section.Name != "amavis" {
			t.Errorf("Expected section name 'amavis', got: %s", section.Name)
		}
		if len(section.Rewrites) == 0 {
			t.Error("Expected amavis section to have rewrites")
		}
		if len(section.Restarts) == 0 {
			t.Error("Expected amavis section to have restarts")
		}
	} else {
		t.Error("Expected amavis section to be parsed")
	}

	// Check mta section with dependency
	if section, ok := cm.State.MtaConfig.Sections["mta"]; ok {
		if _, hasDep := section.Depends["amavis"]; !hasDep {
			t.Error("Expected mta section to depend on amavis")
		}
		if len(section.Conditionals) == 0 {
			t.Error("Expected mta section to have conditionals (if/fi blocks)")
		}
	} else {
		t.Error("Expected mta section to be parsed")
	}
}

// TestParseMtaConfig_InvalidFile tests parsing with non-existent file
func TestParseMtaConfig_InvalidFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "nonexistent.cf")

	cm := setupTestConfigManagerForParser(tempDir)

	err := cm.ParseMtaConfig(context.Background(), configFile)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}
}

// TestParseMtaConfig_EmptyFile tests parsing an empty file
func TestParseMtaConfig_EmptyFile(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "empty.cf")

	err := os.WriteFile(configFile, []byte(""), 0644)
	if err != nil {
		t.Fatalf("Failed to create empty file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error for empty file, got: %v", err)
	}

	// Empty file should have no sections
	if len(cm.State.MtaConfig.Sections) != 0 {
		t.Errorf("Expected 0 sections, got: %d", len(cm.State.MtaConfig.Sections))
	}
}

// TestParseMtaConfig_WithConditionals tests parsing conditionals
func TestParseMtaConfig_WithConditionals(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION mta
	VAR zimbraMtaEnableSmtpdPolicyd
	if VAR zimbraMtaEnableSmtpdPolicyd
		POSTCONF policy_time_limit VAR zimbraMtaPolicyTimeLimit
		POSTCONFD policy.cf
	fi
	if VAR !zimbraMtaEnableSmtpdPolicyd
		POSTCONFD policy_time_limit
	fi
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["mta"]
	if !ok {
		t.Fatal("Expected mta section to be parsed")
	}

	if len(section.Conditionals) == 0 {
		t.Fatal("Expected conditionals to be parsed")
	}

	// Check first conditional (positive)
	cond := section.Conditionals[0]
	if cond.Type != "VAR" {
		t.Errorf("Expected conditional type 'VAR', got: %s", cond.Type)
	}
	if cond.Key != "zimbraMtaEnableSmtpdPolicyd" {
		t.Errorf("Expected conditional key 'zimbraMtaEnableSmtpdPolicyd', got: %s", cond.Key)
	}
	if cond.Negated {
		t.Error("Expected first conditional to not be negated")
	}
}

// TestParseMtaConfig_WithNestedConditionals tests parsing nested if blocks
func TestParseMtaConfig_WithNestedConditionals(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION proxy
	VAR zimbraReverseProxyMailEnabled
	if VAR zimbraReverseProxyMailEnabled
		VAR zimbraReverseProxyImapEnabled
		if VAR zimbraReverseProxyImapEnabled
			POSTCONF imap_enabled yes
		fi
	fi
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["proxy"]
	if !ok {
		t.Fatal("Expected proxy section to be parsed")
	}

	if len(section.Conditionals) == 0 {
		t.Fatal("Expected conditionals to be parsed")
	}

	// Check outer conditional has nested conditional
	outerCond := section.Conditionals[0]
	if len(outerCond.Nested) == 0 {
		t.Error("Expected outer conditional to have nested conditionals")
	}
}

// TestParseMtaConfig_LDAPLookup tests LDAP directive with lookup
func TestParseMtaConfig_LDAPLookup(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION mta
	LOCAL ldap_url
	LOCAL zimbra_server_hostname
	LDAP ldap-alias LOCAL ldap_url
	LDAP ldap-vam LOCAL zimbra_server_hostname
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)
	// Set up config values for lookup
	cm.State.LocalConfig.Data["ldap_url"] = "ldap://localhost:389"
	cm.State.LocalConfig.Data["zimbra_server_hostname"] = "mail.example.com"

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["mta"]
	if !ok {
		t.Fatal("Expected mta section to be parsed")
	}

	// Check LDAP values were resolved
	if val, ok := section.Ldap["ldap-alias"]; ok {
		if val != "ldap://localhost:389" {
			t.Errorf("Expected LDAP value to be 'ldap://localhost:389', got: %s", val)
		}
	} else {
		t.Error("Expected ldap-alias to be in LDAP map")
	}

	if val, ok := section.Ldap["ldap-vam"]; ok {
		if val != "mail.example.com" {
			t.Errorf("Expected LDAP value to be 'mail.example.com', got: %s", val)
		}
	} else {
		t.Error("Expected ldap-vam to be in LDAP map")
	}
}

// TestParseMtaConfig_LDAPLookupFailure tests LDAP directive with failed lookup
func TestParseMtaConfig_LDAPLookupFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION mta
	LDAP ldap-alias LOCAL nonexistent_key
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error (lookup failure should be treated as empty), got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["mta"]
	if !ok {
		t.Fatal("Expected mta section to be parsed")
	}

	// Failed lookup should result in empty string
	if val, ok := section.Ldap["ldap-alias"]; ok {
		if val != "" {
			t.Errorf("Expected empty value for failed lookup, got: %s", val)
		}
	} else {
		t.Error("Expected ldap-alias to be in LDAP map with empty value")
	}
}

// TestParseMtaConfig_InvalidLDAPSpec tests LDAP directive with invalid format
func TestParseMtaConfig_InvalidLDAPSpec(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION mta
	LDAP ldap-alias invalid_format_no_colon
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error (invalid spec should be skipped), got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["mta"]
	if !ok {
		t.Fatal("Expected mta section to be parsed")
	}

	// Invalid spec should still be in the map with original value
	if val, ok := section.Ldap["ldap-alias"]; ok {
		if val != "invalid_format_no_colon" {
			t.Errorf("Expected value to remain as 'invalid_format_no_colon', got: %s", val)
		}
	}
}

// TestParseMtaConfig_MultipleRewritesAndRestarts tests multiple REWRITE and RESTART directives
func TestParseMtaConfig_MultipleRewritesAndRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION amavis
	REWRITE conf/amavisd.conf.in conf/amavisd.conf
	REWRITE conf/clamd.conf.in conf/clamd.conf MODE 0600
	RESTART antivirus amavis mta
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["amavis"]
	if !ok {
		t.Fatal("Expected amavis section to be parsed")
	}

	// Check rewrites
	if len(section.Rewrites) != 2 {
		t.Errorf("Expected 2 rewrites, got: %d", len(section.Rewrites))
	}

	// Check restarts
	expectedRestarts := []string{"antivirus", "amavis", "mta"}
	for _, service := range expectedRestarts {
		if _, ok := section.Restarts[service]; !ok {
			t.Errorf("Expected restart for service '%s' to be in section", service)
		}
	}
}

// TestParseMtaConfig_ServiceConditional tests SERVICE conditional
func TestParseMtaConfig_ServiceConditional(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION amavis
	if SERVICE antivirus
		POSTCONF content_filter FILE zmconfigd/postfix_content_filter.cf
	fi
	if SERVICE !antispam
		POSTCONF spam_filter none
	fi
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["amavis"]
	if !ok {
		t.Fatal("Expected amavis section to be parsed")
	}

	if len(section.Conditionals) < 2 {
		t.Fatal("Expected at least 2 conditionals to be parsed")
	}

	// Check first conditional
	cond1 := section.Conditionals[0]
	if cond1.Type != "SERVICE" {
		t.Errorf("Expected conditional type 'SERVICE', got: %s", cond1.Type)
	}
	if cond1.Key != "antivirus" {
		t.Errorf("Expected conditional key 'antivirus', got: %s", cond1.Key)
	}

	// Check second conditional (negated)
	cond2 := section.Conditionals[1]
	if cond2.Type != "SERVICE" {
		t.Errorf("Expected conditional type 'SERVICE', got: %s", cond2.Type)
	}
	if cond2.Key != "antispam" {
		t.Errorf("Expected conditional key 'antispam', got: %s", cond2.Key)
	}
	if !cond2.Negated {
		t.Error("Expected second conditional to be negated")
	}
}

// TestParseMtaConfig_ProxygenDirective tests PROXYGEN directive
func TestParseMtaConfig_ProxygenDirective(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION proxy
	VAR zimbraReverseProxyMailEnabled
	PROXYGEN
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["proxy"]
	if !ok {
		t.Fatal("Expected proxy section to be parsed")
	}

	if !section.Proxygen {
		t.Error("Expected Proxygen to be true for proxy section")
	}
}

// TestParseMtaConfig_AllDirectiveTypes tests parsing with all directive types
func TestParseMtaConfig_AllDirectiveTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: configmgr test may have retry delays")
	}
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "zmconfigd.cf")

	content := `SECTION comprehensive DEPENDS mta
	VAR zimbraVar1
	LOCAL localVar1
	MAPFILE zimbraSSLDHParam
	MAPLOCAL zimbraSSLDHParam
	REWRITE input.txt output.txt MODE 0644
	POSTCONF mynetworks VAR zimbraMtaMyNetworks
	POSTCONFD policy.cf
	LDAP ldap-alias LOCAL ldap_url
	RESTART service1 service2
	PROXYGEN
	if VAR zimbraEnabled
		POSTCONF enabled yes
	fi
`

	err := os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cm := setupTestConfigManagerForParser(tempDir)
	cm.State.LocalConfig.Data["ldap_url"] = "ldap://localhost"

	err = cm.ParseMtaConfig(context.Background(), configFile)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	section, ok := cm.State.MtaConfig.Sections["comprehensive"]
	if !ok {
		t.Fatal("Expected comprehensive section to be parsed")
	}

	// Verify all components were parsed
	if _, ok := section.RequiredVars["zimbraVar1"]; !ok {
		t.Error("Expected VAR zimbraVar1 to be in RequiredVars")
	}
	if _, ok := section.RequiredVars["localVar1"]; !ok {
		t.Error("Expected LOCAL localVar1 to be in RequiredVars")
	}
	if len(section.Rewrites) == 0 {
		t.Error("Expected rewrites to be present")
	}
	if len(section.Restarts) == 0 {
		t.Error("Expected restarts to be present")
	}
	if _, ok := section.Depends["mta"]; !ok {
		t.Error("Expected dependency on mta")
	}
	if !section.Proxygen {
		t.Error("Expected Proxygen to be true")
	}
	if len(section.Conditionals) == 0 {
		t.Error("Expected conditionals to be present")
	}
}

// Helper function to set up a test ConfigManager for parser tests
func setupTestConfigManagerForParser(baseDir string) *ConfigManager {
	st := state.NewState()
	st.FirstRun = true

	cfg := &config.Config{
		BaseDir:    baseDir,
		ConfigFile: filepath.Join(baseDir, "zmconfigd.cf"),
	}

	// Create a minimal LDAP client (nil is fine for parser tests)
	var ldapClient *ldap.Ldap

	// Create cache with context
	ctx := context.Background()
	cacheInstance := cache.New(ctx, false)

	cm := NewConfigManager(ctx, cfg, st, ldapClient, cacheInstance)

	return cm
}
