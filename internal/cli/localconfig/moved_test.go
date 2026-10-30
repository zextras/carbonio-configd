// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lcfg "github.com/zextras/carbonio-configd/internal/localconfig"
)

func TestProtectedKeys_MatchLegacyProtectedKeys(t *testing.T) {
	// Keys marked .protect() in legacy LC.java: editing requires -f.
	for _, key := range []string{
		"zimbra_home",
		"zimbra_mysql_password",
		"zimbra_ldap_password",
		"ldap_root_password",
		"mysql_root_password",
		"zimbra_vami_password",
	} {
		if !lcfg.IsProtected(key) {
			t.Errorf("%s: legacy requires -f, want dangerous", key)
		}
	}

	// Editable without -f in legacy; carbonio-install-ansible sets ldap_url
	// with a plain "zmlocalconfig -e".
	for _, key := range []string{
		"ldap_url",
		"ldap_master_url",
		"zimbra_server_hostname",
		"zimbra_ldap_userdn",
		"zimbra_require_interprocess_security",
	} {
		if lcfg.IsProtected(key) {
			t.Errorf("%s: legacy allows editing without -f, want not dangerous", key)
		}
	}
}

func TestHandleEdit_LDAPURLWithoutForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localconfig.xml")
	if err := os.WriteFile(path, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<localconfig>
    <key name="ldap_url"><value>ldap://old:389</value></key>
</localconfig>`), 0o600); err != nil {
		t.Fatal(err)
	}

	want := "ldap://replica:389 ldap://master:389"
	if err := (&localconfigOpts{configPath: path, keys: []string{"ldap_url=" + want}}).runEdit(); err != nil {
		t.Fatal(err)
	}

	cfg, err := lcfg.LoadLocalConfigFromFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg["ldap_url"]; got != want {
		t.Errorf("ldap_url = %q, want %q", got, want)
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

func TestResolveEditKeyValue_KeyValue(t *testing.T) {
	key, val, err := resolveEditKeyValue("mykey=myvalue", false)
	if err != nil {
		t.Fatal(err)
	}
	if key != "mykey" {
		t.Errorf("expected key 'mykey', got %q", key)
	}
	if val != "myvalue" {
		t.Errorf("expected value 'myvalue', got %q", val)
	}
}

func TestResolveEditKeyValue_ValueWithEquals(t *testing.T) {
	// Value contains an '=' sign — only first '=' is the separator
	key, val, err := resolveEditKeyValue("k=v=extra", false)
	if err != nil {
		t.Fatal(err)
	}
	if key != "k" {
		t.Errorf("expected key 'k', got %q", key)
	}
	if val != "v=extra" {
		t.Errorf("expected value 'v=extra', got %q", val)
	}
}

func TestResolveEditKeyValue_EmptyValue(t *testing.T) {
	key, val, err := resolveEditKeyValue("mykey=", false)
	if err != nil {
		t.Fatal(err)
	}
	if key != "mykey" {
		t.Errorf("expected key 'mykey', got %q", key)
	}
	if val != "" {
		t.Errorf("expected empty value, got %q", val)
	}
}

func TestWriteOutput_Plain(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if err := writeOutput(map[string]string{"key1": "val1"}, &localconfigOpts{mode: "plain"}); err != nil {
		t.Fatal(err)
	}

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

	if err := writeOutput(map[string]string{"mykey": "myval"}, &localconfigOpts{mode: "shell"}); err != nil {
		t.Fatal(err)
	}

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

	if err := writeOutput(map[string]string{"expkey": "expval"}, &localconfigOpts{mode: modeExport}); err != nil {
		t.Fatal(err)
	}

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

	if err := writeOutput(map[string]string{"nk": "nkval"}, &localconfigOpts{mode: "nokey", keys: []string{"nk"}}); err != nil {
		t.Fatal(err)
	}

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

	if err := writeOutput(map[string]string{"xmlkey": "xmlval"}, &localconfigOpts{mode: "xml"}); err != nil {
		t.Fatal(err)
	}

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()
	if !strings.Contains(out, "xmlkey") {
		t.Errorf("expected key in xml output, got %q", out)
	}
}

func TestSelectValues_MissingKeyQuiet(t *testing.T) {
	configured := map[string]string{"a": "1"}

	// quiet=true: missing keys must not warn but must be excluded.
	var stderr bytes.Buffer

	opts := &localconfigOpts{keys: []string{"a", "missing"}, quiet: true, stderr: &stderr}

	filtered := opts.selectValues(configured)
	if len(filtered) != 1 || filtered["a"] != "1" {
		t.Fatalf("expected only a=1, got %v", filtered)
	}

	if stderr.Len() != 0 {
		t.Errorf("quiet mode must not warn, got %q", stderr.String())
	}
}

func TestSelectValues_MissingKeyWarns(t *testing.T) {
	var stderr bytes.Buffer

	opts := &localconfigOpts{keys: []string{"missing"}, stderr: &stderr}

	if filtered := opts.selectValues(map[string]string{"a": "1"}); len(filtered) != 0 {
		t.Errorf("expected 0 keys for missing key, got %d", len(filtered))
	}

	if want := "Warning: null valued key 'missing'\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestSelectValues(t *testing.T) {
	configured := map[string]string{
		"custom": "override",
		"known":  "value",
	}

	defaults := (&localconfigOpts{showDefaults: true}).selectValues(configured)
	if len(defaults) == 0 {
		t.Fatal("expected defaults to be returned")
	}

	if _, ok := defaults["custom"]; ok {
		t.Fatal("defaults must not contain configured-only keys")
	}

	changed := (&localconfigOpts{showChanged: true}).selectValues(configured)
	if changed["custom"] != "override" {
		t.Fatalf("expected changed config to preserve custom key, got %q", changed["custom"])
	}

	plain := (&localconfigOpts{}).selectValues(configured)
	if plain["known"] != "value" || plain["zimbra_home"] == "" {
		t.Fatalf("expected configured keys over defaults, got known=%q zimbra_home=%q",
			plain["known"], plain["zimbra_home"])
	}
}

func TestSelectValues_KeyFilter(t *testing.T) {
	configured := map[string]string{"a": "1", "b": "2"}

	filtered := (&localconfigOpts{keys: []string{"b", "missing"}, quiet: true}).selectValues(configured)
	if len(filtered) != 1 || filtered["b"] != "2" {
		t.Fatalf("expected only b=2, got %v", filtered)
	}
}
