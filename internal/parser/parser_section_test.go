// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestParser_SimpleSection(t *testing.T) {
	input := `SECTION test
	VAR zimbraMtaMyNetworks
	LOCAL ldap_url
	RESTART mta
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(mtaConfig.Sections) != 1 {
		t.Errorf("Expected 1 section, got %d", len(mtaConfig.Sections))
	}

	section, ok := mtaConfig.Sections["test"]
	if !ok {
		t.Fatal("Section 'test' not found")
	}

	if section.Name != "test" {
		t.Errorf("Expected section name 'test', got '%s'", section.Name)
	}

	if _, ok := section.RequiredVars["zimbraMtaMyNetworks"]; !ok {
		t.Error("VAR zimbraMtaMyNetworks not found in RequiredVars")
	}

	if section.RequiredVars["zimbraMtaMyNetworks"] != "VAR" {
		t.Errorf("Expected type 'VAR', got '%s'", section.RequiredVars["zimbraMtaMyNetworks"])
	}

	if _, ok := section.RequiredVars["ldap_url"]; !ok {
		t.Error("LOCAL ldap_url not found in RequiredVars")
	}

	if section.RequiredVars["ldap_url"] != "LOCAL" {
		t.Errorf("Expected type 'LOCAL', got '%s'", section.RequiredVars["ldap_url"])
	}

	if _, ok := section.Restarts["mta"]; !ok {
		t.Error("RESTART mta not found")
	}
}

func TestParser_SectionWithDependencies(t *testing.T) {
	input := `SECTION antivirus DEPENDS amavis
	VAR zimbraVirusWarnAdmin
	RESTART antivirus mta
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := mtaConfig.Sections["antivirus"]
	if section == nil {
		t.Fatal("Section 'antivirus' not found")
	}

	if _, ok := section.Depends["amavis"]; !ok {
		t.Error("Dependency 'amavis' not found")
	}
}

func TestParser_RewriteDirective(t *testing.T) {
	input := `SECTION test
	REWRITE conf/test.in conf/test.out
	REWRITE conf/secured.in conf/secured.out MODE 0600
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := mtaConfig.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}

	rewrite1, ok := section.Rewrites["conf/test.in"]
	if !ok {
		t.Error("Rewrite 'conf/test.in' not found")
	}
	if rewrite1.Value != "conf/test.out" {
		t.Errorf("Expected value 'conf/test.out', got '%s'", rewrite1.Value)
	}
	if rewrite1.Mode != "" {
		t.Errorf("Expected no mode, got '%s'", rewrite1.Mode)
	}

	rewrite2, ok := section.Rewrites["conf/secured.in"]
	if !ok {
		t.Error("Rewrite 'conf/secured.in' not found")
	}
	if rewrite2.Value != "conf/secured.out" {
		t.Errorf("Expected value 'conf/secured.out', got '%s'", rewrite2.Value)
	}
	if rewrite2.Mode != "0600" {
		t.Errorf("Expected mode '0600', got '%s'", rewrite2.Mode)
	}
}

func TestParser_PostconfDirective(t *testing.T) {
	input := `SECTION test
	POSTCONF myhostname LOCAL zimbra_server_hostname
	POSTCONF message_size_limit VAR zimbraMtaMaxMessageSize
	POSTCONF smtpd_tls_cert_file /opt/zextras/conf/smtpd.crt
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := mtaConfig.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}

	if val := section.Postconf["myhostname"]; val != "LOCAL:zimbra_server_hostname" {
		t.Errorf("Expected 'LOCAL:zimbra_server_hostname', got '%s'", val)
	}

	if val := section.Postconf["message_size_limit"]; val != "VAR:zimbraMtaMaxMessageSize" {
		t.Errorf("Expected 'VAR:zimbraMtaMaxMessageSize', got '%s'", val)
	}

	if val := section.Postconf["smtpd_tls_cert_file"]; val != "/opt/zextras/conf/smtpd.crt" {
		t.Errorf("Expected '/opt/zextras/conf/smtpd.crt', got '%s'", val)
	}
}

func TestParser_MultipleSections(t *testing.T) {
	input := `SECTION section1
	VAR var1
	RESTART service1

SECTION section2 DEPENDS section1
	VAR var2
	RESTART service2
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(mtaConfig.Sections) != 2 {
		t.Errorf("Expected 2 sections, got %d", len(mtaConfig.Sections))
	}

	if _, ok := mtaConfig.Sections["section1"]; !ok {
		t.Error("Section 'section1' not found")
	}

	if _, ok := mtaConfig.Sections["section2"]; !ok {
		t.Error("Section 'section2' not found")
	}

	section2 := mtaConfig.Sections["section2"]
	if _, ok := section2.Depends["section1"]; !ok {
		t.Error("Dependency 'section1' not found in section2")
	}
}

func TestParser_ProxygenDirective(t *testing.T) {
	input := `SECTION proxy
	VAR zimbraReverseProxyResponseHeaders
	PROXYGEN
	RESTART proxy

SECTION ldap
	LOCAL ldap_common_loglevel
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(mtaConfig.Sections) != 2 {
		t.Fatalf("Expected 2 sections, got %d", len(mtaConfig.Sections))
	}

	if _, ok := mtaConfig.Sections["proxy"]; !ok {
		t.Error("Section 'proxy' not found")
	}

	if _, ok := mtaConfig.Sections["ldap"]; !ok {
		t.Error("Section 'ldap' not found")
	}
}

func TestParser_RealConfig(t *testing.T) {
	// Test parsing the actual zmconfigd.cf file
	configPath := "../../conf/zmconfigd.cf"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skip("Config file not available, skipping")
	}

	// Create a dummy lexer - Parse() will replace it with actual content
	p := &parser{errors: []error{}}
	mtaConfig, err := p.Parse(context.Background(), configPath)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(mtaConfig.Sections) == 0 {
		t.Fatal("No sections parsed")
	}

	t.Logf("Successfully parsed %d sections", len(mtaConfig.Sections))

	// Count conditionals
	totalConds := 0
	for _, section := range mtaConfig.Sections {
		totalConds += len(section.Conditionals)
	}
	t.Logf("Total conditionals: %d", totalConds)

	// Check that mta section has conditionals
	if section, ok := mtaConfig.Sections["mta"]; ok {
		t.Logf("Section 'mta' has %d conditionals", len(section.Conditionals))

		if len(section.Conditionals) == 0 {
			t.Error("Expected mta section to have conditionals")
		}

		// Show first few conditionals
		for i, cond := range section.Conditionals {
			if i >= 5 {
				break
			}
			negStr := ""
			if cond.Negated {
				negStr = "!"
			}
			t.Logf("  Conditional %d: if %s %s%s (postconf: %d, postconfd: %d, ldap: %d)",
				i+1, cond.Type, negStr, cond.Key,
				len(cond.Postconf), len(cond.Postconfd), len(cond.Ldap))
		}
	} else {
		t.Error("Section 'mta' not found")
	}
}

func TestParser_MapfileDirective(t *testing.T) {
	input := `SECTION test
	MAPFILE ldap_maps
	RESTART mta
`
	cfg, err := new(parser).ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := cfg.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}

	val, ok := section.RequiredVars["ldap_maps"]
	if !ok {
		t.Error("MAPFILE ldap_maps not found in RequiredVars")
	}
	if val != "MAPFILE" {
		t.Errorf("Expected type 'MAPFILE', got '%s'", val)
	}
}

func TestParser_MaplocalDirective(t *testing.T) {
	input := `SECTION test
	MAPLOCAL local_maps
	RESTART mta
`
	cfg, err := new(parser).ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := cfg.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}

	val, ok := section.RequiredVars["local_maps"]
	if !ok {
		t.Error("MAPLOCAL local_maps not found in RequiredVars")
	}
	if val != "MAPLOCAL" {
		t.Errorf("Expected type 'MAPLOCAL', got '%s'", val)
	}
}

func TestParser_PostconfdDirective(t *testing.T) {
	input := `SECTION test
	POSTCONFD smtpd_milters
	POSTCONFD content_filter
`
	cfg, err := new(parser).ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := cfg.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}

	if _, ok := section.Postconfd["smtpd_milters"]; !ok {
		t.Error("POSTCONFD smtpd_milters not found")
	}
	if _, ok := section.Postconfd["content_filter"]; !ok {
		t.Error("POSTCONFD content_filter not found")
	}
}

func TestParser_LdapDirective(t *testing.T) {
	input := `SECTION ldap
	LDAP server_host LOCAL ldap_url
	LDAP dh_param MAPLOCAL zimbraSSLDHParam
	LDAP bind_dn VAR zimbraLdapUserDn
`
	cfg, err := new(parser).ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := cfg.Sections["ldap"]
	if section == nil {
		t.Fatal("Section 'ldap' not found")
	}

	if val := section.Ldap["server_host"]; val != "LOCAL:ldap_url" {
		t.Errorf("Expected 'LOCAL:ldap_url', got '%s'", val)
	}
	if val := section.Ldap["dh_param"]; val != "MAPLOCAL:zimbraSSLDHParam" {
		t.Errorf("Expected 'MAPLOCAL:zimbraSSLDHParam', got '%s'", val)
	}
	// Non-LOCAL/MAPLOCAL LDAP directives are dropped, mirroring
	// jylibs/mtaconfig.py parseLdap (only LOCAL|MAPLOCAL recorded).
	if val, ok := section.Ldap["bind_dn"]; ok {
		t.Errorf("VAR-typed LDAP directive should be dropped, got %q", val)
	}
}

func TestParser_SkipToNextSection_ErrorRecovery(t *testing.T) {
	// A non-SECTION token at top level triggers skipToNextSection() error recovery.
	// The parser skips the garbage and continues to parse subsequent sections.
	// Note: the garbage section itself is not added (parse failed before completion).
	input := `GARBAGE here
SECTION second
	VAR zimbraSecond
`
	p := &parser{errors: []error{}}
	cfg, err := p.ParseString(context.Background(), input)
	// Errors are expected (GARBAGE line)
	if err == nil {
		t.Error("Expected parse errors, got nil")
	}

	// Despite errors, second section should be recovered
	if _, ok := cfg.Sections["second"]; !ok {
		t.Error("Section 'second' should have been recovered after error")
	}
}

func TestParser_Parse_TempFile(t *testing.T) {
	content := `SECTION mta
	VAR zimbraMtaMyNetworks
	RESTART mta
`
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "zmconfigd-*.cf")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	f.Close()

	p := &parser{errors: []error{}}
	cfg, err := p.Parse(context.Background(), f.Name())
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(cfg.Sections) != 1 {
		t.Errorf("Expected 1 section, got %d", len(cfg.Sections))
	}

	section := cfg.Sections["mta"]
	if section == nil {
		t.Fatal("Section 'mta' not found")
	}

	if _, ok := section.RequiredVars["zimbraMtaMyNetworks"]; !ok {
		t.Error("VAR zimbraMtaMyNetworks not found")
	}
}

func TestParser_Parse_FileNotFound(t *testing.T) {
	p := &parser{errors: []error{}}
	_, err := p.Parse(context.Background(), "/nonexistent/path/zmconfigd.cf")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestParser_ParseSection_MissingSectionName(t *testing.T) {
	// A SECTION keyword followed immediately by a newline (no identifier).
	input := "SECTION\nVAR foo\n"
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error for missing section name")
	}
}

func TestParser_ParseSection_MissingNewlineAfterHeader(t *testing.T) {
	// "SECTION test VAR foo" — after the name the next token is VAR, not a newline.
	input := "SECTION test VAR foo\n"
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error for missing newline after section header")
	}
}

// TestParser_ParseSection_EmptyLinesInBody exercises the TokenNewline continue
// branch (L154-157) where empty lines between directives inside a section body
// are skipped.
func TestParser_ParseSection_EmptyLinesInBody(t *testing.T) {
	input := "SECTION test\n\tVAR key1 val1\n\n\tVAR key2 val2\n"
	p := &parser{errors: []error{}}
	cfg, err := p.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("Expected config, got nil")
	}
	section, ok := cfg.Sections["test"]
	if !ok {
		t.Fatal("Expected section 'test'")
	}
	if len(section.RequiredVars) != 2 {
		t.Errorf("Expected 2 required vars, got %d", len(section.RequiredVars))
	}
}
