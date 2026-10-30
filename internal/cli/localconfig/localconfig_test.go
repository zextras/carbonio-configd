// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

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
			key, val, err := resolveEditKeyValue(tt.arg, tt.random)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
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
	key, val, err := resolveEditKeyValue("my_password", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "my_password" {
		t.Errorf("key = %q, want %q", key, "my_password")
	}
	if len(val) == 0 {
		t.Error("expected non-empty random password")
	}
}

func TestSelectValues_EmptyConfigMissingKey(t *testing.T) {
	filtered := (&localconfigOpts{keys: []string{"missing"}, quiet: true}).selectValues(map[string]string{})
	if len(filtered) != 0 {
		t.Errorf("expected 0 keys from empty config, got %d", len(filtered))
	}
}

func TestWriteOutput_XMLFormat(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := writeOutput(map[string]string{"test_key": "test_val"}, &localconfigOpts{mode: "xml"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if out == "" {
		t.Error("expected non-empty XML output")
	}
}

func TestSelectValues_ShowDefaults_ReturnsNonEmpty(t *testing.T) {
	config := map[string]string{"custom_key": "custom_val"}

	defaults := (&localconfigOpts{showDefaults: true}).selectValues(config)
	if len(defaults) == 0 {
		t.Error("expected non-empty defaults map")
	}

	if _, ok := defaults["custom_key"]; ok {
		t.Error("defaults should not contain custom_key")
	}
}

func TestSelectValues_ShowChanged_IncludesConfiguredUnknown(t *testing.T) {
	config := map[string]string{"custom_key": "custom_val"}

	changed := (&localconfigOpts{showChanged: true}).selectValues(config)
	if changed["custom_key"] != "custom_val" {
		t.Errorf("expected custom_key in changed set, got %v", changed)
	}
}

func TestSelectValues_NoFlags_ReturnsConfiguredOverDefaults(t *testing.T) {
	config := map[string]string{"a": "1", "b": "2"}

	result := (&localconfigOpts{}).selectValues(config)
	if result["a"] != "1" || result["b"] != "2" || len(result) < len(config) {
		t.Errorf("unexpected result: %d keys", len(result))
	}
}
