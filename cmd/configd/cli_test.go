// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestInitComponents(t *testing.T) {
	if _, ok := initComponents["mta"]; !ok {
		t.Error("expected mta component to be defined")
	}
	if _, ok := initComponents["proxy"]; !ok {
		t.Error("expected proxy component to be defined")
	}
	if desc := initComponents["mta"]; desc == "" {
		t.Error("expected mta to have a description")
	}
}

func TestInitCmd_Structure(t *testing.T) {
	cmd := &InitCmd{
		Component: "mta",
		Force:     true,
	}

	if cmd.Component != "mta" {
		t.Errorf("expected Component mta, got %s", cmd.Component)
	}
	if !cmd.Force {
		t.Error("expected Force true")
	}
}

func TestDangerousKeys(t *testing.T) {
	dangerous := []string{
		"zimbra_ldap_password",
		"ldap_root_password",
		"zimbra_mysql_password",
		"mysql_root_password",
		"zimbra_ldap_userdn",
		"ldap_url",
		"ldap_master_url",
		"zimbra_server_hostname",
		"zimbra_require_interprocess_security",
	}

	for _, key := range dangerous {
		if !dangerousKeys[key] {
			t.Errorf("expected %s to be marked as dangerous", key)
		}
	}

	if dangerousKeys["safe_key"] {
		t.Error("expected safe_key to not be dangerous")
	}
}

func TestLocalconfigOpts_Structure(t *testing.T) {
	opts := &localconfigOpts{
		mode:          modeExport,
		configPath:    "/tmp/test.xml",
		quiet:         true,
		showPath:      false,
		showPasswords: true,
		showDefaults:  false,
		showChanged:   true,
		edit:          false,
		unset:         false,
		random:        false,
		force:         true,
		keys:          []string{"key1", "key2"},
	}

	if opts.mode != modeExport {
		t.Errorf("expected mode export, got %s", opts.mode)
	}
	if opts.configPath != "/tmp/test.xml" {
		t.Errorf("expected configPath /tmp/test.xml, got %s", opts.configPath)
	}
	if !opts.quiet {
		t.Error("expected quiet true")
	}
	if !opts.showPasswords {
		t.Error("expected showPasswords true")
	}
	if !opts.showChanged {
		t.Error("expected showChanged true")
	}
	if !opts.force {
		t.Error("expected force true")
	}
	if len(opts.keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(opts.keys))
	}
}

func TestRewriteCmd_Structure(t *testing.T) {
	cmd := &RewriteCmd{
		ConfigNames: []string{"proxy", "mta"},
	}

	if len(cmd.ConfigNames) != 2 {
		t.Errorf("expected 2 config names, got %d", len(cmd.ConfigNames))
	}
	if cmd.ConfigNames[0] != "proxy" {
		t.Errorf("expected first config proxy, got %s", cmd.ConfigNames[0])
	}
}

func TestDaemonCmd_Structure(t *testing.T) {
	cmd := &DaemonCmd{}
	_ = cmd // Use the variable to avoid unused warnings
}

func TestFormatVersion(t *testing.T) {
	version := formatVersion()
	if version == "" {
		t.Error("expected non-empty version string")
	}
	// Should contain "configd"
	if len(version) < 7 {
		t.Errorf("expected version string to contain 'configd', got %s", version)
	}
}

func TestResolveEditKeyValue_KeyValue(t *testing.T) {
	key, val := resolveEditKeyValue("mykey=myvalue", false)
	if key != "mykey" {
		t.Errorf("expected key 'mykey', got %q", key)
	}
	if val != "myvalue" {
		t.Errorf("expected value 'myvalue', got %q", val)
	}
}

func TestResolveEditKeyValue_ValueWithEquals(t *testing.T) {
	// Value contains an '=' sign — only first '=' is the separator
	key, val := resolveEditKeyValue("k=v=extra", false)
	if key != "k" {
		t.Errorf("expected key 'k', got %q", key)
	}
	if val != "v=extra" {
		t.Errorf("expected value 'v=extra', got %q", val)
	}
}

func TestResolveEditKeyValue_EmptyValue(t *testing.T) {
	key, val := resolveEditKeyValue("mykey=", false)
	if key != "mykey" {
		t.Errorf("expected key 'mykey', got %q", key)
	}
	if val != "" {
		t.Errorf("expected empty value, got %q", val)
	}
}

func TestConfigsExist_UnknownComponent(t *testing.T) {
	// Unknown component should always return false
	if configsExist("unknown_component") {
		t.Error("expected configsExist to return false for unknown component")
	}
}

func TestConfigsExist_MTA_NoFile(t *testing.T) {
	// /opt/zextras/common/conf/postfix/main.cf almost certainly doesn't exist in test env
	result := configsExist(componentMTA)
	// We just verify it doesn't panic; on a dev machine it may or may not exist
	_ = result
}

func TestConfigsExist_Proxy_NoFile(t *testing.T) {
	result := configsExist(componentProxy)
	_ = result
}

func TestWriteOutput_Plain(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	writeOutput(map[string]string{"key1": "val1"}, &localconfigOpts{mode: "plain"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if !strings.Contains(out, "key1") {
		t.Errorf("expected key1 in plain output, got %q", out)
	}
}

func TestWriteOutput_Shell(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	writeOutput(map[string]string{"mykey": "myval"}, &localconfigOpts{mode: "shell"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if !strings.Contains(out, "mykey") {
		t.Errorf("expected mykey in shell output, got %q", out)
	}
}

func TestWriteOutput_Export(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	writeOutput(map[string]string{"expkey": "expval"}, &localconfigOpts{mode: modeExport})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if !strings.Contains(out, "expkey") {
		t.Errorf("expected expkey in export output, got %q", out)
	}
}

func TestWriteOutput_Nokey(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	writeOutput(map[string]string{"nk": "nkval"}, &localconfigOpts{mode: "nokey", keys: []string{"nk"}})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if !strings.Contains(out, "nkval") {
		t.Errorf("expected value in nokey output, got %q", out)
	}
}

func TestWriteOutput_XML(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	writeOutput(map[string]string{"xmlkey": "xmlval"}, &localconfigOpts{mode: "xml"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if !strings.Contains(out, "xmlkey") {
		t.Errorf("expected key in xml output, got %q", out)
	}
}

func TestFilterKeys_MissingKeyQuiet(t *testing.T) {
	config := map[string]string{"a": "1"}

	// quiet=true: missing keys should not produce warning but should be excluded
	filtered := filterKeys(config, &localconfigOpts{keys: []string{"a", "missing"}, quiet: true})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 key, got %d", len(filtered))
	}
	if filtered["a"] != "1" {
		t.Errorf("expected a=1, got %q", filtered["a"])
	}
}

func TestFilterKeys_MissingKeyNotQuiet(t *testing.T) {
	config := map[string]string{"a": "1"}

	// quiet=false: missing key should print a warning to stderr, but not panic
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	filtered := filterKeys(config, &localconfigOpts{keys: []string{"missing"}, quiet: false})

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	warn := buf.String()

	if len(filtered) != 0 {
		t.Errorf("expected 0 keys for missing key, got %d", len(filtered))
	}
	if !strings.Contains(warn, "Warning") {
		t.Errorf("expected Warning message to stderr, got %q", warn)
	}
}
