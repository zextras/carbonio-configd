// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/config"
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

func parseConfig(t *testing.T, input string) (*config.MtaConfig, error) {
	t.Helper()

	return NewParser(NewLexer(context.Background(), strings.NewReader(""))).
		ParseString(context.Background(), input)
}

// jylibs/mtaconfig.py load(): inside an if block only POSTCONF, POSTCONFD and
// VAR/LOCAL lines are processed; every other line is logged and ignored.
func TestParser_ConditionalBodyHonoursOnlyLegacyDirectives(t *testing.T) {
	t.Parallel()

	input := `SECTION mta
	if VAR enabled
		POSTCONF a literal
		POSTCONF b VAR zimbraB
		POSTCONF c MAPLOCAL zimbraC
		POSTCONF d FILE zmconfigd/d.cf
		POSTCONFD e LOCAL local_e
		VAR zimbraV
		LOCAL local_l
		LDAP key LOCAL ldap_url
		REWRITE conf/a.in conf/a
		RESTART mta
		MAPLOCAL zimbraM
		MAPFILE zimbraF
		PROXYGEN
		bogus words
	fi
`

	cfg, err := parseConfig(t, input)
	require.NoError(t, err)

	section := cfg.Sections["mta"]
	require.Len(t, section.Conditionals, 1)

	cond := section.Conditionals[0]
	assert.Equal(t, map[string]string{
		"a": "literal",
		"b": "VAR:zimbraB",
		"c": "MAPLOCAL zimbraC",
		"d": "FILE zmconfigd/d.cf",
	}, cond.Postconf)
	assert.Equal(t, map[string]string{"e": ""}, cond.Postconfd)
	assert.Equal(t, map[string]string{
		"zimbraB":        "VAR",
		"zmconfigd/d.cf": "FILE",
		"local_e":        "LOCAL",
		"zimbraV":        "VAR",
		"local_l":        "LOCAL",
	}, cond.RequiredVars)

	assert.Empty(t, section.Ldap)
	assert.Empty(t, section.Rewrites)
	assert.Empty(t, section.Restarts)
	assert.Empty(t, section.RequiredVars)
	assert.False(t, section.Proxygen)
}

// Legacy ends the block at the FIRST fi and does not support nesting: the inner
// if header is ignored, its body is folded into the outer block, and the outer
// fi is left over at section level, where it is skipped (legacy would hang).
func TestParser_NestedConditionalIsFlattened(t *testing.T) {
	t.Parallel()

	input := `SECTION one
	IF VAR outer
		POSTCONF outer_key outer_value
		IF VAR inner
			POSTCONF inner_key inner_value
		FI
		POSTCONF after_inner after_value
	FI
	POSTCONF section_key section_value
SECTION two
	POSTCONF two_key two_value
`

	cfg, err := parseConfig(t, input)
	require.NoError(t, err)

	one := cfg.Sections["one"]
	require.Len(t, one.Conditionals, 1)

	cond := one.Conditionals[0]
	assert.Equal(t, "outer", cond.Key)
	assert.Equal(t, map[string]string{
		"outer_key": "outer_value",
		"inner_key": "inner_value",
	}, cond.Postconf)

	// Everything after the first fi is section-level.
	assert.Equal(t, map[string]string{
		"after_inner": "after_value",
		"section_key": "section_value",
	}, one.Postconf)
	assert.Equal(t, map[string]string{"two_key": "two_value"}, cfg.Sections["two"].Postconf)
}

func TestParser_StrayFiIsSkipped(t *testing.T) {
	t.Parallel()

	input := `SECTION one
	POSTCONF a 1
	FI
	POSTCONF b 2
SECTION two
	POSTCONF c 3
`

	cfg, err := parseConfig(t, input)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"a": "1", "b": "2"}, cfg.Sections["one"].Postconf)
	assert.Equal(t, map[string]string{"c": "3"}, cfg.Sections["two"].Postconf)
}

func TestParser_UnterminatedConditionalIsAnError(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, input string }{
		{"end of file", "SECTION a\n\tif VAR x\n\t\tPOSTCONF k v\n"},
		{"next section", "SECTION a\n\tif VAR x\n\t\tPOSTCONF k v\nSECTION b\n\tPOSTCONF c d\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseConfig(t, tt.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing fi")
		})
	}
}

func TestParser_ConditionalMissingLookupKeyIsAnError(t *testing.T) {
	t.Parallel()

	_, err := parseConfig(t, "SECTION a\n\tif VAR x\n\t\tPOSTCONF k LOCALHOST\n\tfi\n")
	require.Error(t, err)

	_, err = parseConfig(t, "SECTION a\n\tif VAR x\n\t\tVAR\n\tfi\n")
	require.Error(t, err)
}
