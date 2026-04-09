// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"os"
	"testing"
)

func TestResolveEditKeyValue_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		arg       string
		random    bool
		wantKey   string
		wantValue string
	}{
		{
			name:      "simple key=value",
			arg:       "smtp_port=587",
			wantKey:   "smtp_port",
			wantValue: "587",
		},
		{
			name:      "value with embedded equals",
			arg:       "java_opts=-Xmx=512m",
			wantKey:   "java_opts",
			wantValue: "-Xmx=512m",
		},
		{
			name:      "empty value after equals",
			arg:       "unset_key=",
			wantKey:   "unset_key",
			wantValue: "",
		},
		{
			name:      "value with special chars",
			arg:       "ldap_url=ldap://host:389 ldap://host2:389",
			wantKey:   "ldap_url",
			wantValue: "ldap://host:389 ldap://host2:389",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, val := resolveEditKeyValue(tt.arg, tt.random)
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if !tt.random && val != tt.wantValue {
				t.Errorf("value = %q, want %q", val, tt.wantValue)
			}
		})
	}
}

func TestResolveEditKeyValue_Random(t *testing.T) {
	key, val := resolveEditKeyValue("my_password", true)
	if key != "my_password" {
		t.Errorf("key = %q, want %q", key, "my_password")
	}
	if len(val) == 0 {
		t.Error("expected non-empty random password")
	}
}

func TestLocalconfigCmd_Fields(t *testing.T) {
	cmd := &LocalconfigCmd{
		Mode:          modeExport,
		ConfigPath:    "/tmp/lc.xml",
		Quiet:         true,
		ShowPath:      true,
		ShowPasswords: false,
		ShowDefaults:  true,
		ShowChanged:   false,
		Edit:          false,
		Unset:         false,
		Random:        false,
		Force:         true,
		Key:           []string{"key1"},
		KeyArgs:       []string{"key2"},
	}

	if cmd.Mode != modeExport {
		t.Errorf("Mode = %q, want %q", cmd.Mode, modeExport)
	}
	if cmd.ConfigPath != "/tmp/lc.xml" {
		t.Errorf("ConfigPath = %q, want %q", cmd.ConfigPath, "/tmp/lc.xml")
	}
	if !cmd.Quiet {
		t.Error("expected Quiet=true")
	}
	if !cmd.ShowPath {
		t.Error("expected ShowPath=true")
	}
	if !cmd.ShowDefaults {
		t.Error("expected ShowDefaults=true")
	}
	if !cmd.Force {
		t.Error("expected Force=true")
	}
	if len(cmd.Key) != 1 || cmd.Key[0] != "key1" {
		t.Errorf("Key = %v, want [key1]", cmd.Key)
	}
}

func TestApplyFilters_ShowDefaults_ReturnsNonEmpty(t *testing.T) {
	config := map[string]string{"custom_key": "custom_val"}
	defaults := applyFilters(config, &localconfigOpts{showDefaults: true})
	if len(defaults) == 0 {
		t.Error("expected non-empty defaults map")
	}
	if _, ok := defaults["custom_key"]; ok {
		t.Error("defaults should not contain custom_key")
	}
}

func TestApplyFilters_ShowChanged_ExcludesUnchanged(t *testing.T) {
	config := map[string]string{
		"custom_key": "custom_val",
	}

	changed := applyFilters(config, &localconfigOpts{showChanged: true})
	if changed["custom_key"] != "custom_val" {
		t.Errorf("expected custom_key in changed set, got %v", changed)
	}
}

func TestApplyFilters_NoFlags_ReturnsSameMap(t *testing.T) {
	config := map[string]string{"a": "1", "b": "2"}
	result := applyFilters(config, &localconfigOpts{})
	if len(result) != len(config) {
		t.Errorf("expected same map, got %d keys", len(result))
	}
}

func TestFilterKeys_EmptyConfig(t *testing.T) {
	config := map[string]string{}
	filtered := filterKeys(config, &localconfigOpts{keys: []string{"missing"}, quiet: true})
	if len(filtered) != 0 {
		t.Errorf("expected 0 keys from empty config, got %d", len(filtered))
	}
}

func TestFilterKeys_NoKeysFilter_ReturnsAll(t *testing.T) {
	config := map[string]string{"x": "1", "y": "2"}
	filtered := filterKeys(config, &localconfigOpts{})
	if len(filtered) != 2 {
		t.Errorf("expected 2 keys, got %d", len(filtered))
	}
}

func TestWriteOutput_XMLFormat(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	writeOutput(map[string]string{"test_key": "test_val"}, &localconfigOpts{mode: "xml"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if out == "" {
		t.Error("expected non-empty XML output")
	}
}
