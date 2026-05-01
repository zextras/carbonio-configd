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

func TestTransformCommentDirective(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Comment when TRUE",
			input:    "%%comment VAR:zimbraLogToSyslog%%appender.SYSLOG.type = Syslog",
			expected: "#appender.SYSLOG.type = Syslog\n",
		},
		{
			name:     "Uncomment when TRUE",
			input:    "%%uncomment VAR:zimbraLogToSyslog%%appender.SYSLOG.type = Syslog",
			expected: "appender.SYSLOG.type = Syslog\n",
		},
		{
			name:     "Comment SERVICE when enabled",
			input:    "%%comment SERVICE:antispam%% @bypass_spam_checks_maps = (1);",
			expected: "# @bypass_spam_checks_maps = (1);\n",
		},
		{
			name:     "Uncomment SERVICE when disabled",
			input:    "%%uncomment SERVICE:webmail%%webmail_enabled = true",
			expected: "#webmail_enabled = true\n",
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

func TestTransformListDirective(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "List with pipe separator",
			input:    "blocked = %%list VAR:zimbraMtaBlockedExtension |%%",
			expected: "blocked = exe|bat|com|pif|scr|vbs\n",
		},
		{
			name:     "List with comma separator",
			input:    "blocked = %%list VAR:zimbraMtaBlockedExtension ,%%",
			expected: "blocked = exe,bat,com,pif,scr,vbs\n",
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

func TestTransformBinaryDirective(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Binary TRUE to 1",
			input:    "enabled = %%binary VAR:zimbraLogToSyslog%%",
			expected: "enabled = 1\n",
		},
		{
			name:     "Binary SERVICE enabled to 1",
			input:    "enabled = %%binary SERVICE:antispam%%",
			expected: "enabled = 1\n",
		},
		{
			name:     "Binary SERVICE disabled to 0",
			input:    "enabled = %%binary SERVICE:webmail%%",
			expected: "enabled = 0\n",
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

func TestTransformTrueFalseDirective(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "TrueFalse TRUE to true",
			input:    "enabled = %%truefalse VAR:zimbraLogToSyslog%%",
			expected: "enabled = true\n",
		},
		{
			name:     "TrueFalse SERVICE enabled to true",
			input:    "enabled = %%truefalse SERVICE:antivirus%%",
			expected: "enabled = true\n",
		},
		{
			name:     "TrueFalse SERVICE disabled to false",
			input:    "enabled = %%truefalse SERVICE:webmail%%",
			expected: "enabled = false\n",
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

func TestTransformContainsDirective(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraMtaBlockedExtension": "exe bat com pif scr vbs",
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Contains match - use replacement",
			input:    "result = %%contains VAR:zimbraMtaBlockedExtension exe^FOUND^NOTFOUND%%",
			expected: "result = FOUND\n",
		},
		{
			name:     "Contains no match - use alt replacement",
			input:    "result = %%contains VAR:zimbraMtaBlockedExtension zip^FOUND^NOTFOUND%%",
			expected: "result = NOTFOUND\n",
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

func TestTransformExactDirective(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraMtaBlockedExtension": "exe bat com pif scr vbs",
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Exact match - use replacement",
			input:    "result = %%exact VAR:zimbraMtaBlockedExtension exe^FOUND^NOTFOUND%%",
			expected: "result = FOUND\n",
		},
		{
			name:     "Exact no match - use alt replacement",
			input:    "result = %%exact VAR:zimbraMtaBlockedExtension ex^FOUND^NOTFOUND%%",
			expected: "result = NOTFOUND\n",
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

func TestTransformRangeDirective(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraThreadPoolPercentage": "50",
			"zimbraMaxThreads":           "80",
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Range calculation at 50%",
			input:    "workers = %%range VAR:zimbraThreadPoolPercentage 10 100%%",
			expected: "workers = 55\n", // (50/100) * (100-10) + 10 = 0.5 * 90 + 10 = 55
		},
		{
			name:     "Range calculation at 80%",
			input:    "workers = %%range VAR:zimbraMaxThreads 20 200%%",
			expected: "workers = 164\n", // (80/100) * (200-20) + 20 = 0.8 * 180 + 20 = 164
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

func TestTransformFreqDirective(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraCheckInterval": "5m",
			"zimbraCleanupFreq":   "2h",
			"zimbraRotateDaily":   "1d",
			"zimbraLargeInterval": "120m",
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Freq with minutes - 5m converts to hours (0) so returns total",
			input:    "freq = %%freq VAR:zimbraCheckInterval 300%%",
			expected: "freq = 300\n", // 5m → 5/60 = 0 (integer division), so returns total
		},
		{
			name:     "Freq with hours - 2h into 3600 seconds",
			input:    "freq = %%freq VAR:zimbraCleanupFreq 3600%%",
			expected: "freq = 1800\n", // 2h → valNum=2, 3600/2 = 1800
		},
		{
			name:     "Freq with days - 1d into hourly",
			input:    "freq = %%freq VAR:zimbraRotateDaily 24%%",
			expected: "freq = 1\n", // 1d * 24 = 24, 24/24 = 1
		},
		{
			name:     "Freq with larger minutes - 120m converts to hours properly",
			input:    "freq = %%freq VAR:zimbraLargeInterval 3600%%",
			expected: "freq = 1800\n", // 120m → 120/60 = 2 hours, 3600/2 = 1800
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

func TestTransformExplodeDirective(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraUpstreamServers": "server1 server2 server3",
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Explode with base string",
			input:    "%%explode upstream VAR:zimbraUpstreamServers%%",
			expected: "upstream server1\nupstream server2\nupstream server3\n",
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

func TestTransformUncommentDirective(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraFeatureEnabled":  "TRUE",
			"zimbraFeatureDisabled": "FALSE",
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Uncomment when TRUE - no comment",
			input:    "%%uncomment VAR:zimbraFeatureEnabled%%feature.enabled = true",
			expected: "feature.enabled = true\n",
		},
		{
			name:     "Uncomment when FALSE - add comment",
			input:    "%%uncomment VAR:zimbraFeatureDisabled%%feature.disabled = true",
			expected: "#feature.disabled = true\n",
		},
		{
			name:     "Uncomment with custom comment string",
			input:    "%%uncomment VAR:zimbraFeatureDisabled,;%%feature = value",
			expected: ";feature = value\n",
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

func TestTransformCommentDirectiveWithValSet(t *testing.T) {
	ctx := context.Background()
	mockLookup := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"zimbraLogLevel":   "debug",
			"zimbraAuthMethod": "ldap",
		},
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Comment when value in set",
			input:    "%%comment VAR:zimbraLogLevel,#,debug,trace%%log.level = DEBUG",
			expected: "#log.level = DEBUG\n",
		},
		{
			name:     "No comment when value not in set",
			input:    "%%comment VAR:zimbraLogLevel,#,info,warn,error%%log.level = DEBUG",
			expected: "log.level = DEBUG\n",
		},
		{
			name:     "Uncomment when value not in set",
			input:    "%%uncomment VAR:zimbraAuthMethod,#,basic,oauth%%auth = ldap",
			expected: "auth = ldap\n",
		},
		{
			name:     "Uncomment adds comment when value in set",
			input:    "%%uncomment VAR:zimbraAuthMethod,#,ldap,kerberos%%auth = ldap",
			expected: "#auth = ldap\n",
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

// === DIRECTIVE EDGE CASES ===

// TestInvalidDirectiveFormats tests error handling for malformed directives
func TestInvalidDirectiveFormats(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Invalid VAR formats
		{
			name:     "VAR missing colon",
			input:    "%%VAR%%",
			expected: "\n",
		},
		{
			name:     "VAR empty key",
			input:    "%%VAR:%%",
			expected: "%%VAR:%%\n", // Empty keys are not transformed, pattern passed through
		},
		// Invalid directive formats
		{
			name:     "Binary missing config",
			input:    "%%binary%%",
			expected: "\n",
		},
		{
			name:     "Truefalse missing config",
			input:    "%%truefalse%%",
			expected: "\n",
		},
		{
			name:     "List missing separator",
			input:    "%%list VAR:test%%",
			expected: "\n",
		},
		{
			name:     "List invalid config format",
			input:    "%%list invalidconfig |%%",
			expected: "\n",
		},
		{
			name:     "Contains missing parts",
			input:    "%%contains VAR:test%%",
			expected: "\n",
		},
		{
			name:     "Exact missing search string",
			input:    "%%exact VAR:test%%",
			expected: "\n",
		},
		{
			name:     "Range missing values",
			input:    "%%range VAR:test%%",
			expected: "\n",
		},
		{
			name:     "Freq missing total",
			input:    "%%freq VAR:test%%",
			expected: "\n",
		},
		{
			name:     "Explode missing config",
			input:    "%%explode basestring%%",
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

// TestExplodeDirectiveRealWorld tests explode with realistic scenarios
func TestExplodeDirectiveRealWorld(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"rbl_servers":   "bl.spamcop.net zen.spamhaus.org",
			"single_server": "blocklist.example.com",
			"empty_servers": "",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Explode multiple RBL servers",
			input:    "%%explode reject_rbl_client VAR:rbl_servers%%",
			expected: "reject_rbl_client bl.spamcop.net\nreject_rbl_client zen.spamhaus.org\n",
		},
		{
			name:     "Explode single server",
			input:    "%%explode reject_rbl_client VAR:single_server%%",
			expected: "reject_rbl_client blocklist.example.com\n",
		},
		{
			name:     "Explode empty value",
			input:    "%%explode reject_rbl_client VAR:empty_servers%%",
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

// TestExactDirectiveRealWorld tests exact with realistic scenarios from Postfix
func TestExactDirectiveRealWorld(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"restrictions": "reject_invalid_helo_hostname reject_unknown_sender_domain permit_mynetworks",
			"empty":        "",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Exact match found - outputs search string",
			input:    "%%exact VAR:restrictions reject_invalid_helo_hostname%%",
			expected: "reject_invalid_helo_hostname\n",
		},
		{
			name:     "Exact match not found - outputs empty",
			input:    "%%exact VAR:restrictions reject_unverified_recipient%%",
			expected: "\n",
		},
		{
			name:     "Exact with replacement and altreplacement",
			input:    "%%exact VAR:restrictions reject_invalid_helo_hostname^REJECT^PERMIT%%",
			expected: "REJECT\n",
		},
		{
			name:     "Exact no match with altreplacement",
			input:    "%%exact VAR:restrictions reject_unverified_recipient^REJECT^PERMIT%%",
			expected: "PERMIT\n",
		},
		{
			name:     "Exact on empty value",
			input:    "%%exact VAR:empty anything^match^nomatch%%",
			expected: "nomatch\n",
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

// TestContainsDirectiveEdgeCases tests contains with edge cases
func TestContainsDirectiveEdgeCases(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"services": "cbpolicyd antispam antivirus",
			"empty":    "",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Contains substring match",
			input:    "%%contains VAR:services cbpolicyd^FOUND^NOTFOUND%%",
			expected: "FOUND\n",
		},
		{
			name:     "Contains no match",
			input:    "%%contains VAR:services webmail^FOUND^NOTFOUND%%",
			expected: "NOTFOUND\n",
		},
		{
			name:     "Contains with empty value",
			input:    "%%contains VAR:empty test^FOUND^NOTFOUND%%",
			expected: "NOTFOUND\n",
		},
		{
			name:     "Contains empty search always matches",
			input:    "%%contains VAR:services ^FOUND^NOTFOUND%%",
			expected: "FOUND\n",
		},
		{
			name:     "Contains case sensitive",
			input:    "%%contains VAR:services ANTISPAM^FOUND^NOTFOUND%%",
			expected: "NOTFOUND\n",
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

// TestListDirectiveEdgeCases tests list with various separators and edge cases
func TestListDirectiveEdgeCases(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"extensions": "exe bat com pif scr",
			"single":     "zip",
			"empty":      "",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "List with pipe separator",
			input:    "%%list VAR:extensions |%%",
			expected: "exe|bat|com|pif|scr\n",
		},
		{
			name:     "List with comma separator",
			input:    "%%list VAR:extensions ,%%",
			expected: "exe,bat,com,pif,scr\n",
		},
		{
			name:     "List with space separator",
			input:    "%%list VAR:extensions  %%",
			expected: "exe bat com pif scr\n",
		},
		{
			name:     "List single item",
			input:    "%%list VAR:single ,%%",
			expected: "zip\n",
		},
		{
			name:     "List empty value",
			input:    "%%list VAR:empty ,%%",
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

// TestFreqDirectiveEdgeCases tests frequency calculations
func TestFreqDirectiveEdgeCases(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"invalid":      "invalid_format",
			"zero_minutes": "0m",
			"seconds_only": "30s",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Freq invalid format returns total",
			input:    "%%freq VAR:invalid 3600%%",
			expected: "3600\n",
		},
		{
			name:     "Freq zero minutes returns total",
			input:    "%%freq VAR:zero_minutes 7200%%",
			expected: "7200\n",
		},
		{
			name:     "Freq seconds format returns total",
			input:    "%%freq VAR:seconds_only 1800%%",
			expected: "1800\n",
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

// TestValSetDirective tests comment/uncomment with valset
func TestValSetDirective(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"loglevel": "debug",
			"mode":     "production",
		},
	})

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Comment when value in valset",
			input:    "%%comment VAR:loglevel,#,debug,trace%%log_level = debug",
			expected: "#log_level = debug\n",
		},
		{
			name:     "No comment when value not in valset",
			input:    "%%comment VAR:loglevel,#,info,warn%%log_level = debug",
			expected: "log_level = debug\n",
		},
		{
			name:     "Uncomment when value not in valset",
			input:    "%%uncomment VAR:mode,#,development,staging%%mode = production",
			expected: "mode = production\n",
		},
		{
			name:     "Uncomment adds comment when value in valset",
			input:    "%%uncomment VAR:mode,#,production,testing%%mode = production",
			expected: "#mode = production\n",
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

// === DIRECTIVE HANDLER TESTS ===

// TestHandleRangeDirective_LookupError exercises the lookup error branch.
func TestHandleRangeDirective_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.handleRangeDirective(ctx, "range VAR:missingkey 0 100")
	if got != "" {
		t.Errorf("expected empty string on range lookup error, got %q", got)
	}
}

// TestHandleListDirective_LookupError exercises the lookup error branch.
func TestHandleListDirective_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.handleListDirective(ctx, "list VAR:missingkey |")
	if got != "" {
		t.Errorf("expected empty string on list lookup error, got %q", got)
	}
}

// TestHandleContainsDirective_LookupError exercises the lookup error branch.
func TestHandleContainsDirective_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.handleContainsDirective(ctx, "contains VAR:missingkey search^yes^no")
	if got != "" {
		t.Errorf("expected empty string on contains lookup error, got %q", got)
	}
}

// TestHandleContainsDirective_InvalidCfgTypeKey exercises the invalid type:key branch.
func TestHandleContainsDirective_InvalidCfgTypeKey(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(newMockLookup(), nil)

	got := tr.handleContainsDirective(ctx, "contains nokeyformat search^yes^no")
	if got != "" {
		t.Errorf("expected empty string for invalid contains config key, got %q", got)
	}
}

// TestHandleExactDirective_LookupError exercises the lookup error branch.
func TestHandleExactDirective_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.handleExactDirective(ctx, "exact VAR:missingkey word^yes^no")
	if got != "" {
		t.Errorf("expected empty string on exact lookup error, got %q", got)
	}
}

// TestHandleExactDirective_InvalidCfgTypeKey exercises the invalid type:key branch.
func TestHandleExactDirective_InvalidCfgTypeKey(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(newMockLookup(), nil)

	got := tr.handleExactDirective(ctx, "exact nokeyformat word^yes^no")
	if got != "" {
		t.Errorf("expected empty string for invalid exact config key, got %q", got)
	}
}

// TestHandleFreqDirective_LookupError exercises the lookup error branch.
func TestHandleFreqDirective_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.handleFreqDirective(ctx, "freq VAR:missingkey 3600")
	if got != "" {
		t.Errorf("expected empty string on freq lookup error, got %q", got)
	}
}

// TestHandleFreqDirective_ValIntLessThanOne exercises the val<1 clamp branch.
func TestHandleFreqDirective_ValIntLessThanOne(t *testing.T) {
	ctx := context.Background()
	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {"bigInterval": "100h"},
	})
	tr := NewTransformer(mock, nil)

	got := tr.handleFreqDirective(ctx, "freq VAR:bigInterval 3")
	if got != "1" {
		t.Errorf("expected freq clamp to 1, got %q", got)
	}
}

// TestHandleExplodeDirective_LookupError exercises the lookup error branch.
func TestHandleExplodeDirective_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.handleExplodeDirective(ctx, "explode base VAR:missingkey")
	if got != "" {
		t.Errorf("expected empty string on explode lookup error, got %q", got)
	}
}

// TestResolveCommentState_InvalidFormat exercises the len(matches)<3 branch.
func TestResolveCommentState_InvalidFormat(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(newMockLookup(), nil)

	got := tr.handleCommentDirective(ctx, "comment")
	if got != "" {
		t.Errorf("expected empty string for invalid comment format, got %q", got)
	}

	got = tr.handleUncommentDirective(ctx, "uncomment")
	if got != "" {
		t.Errorf("expected empty string for invalid uncomment format, got %q", got)
	}
}

// TestResolveCommentState_LookupError exercises the err!=nil branch in resolveCommentState.
func TestResolveCommentState_LookupError(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(&errorConfigLookup{}, nil)

	got := tr.handleCommentDirective(ctx, "comment VAR:anykey")
	if got != "" {
		t.Errorf("expected empty string when comment lookup fails (FALSE path), got %q", got)
	}

	got = tr.handleUncommentDirective(ctx, "uncomment VAR:anykey")
	if got != "#" {
		t.Errorf("expected \"#\" when uncomment lookup fails (empty=false path), got %q", got)
	}
}

// TestResolveCommentState_NegateWithValSet exercises the negate + valSet branch.
func TestResolveCommentState_NegateWithValSet(t *testing.T) {
	ctx := context.Background()
	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {"level": "debug"},
	})
	tr := NewTransformer(mock, nil)

	got := tr.Transform(ctx, "%%comment VAR:!level,#,debug,trace%%line content")
	want := "line content\n"
	if got != want {
		t.Errorf("negate+valset match: got %q, want %q", got, want)
	}

	got = tr.Transform(ctx, "%%comment VAR:!level,#,info,warn%%line content")
	want = "#line content\n"
	if got != want {
		t.Errorf("negate+valset no-match: got %q, want %q", got, want)
	}
}

// TestResolveCommentState_NegateNoValSet_Uncomment exercises negate + trueIsComment=false.
func TestResolveCommentState_NegateNoValSet_Uncomment(t *testing.T) {
	ctx := context.Background()
	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"enabled":  "TRUE",
			"disabled": "FALSE",
		},
	})
	tr := NewTransformer(mock, nil)

	got := tr.Transform(ctx, "%%uncomment VAR:!enabled%%content")
	want := "#content\n"
	if got != want {
		t.Errorf("uncomment negate TRUE: got %q, want %q", got, want)
	}

	got = tr.Transform(ctx, "%%uncomment VAR:!disabled%%content")
	want = "content\n"
	if got != want {
		t.Errorf("uncomment negate FALSE: got %q, want %q", got, want)
	}
}

// TestResolveCommentState_NegateNoValSet_Comment exercises negate + trueIsComment=true.
func TestResolveCommentState_NegateNoValSet_Comment(t *testing.T) {
	ctx := context.Background()
	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"enabled":  "TRUE",
			"disabled": "FALSE",
		},
	})
	tr := NewTransformer(mock, nil)

	got := tr.Transform(ctx, "%%comment VAR:!enabled%%content")
	want := "content\n"
	if got != want {
		t.Errorf("comment negate TRUE: got %q, want %q", got, want)
	}

	got = tr.Transform(ctx, "%%comment VAR:!disabled%%content")
	want = "#content\n"
	if got != want {
		t.Errorf("comment negate FALSE: got %q, want %q", got, want)
	}
}

// TestProcessPrefixDirective_NoClosingDelimiter exercises the endIdx==-1 branch.
func TestProcessPrefixDirective_NoClosingDelimiter(t *testing.T) {
	ctx := context.Background()
	tr := NewTransformer(newMockLookup(), nil)

	result, handled := tr.processPrefixDirective(ctx, "%%comment VAR:key_no_close")
	if handled {
		t.Errorf("expected handled=false when no closing %%%%, got true")
	}
	if result != "%%comment VAR:key_no_close" {
		t.Errorf("expected original line returned, got %q", result)
	}
}
