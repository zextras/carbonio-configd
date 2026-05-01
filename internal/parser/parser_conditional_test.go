// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"context"
	"strings"
	"testing"
)

func TestParser_SimpleConditional(t *testing.T) {
	input := `SECTION mta
	if SERVICE antivirus
		POSTCONF content_filter FILE zmconfigd/postfix_content_filter.cf
	fi
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section, ok := mtaConfig.Sections["mta"]
	if !ok {
		t.Fatal("Section 'mta' not found")
	}

	if len(section.Conditionals) != 1 {
		t.Fatalf("Expected 1 conditional, got %d", len(section.Conditionals))
	}

	cond := section.Conditionals[0]
	if cond.Type != "SERVICE" {
		t.Errorf("Expected condition type 'SERVICE', got '%s'", cond.Type)
	}

	if cond.Key != "antivirus" {
		t.Errorf("Expected condition key 'antivirus', got '%s'", cond.Key)
	}

	if cond.Negated {
		t.Error("Expected condition not to be negated")
	}

	if len(cond.Postconf) != 1 {
		t.Fatalf("Expected 1 POSTCONF directive, got %d", len(cond.Postconf))
	}

	if val, ok := cond.Postconf["content_filter"]; !ok {
		t.Error("POSTCONF 'content_filter' not found")
	} else if val != "FILE zmconfigd/postfix_content_filter.cf" {
		t.Errorf("Expected 'FILE zmconfigd/postfix_content_filter.cf', got '%s'", val)
	}
}

func TestParser_NegatedConditional(t *testing.T) {
	input := `SECTION mta
	if VAR !zimbraMtaEnableSmtpdPolicyd
		POSTCONF smtpd_end_of_data_restrictions permit
	fi
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section, ok := mtaConfig.Sections["mta"]
	if !ok {
		t.Fatal("Section 'mta' not found")
	}

	if len(section.Conditionals) != 1 {
		t.Fatalf("Expected 1 conditional, got %d", len(section.Conditionals))
	}

	cond := section.Conditionals[0]
	if cond.Type != "VAR" {
		t.Errorf("Expected condition type 'VAR', got '%s'", cond.Type)
	}

	if cond.Key != "zimbraMtaEnableSmtpdPolicyd" {
		t.Errorf("Expected condition key 'zimbraMtaEnableSmtpdPolicyd', got '%s'", cond.Key)
	}

	if !cond.Negated {
		t.Error("Expected condition to be negated")
	}

	if len(cond.Postconf) != 1 {
		t.Fatalf("Expected 1 POSTCONF directive, got %d", len(cond.Postconf))
	}
}

func TestParser_MultipleConditionals(t *testing.T) {
	input := `SECTION mta
	if VAR zimbraMtaMyNetworks
		POSTCONF mynetworks VAR zimbraMtaMyNetworks
	fi
	if VAR zimbraMtaMyOrigin
		POSTCONF myorigin VAR zimbraMtaMyOrigin
	fi
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	mtaConfig, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section, ok := mtaConfig.Sections["mta"]
	if !ok {
		t.Fatal("Section 'mta' not found")
	}

	if len(section.Conditionals) != 2 {
		t.Fatalf("Expected 2 conditionals, got %d", len(section.Conditionals))
	}

	// Check first conditional
	cond1 := section.Conditionals[0]
	if cond1.Type != "VAR" || cond1.Key != "zimbraMtaMyNetworks" {
		t.Errorf("First conditional incorrect: type=%s, key=%s", cond1.Type, cond1.Key)
	}

	// Check second conditional
	cond2 := section.Conditionals[1]
	if cond2.Type != "VAR" || cond2.Key != "zimbraMtaMyOrigin" {
		t.Errorf("Second conditional incorrect: type=%s, key=%s", cond2.Type, cond2.Key)
	}
}

func TestParser_NestedConditionals(t *testing.T) {
	input := `
SECTION test
	IF VAR outer
		POSTCONF outer_key outer_value
		IF VAR inner
			POSTCONF inner_key inner_value
		FI
	FI
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	cfg, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	section := cfg.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}

	if len(section.Conditionals) != 1 {
		t.Fatalf("Expected 1 conditional, got %d", len(section.Conditionals))
	}

	outer := section.Conditionals[0]
	if outer.Key != "outer" {
		t.Errorf("Expected outer key 'outer', got '%s'", outer.Key)
	}

	if val, ok := outer.Postconf["outer_key"]; !ok || val != "outer_value" {
		t.Errorf("Expected outer_key=outer_value, got %v", val)
	}

	if len(outer.Nested) != 1 {
		t.Fatalf("Expected 1 nested conditional, got %d", len(outer.Nested))
	}

	inner := outer.Nested[0]
	if inner.Key != "inner" {
		t.Errorf("Expected inner key 'inner', got '%s'", inner.Key)
	}

	if val, ok := inner.Postconf["inner_key"]; !ok || val != "inner_value" {
		t.Errorf("Expected inner_key=inner_value, got %v", val)
	}
}

func TestParser_MultiLevelNestedConditionals(t *testing.T) {
	input := `
SECTION test
	IF VAR level1
		POSTCONF l1_key l1_value
		IF VAR level2
			POSTCONF l2_key l2_value
			IF VAR level3
				POSTCONF l3_key l3_value
			FI
		FI
	FI
`
	lexer := NewLexer(context.Background(), strings.NewReader(input))
	parser := NewParser(lexer)

	cfg, err := parser.ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	section := cfg.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}

	if len(section.Conditionals) != 1 {
		t.Fatalf("Expected 1 conditional, got %d", len(section.Conditionals))
	}

	level1 := section.Conditionals[0]
	if level1.Key != "level1" {
		t.Errorf("Expected level1 key, got '%s'", level1.Key)
	}

	if len(level1.Nested) != 1 {
		t.Fatalf("Expected 1 nested conditional at level1, got %d", len(level1.Nested))
	}

	level2 := level1.Nested[0]
	if level2.Key != "level2" {
		t.Errorf("Expected level2 key, got '%s'", level2.Key)
	}

	if len(level2.Nested) != 1 {
		t.Fatalf("Expected 1 nested conditional at level2, got %d", len(level2.Nested))
	}

	level3 := level2.Nested[0]
	if level3.Key != "level3" {
		t.Errorf("Expected level3 key, got '%s'", level3.Key)
	}

	if val, ok := level3.Postconf["l3_key"]; !ok || val != "l3_value" {
		t.Errorf("Expected l3_key=l3_value, got %v", val)
	}
}

func TestParser_ConditionalPostconfd(t *testing.T) {
	input := `SECTION mta
	if VAR zimbraMtaEnableSmtpdPolicyd
		POSTCONFD smtpd_end_of_data_restrictions
	fi
`
	cfg, err := new(parser).ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := cfg.Sections["mta"]
	if section == nil {
		t.Fatal("Section 'mta' not found")
	}

	if len(section.Conditionals) != 1 {
		t.Fatalf("Expected 1 conditional, got %d", len(section.Conditionals))
	}

	cond := section.Conditionals[0]
	if _, ok := cond.Postconfd["smtpd_end_of_data_restrictions"]; !ok {
		t.Error("POSTCONFD smtpd_end_of_data_restrictions not found in conditional")
	}
}

func TestParser_ConditionalLdap(t *testing.T) {
	input := `SECTION ldap
	if VAR zimbraLdapEnabled
		LDAP server_host LOCAL ldap_url
		LDAP timeout VAR zimbraLdapTimeout
	fi
`
	cfg, err := new(parser).ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	section := cfg.Sections["ldap"]
	if section == nil {
		t.Fatal("Section 'ldap' not found")
	}

	if len(section.Conditionals) != 1 {
		t.Fatalf("Expected 1 conditional, got %d", len(section.Conditionals))
	}

	cond := section.Conditionals[0]
	if val := cond.Ldap["server_host"]; val != "LOCAL:ldap_url" {
		t.Errorf("Expected 'LOCAL:ldap_url', got '%s'", val)
	}
	if val := cond.Ldap["timeout"]; val != "VAR:zimbraLdapTimeout" {
		t.Errorf("Expected 'VAR:zimbraLdapTimeout', got '%s'", val)
	}
}

func TestParser_ConditionalPostconf_ErrorBranch(t *testing.T) {
	input := `SECTION test
	IF VAR someKey
		POSTCONF
	FI
`
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error for POSTCONF with no key inside conditional")
	}
}

func TestParser_ConditionalPostconfd_ErrorBranch(t *testing.T) {
	input := `SECTION test
	IF VAR someKey
		POSTCONFD
	FI
`
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error for POSTCONFD with no key inside conditional")
	}
}

func TestParser_ConditionalLdap_ErrorBranch(t *testing.T) {
	input := `SECTION ldap
	IF VAR someKey
		LDAP
	FI
`
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error for LDAP with no key inside conditional")
	}
}

func TestParser_ParseConditionalHeader_BadToken(t *testing.T) {
	input := `SECTION test
	IF LOCAL badkey
		POSTCONF foo bar
	FI
`
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error for bad token after IF")
	}
}

func TestParser_ParseConditional_ErrorPropagation(t *testing.T) {
	// IF followed by an invalid condition type causes parseConditionalBlock to fail.
	input := `SECTION test
	IF MAPFILE badkey
		POSTCONF foo bar
	FI
`
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error from bad conditional type")
	}
}

func TestParser_ParseConditionalHeader_MissingKey(t *testing.T) {
	// IF VAR followed immediately by a newline — no identifier for the key.
	input := `SECTION test
	IF VAR
		POSTCONF foo bar
	FI
`
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error for IF VAR with no key")
	}
}

func TestParser_ParseNestedConditional_ErrorPropagation(t *testing.T) {
	// Outer IF is valid; inner IF has an invalid condition type (LOCAL).
	input := `SECTION test
	IF VAR outer
		IF LOCAL badkey
			POSTCONF foo bar
		FI
	FI
`
	p := &parser{errors: []error{}}
	_, err := p.ParseString(context.Background(), input)
	if err == nil {
		t.Error("Expected error from invalid nested conditional type")
	}
}

func TestParser_ConditionalToken_FiDepthNotZero(t *testing.T) {
	// Three levels of nesting: outer, middle, inner.
	// When the inner FI fires depth goes from 3→2, middle FI from 2→1, outer FI 1→0.
	input := `SECTION test
	IF VAR level1
		IF VAR level2
			IF VAR level3
				POSTCONF deep_key deep_value
			FI
		FI
	FI
`
	cfg, err := new(parser).ParseString(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	section := cfg.Sections["test"]
	if section == nil {
		t.Fatal("Section 'test' not found")
	}
	if len(section.Conditionals) != 1 {
		t.Fatalf("Expected 1 top-level conditional, got %d", len(section.Conditionals))
	}
	// Walk down to depth-3 conditional.
	l1 := section.Conditionals[0]
	if len(l1.Nested) != 1 {
		t.Fatalf("Expected 1 nested at l1, got %d", len(l1.Nested))
	}
	l2 := l1.Nested[0]
	if len(l2.Nested) != 1 {
		t.Fatalf("Expected 1 nested at l2, got %d", len(l2.Nested))
	}
	l3 := l2.Nested[0]
	if val, ok := l3.Postconf["deep_key"]; !ok || val != "deep_value" {
		t.Errorf("Expected deep_key=deep_value, got %v", val)
	}
}
