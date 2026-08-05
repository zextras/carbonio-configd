// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/testutil"
)

func TestTransformVarSubstitution(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "VAR substitution",
			input:    "bind_address = %%VAR:zimbraLocalBindAddress%%",
			expected: "bind_address = 127.0.0.1\n",
		},
		{
			name:     "LOCAL substitution",
			input:    "ldap_url = %%LOCAL:ldap_url%%",
			expected: "ldap_url = ldap://ldap1.example.com:389 ldap://ldap2.example.com:389\n",
		},
		{
			name:     "SERVICE substitution - enabled",
			input:    "service_enabled = %%SERVICE:antispam%%",
			expected: "service_enabled = TRUE\n",
		},
		{
			name:     "SERVICE substitution - disabled",
			input:    "service_enabled = %%SERVICE:webmail%%",
			expected: "service_enabled = FALSE\n",
		},
		{
			name:     "Multiple VAR substitutions",
			input:    "server = %%VAR:zimbraServerHostname%% bind = %%VAR:zimbraLocalBindAddress%%",
			expected: "server = mail.example.com bind = 127.0.0.1\n",
		},
		{
			name:     "Mixed VAR and LOCAL",
			input:    "server = %%VAR:zimbraServerHostname%% local = %%LOCAL:zimbra_server_hostname%%",
			expected: "server = mail.example.com local = mail.local.example.com\n",
		},
		{
			name:     "No substitution needed",
			input:    "simple line without variables",
			expected: "simple line without variables",
		},
	}

	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Transform() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTransformLocalConfigSubstitution(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Basic @@ substitution",
			input:    "bind_address = @@mysql_bind_address@@",
			expected: "bind_address = 127.0.0.1\n",
		},
		{
			name:     "SPLIT function",
			input:    "first_ldap = @@SPLIT ldap_url@@",
			expected: "first_ldap = ldap://ldap1.example.com:389\n",
		},
		{
			name:     "Multiple @@ substitutions",
			input:    "server = @@zimbra_server_hostname@@ bind = @@mysql_bind_address@@",
			expected: "server = mail.local.example.com bind = 127.0.0.1\n",
		},
	}

	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Transform() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTransformEdgeCases(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"emptyValue":    "",
			"spacedValue":   "  value with spaces  ",
			"multilineText": "line1\nline2\nline3",
		},
		"LOCAL": {},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Empty value substitution",
			input:    "key = %%VAR:emptyValue%%",
			expected: "key = \n",
		},
		{
			name:     "Spaced value substitution",
			input:    "key = %%VAR:spacedValue%%",
			expected: "key =   value with spaces  \n",
		},
		{
			name:     "Nonexistent key",
			input:    "key = %%VAR:nonexistentKey%%",
			expected: "key = \n",
		},
		{
			name:     "Line with no special characters",
			input:    "plain text line",
			expected: "plain text line",
		},
		{
			name:     "Empty line",
			input:    "",
			expected: "",
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mockLookup, st)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Transform() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestTransformPlainVariableSubstitution(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"varKey": "var_value",
		},
		"LOCAL": {
			"localKey": "local_value",
			"varKey":   "local_var_value", // Same key in both
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain variable - VAR takes precedence",
			input:    "value = %%varKey%%",
			expected: "value = var_value\n", // VAR is checked first
		},
		{
			name:     "Plain variable - fallback to LOCAL",
			input:    "value = %%localKey%%",
			expected: "value = local_value\n", // Only in LOCAL
		},
		{
			name:     "Plain variable - not found in either",
			input:    "value = %%unknownKey%%",
			expected: "value = \n",
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mockLookup, st)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Transform() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestTransformMultiValueStaysOnOneLine covers CO-4100: multi-valued LDAP
// attributes are newline-joined by ldap.Client, and emitting those newlines in
// the middle of a line splits the directive. The real-world break was
// "mech_list: %%zimbraMtaSaslSmtpdMechList%%" rendering as "mech_list: LOGIN\nPLAIN",
// which made postfix smtpd fail SASL initialization. A directive that occupies the
// whole line must keep its newlines, because zimbraMtaMyNetworksPerLine and the
// *XML keys are newline-joined on purpose to render multi-line blocks.
func TestTransformMultiValueStaysOnOneLine(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraMtaSaslSmtpdMechList": "LOGIN\nPLAIN",
			"zimbraMtaMyNetworksPerLine": "127.0.0.0/8\n10.0.0.0/8",
			"zimbraServiceEnabled":       "mta\nmailbox",
		},
		"LOCAL": {},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bare multi-value inline is flattened to spaces",
			input:    "mech_list: %%zimbraMtaSaslSmtpdMechList%%",
			expected: "mech_list: LOGIN PLAIN\n",
		},
		{
			name:     "typed multi-value inline is flattened to spaces",
			input:    "mech_list: %%VAR:zimbraMtaSaslSmtpdMechList%%",
			expected: "mech_list: LOGIN PLAIN\n",
		},
		{
			name:     "whole-line directive keeps its newlines",
			input:    "%%zimbraMtaMyNetworksPerLine%%",
			expected: "127.0.0.0/8\n10.0.0.0/8\n",
		},
		{
			name:     "indented whole-line directive keeps its newlines",
			input:    "\t%%VAR:zimbraMtaMyNetworksPerLine%%",
			expected: "\t127.0.0.0/8\n10.0.0.0/8\n",
		},
		{
			name:     "multi-value embedded in contains directive is flattened",
			input:    "%%contains VAR:zimbraServiceEnabled mta^enabled %%VAR:zimbraMtaSaslSmtpdMechList%%^disabled%%",
			expected: "enabled LOGIN PLAIN\n",
		},
	}

	st := &state.State{}
	transformer := NewTransformer(mockLookup, st)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Transform() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestLocalConfigSPLITFunction(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"LOCAL": {
			"multiword":  "first second third",
			"ldap_urls":  "ldap://host1:389 ldap://host2:389 ldap://host3:389",
			"singleword": "single",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "SPLIT extracts first word",
			input:    "@@SPLIT multiword@@",
			expected: "first\n",
		},
		{
			name:     "SPLIT with single word",
			input:    "@@SPLIT singleword@@",
			expected: "single\n",
		},
		{
			name:     "PERDITION_LDAP_SPLIT extracts hostnames",
			input:    "@@PERDITION_LDAP_SPLIT ldap_urls@@",
			expected: "ldap://host1:389 host1 host2 host3\n",
		},
		{
			name:     "Missing LOCAL key",
			input:    "@@missing@@",
			expected: "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// === VARIABLE SUBSTITUTION EDGE CASES ===

// TestSpecialCharactersInValues tests handling of special characters
func TestSpecialCharactersInValues(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"special":   "value with & symbols !@#$%",
			"newline":   "line1\nline2",
			"tabs":      "tab\tseparated",
			"quotes":    "value with \"quotes\"",
			"backslash": "path\\to\\file",
			"unicode":   "unicode: 你好",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Special characters",
			input:    "config = %%VAR:special%%",
			expected: "config = value with & symbols !@#$%\n",
		},
		{
			name:     "Newline in value",
			input:    "%%VAR:newline%%",
			expected: "line1\nline2\n",
		},
		{
			name:     "Tabs in value",
			input:    "%%VAR:tabs%%",
			expected: "tab\tseparated\n",
		},
		{
			name:     "Quotes in value",
			input:    "%%VAR:quotes%%",
			expected: "value with \"quotes\"\n",
		},
		{
			name:     "Backslashes",
			input:    "%%VAR:backslash%%",
			expected: "path\\to\\file\n",
		},
		{
			name:     "Unicode characters",
			input:    "%%VAR:unicode%%",
			expected: "unicode: 你好\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestNumericBoundaryValues tests edge cases for numeric operations
func TestNumericBoundaryValues(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zero":     "0",
			"hundred":  "100",
			"negative": "-50",
			"decimal":  "50.5",
			"large":    "999999",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Range with 0%",
			input:    "%%range VAR:zero 0 100%%",
			expected: "0\n",
		},
		{
			name:     "Range with 100%",
			input:    "%%range VAR:hundred 0 100%%",
			expected: "100\n",
		},
		{
			name:     "Range with negative value",
			input:    "%%range VAR:negative 0 100%%",
			expected: "-50\n", // Negative values are passed through the formula
		},
		{
			name:     "Range with decimal fails Atoi",
			input:    "%%range VAR:decimal 0 100%%",
			expected: "\n", // Decimal values fail integer conversion
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := transformer.Transform(ctx, tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestXformLocalConfig_PerditionLdapSplit_NoURLs exercises the no-hostnames path.
func TestXformLocalConfig_PerditionLdapSplit_NoURLs(t *testing.T) {
	ctx := context.Background()
	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"LOCAL": {
			"plain_server": "mailhost.example.com",
		},
	})
	tr := NewTransformer(mock, nil)

	got := tr.Transform(ctx, "@@PERDITION_LDAP_SPLIT plain_server@@")
	want := "mailhost.example.com\n"
	if got != want {
		t.Errorf("PERDITION_LDAP_SPLIT no urls: got %q, want %q", got, want)
	}
}

// TestXformConfigVariable_InvalidFormat tests branches not reachable via Transform
// because the regex only matches valid types. We call the method directly.
func TestXformConfigVariable_InvalidFormat(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(newMockLookup(), nil)

	got := tr.xformConfigVariable(ctx, "%%NOCODON%%")
	if got != "" {
		t.Errorf("expected empty string for missing colon, got %q", got)
	}
}

func TestXformConfigVariable_InvalidCfgType(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(newMockLookup(), nil)

	got := tr.xformConfigVariable(ctx, "%%BOGUS:somekey%%")
	if got != "" {
		t.Errorf("expected empty string for invalid config type, got %q", got)
	}
}

func TestXformConfigVariable_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.xformConfigVariable(ctx, "%%VAR:missingkey%%")
	if got != "" {
		t.Errorf("expected empty string on lookup error, got %q", got)
	}
}

// TestLookupBooleanValue_LookupError exercises the error branch in lookupBooleanValue.
func TestLookupBooleanValue_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.lookupBooleanValue(ctx, "VAR", "missingkey")
	if got != "" {
		t.Errorf("expected empty string on lookup error, got %q", got)
	}
}
