// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"os"
	"strings"
	"testing"
)

func TestInterpolate_SimpleReference(t *testing.T) {
	config := map[string]string{
		"zimbra_home":          "/opt/zextras",
		"zimbra_log_directory": "${zimbra_home}/log",
	}
	subs := Interpolate(config)

	if subs != 1 {
		t.Errorf("expected 1 substitution, got %d", subs)
	}

	if config["zimbra_log_directory"] != "/opt/zextras/log" {
		t.Errorf("expected /opt/zextras/log, got %q", config["zimbra_log_directory"])
	}
}

func TestInterpolate_TransitiveReference(t *testing.T) {
	config := map[string]string{
		"zimbra_home":          "/opt/zextras",
		"zimbra_log_directory": "${zimbra_home}/log",
		"mysql_errlogfile":     "${zimbra_log_directory}/mysql_error.log",
	}
	Interpolate(config)

	if config["mysql_errlogfile"] != "/opt/zextras/log/mysql_error.log" {
		t.Errorf("expected /opt/zextras/log/mysql_error.log, got %q", config["mysql_errlogfile"])
	}
}

func TestInterpolate_UnresolvedReference(t *testing.T) {
	config := map[string]string{
		"value_with_missing": "prefix-${does_not_exist}-suffix",
	}
	Interpolate(config)

	// Unresolved references should be left as-is
	if config["value_with_missing"] != "prefix-${does_not_exist}-suffix" {
		t.Errorf("expected unresolved reference preserved, got %q", config["value_with_missing"])
	}
}

func TestInterpolate_NoReferences(t *testing.T) {
	config := map[string]string{
		"key1": "plain_value",
		"key2": "another_value",
	}
	subs := Interpolate(config)

	if subs != 0 {
		t.Errorf("expected 0 substitutions, got %d", subs)
	}
}

func TestInterpolate_MultipleReferencesInOneValue(t *testing.T) {
	config := map[string]string{
		"host": "localhost",
		"port": "389",
		"url":  "ldap://${host}:${port}",
	}
	Interpolate(config)

	if config["url"] != "ldap://localhost:389" {
		t.Errorf("expected ldap://localhost:389, got %q", config["url"])
	}
}

func TestInterpolate_MailboxdJavaOptions(t *testing.T) {
	// This is the real-world case: mailboxd_java_options contains ${networkaddress_cache_ttl}
	config := map[string]string{
		"networkaddress_cache_ttl": "60",
		"mailboxd_java_options":    "-Dsun.net.inetaddr.ttl=${networkaddress_cache_ttl} -XX:+UseG1GC",
	}
	Interpolate(config)

	expected := "-Dsun.net.inetaddr.ttl=60 -XX:+UseG1GC"
	if config["mailboxd_java_options"] != expected {
		t.Errorf("expected %q, got %q", expected, config["mailboxd_java_options"])
	}
}

func TestInterpolate_EmptyValue(t *testing.T) {
	config := map[string]string{
		"empty":     "",
		"reference": "${empty}/suffix",
	}
	Interpolate(config)

	if config["reference"] != "/suffix" {
		t.Errorf("expected /suffix, got %q", config["reference"])
	}
}

func TestMergeDefaults_XMLOverridesDefault(t *testing.T) {
	config := map[string]string{
		"ldap_port": "636", // XML value
	}
	MergeDefaults(config)

	// XML value should not be overridden
	if config["ldap_port"] != "636" {
		t.Errorf("expected XML value 636, got %q", config["ldap_port"])
	}
}

func TestMergeDefaults_DefaultAppliedForMissingKey(t *testing.T) {
	config := map[string]string{}
	MergeDefaults(config)

	// Should get defaults for keys not in XML
	if config["networkaddress_cache_ttl"] != "60" {
		t.Errorf("expected default 60, got %q", config["networkaddress_cache_ttl"])
	}

	if config["antispam_enable_restarts"] != "true" {
		t.Errorf("expected default true, got %q", config["antispam_enable_restarts"])
	}

	if config["zimbra_home"] != "/opt/zextras" {
		t.Errorf("expected default /opt/zextras, got %q", config["zimbra_home"])
	}
}

func TestLoadResolvedConfigFromFile(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
  <key name="ldap_port">
    <value>389</value>
  </key>
  <key name="ldap_url">
    <value>ldap://mail.example.com:389</value>
  </key>
</localconfig>`

	tmpFile := createResolveTestFile(t, xmlContent)
	defer os.Remove(tmpFile)

	config, err := LoadResolvedConfigFromFile(tmpFile)
	if err != nil {
		t.Fatalf("LoadResolvedConfigFromFile() failed: %v", err)
	}

	// XML values present
	if config["ldap_port"] != "389" {
		t.Errorf("expected 389 from XML, got %q", config["ldap_port"])
	}

	// Defaults merged
	if config["networkaddress_cache_ttl"] != "60" {
		t.Errorf("expected default 60, got %q", config["networkaddress_cache_ttl"])
	}

	// ${variable} interpolated
	if config["zimbra_log_directory"] != "/opt/zextras/log" {
		t.Errorf("expected /opt/zextras/log, got %q", config["zimbra_log_directory"])
	}

	if config["zimbra_log4j_properties"] != "/opt/zextras/conf/log4j.properties" {
		t.Errorf("expected /opt/zextras/conf/log4j.properties, got %q", config["zimbra_log4j_properties"])
	}
}

func TestLoadResolvedConfigFromFile_MailboxdJavaOptionsInterpolated(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
  <key name="ldap_port">
    <value>389</value>
  </key>
</localconfig>`

	tmpFile := createResolveTestFile(t, xmlContent)
	defer os.Remove(tmpFile)

	config, err := LoadResolvedConfigFromFile(tmpFile)
	if err != nil {
		t.Fatalf("LoadResolvedConfigFromFile() failed: %v", err)
	}

	// mailboxd_java_options default contains ${networkaddress_cache_ttl} which should be resolved to 60
	opts := config["mailboxd_java_options"]
	if strings.Contains(opts, "${networkaddress_cache_ttl}") {
		t.Errorf("mailboxd_java_options still contains unresolved ${networkaddress_cache_ttl}: %q", opts)
	}

	if !strings.Contains(opts, "-Dsun.net.inetaddr.ttl=60") {
		t.Errorf("mailboxd_java_options missing resolved ttl=60: %q", opts)
	}
}

func TestLoadResolvedConfigFromFile_XMLOverridesDefault(t *testing.T) {
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
  <key name="networkaddress_cache_ttl">
    <value>120</value>
  </key>
</localconfig>`

	tmpFile := createResolveTestFile(t, xmlContent)
	defer os.Remove(tmpFile)

	config, err := LoadResolvedConfigFromFile(tmpFile)
	if err != nil {
		t.Fatalf("LoadResolvedConfigFromFile() failed: %v", err)
	}

	// XML value should override the default of 60
	if config["networkaddress_cache_ttl"] != "120" {
		t.Errorf("expected XML value 120, got %q", config["networkaddress_cache_ttl"])
	}

	// And the interpolated value in mailboxd_java_options should use 120
	opts := config["mailboxd_java_options"]
	if !strings.Contains(opts, "-Dsun.net.inetaddr.ttl=120") {
		t.Errorf("mailboxd_java_options should use XML override of 120: %q", opts)
	}
}

func TestFormatAsShell(t *testing.T) {
	config := map[string]string{
		"key1": "simple_value",
		"key2": "value with spaces",
	}

	output := FormatAsShell(config)

	if !strings.Contains(output, "key1='simple_value';\n") {
		t.Errorf("expected shell format for key1, got:\n%s", output)
	}

	if !strings.Contains(output, "key2='value with spaces';\n") {
		t.Errorf("expected shell format for key2, got:\n%s", output)
	}
}

func TestFormatAsShell_SingleQuoteEscaping(t *testing.T) {
	config := map[string]string{
		"key": "it's a test",
	}

	output := FormatAsShell(config)

	expected := "key='it'\\''s a test';\n"
	if output != expected {
		t.Errorf("expected %q, got %q", expected, output)
	}
}

func createResolveTestFile(t *testing.T, content string) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "localconfig-resolve-*.xml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	tmpFile.Close()

	return tmpFile.Name()
}
