// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/state"
	"testing"
)

// mockConfigLookup implements the ConfigLookup interface for testing
type mockConfigLookup struct {
	data map[string]map[string]string
}

func (m *mockConfigLookup) LookUpConfig(ctx context.Context, cfgType, key string) (string, error) {
	if typeData, ok := m.data[cfgType]; ok {
		if val, ok := typeData[key]; ok {
			return val, nil
		}
	}
	return "", fmt.Errorf("key not found: %s:%s", cfgType, key)
}

func newMockLookup() *mockConfigLookup {
	return &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraLocalBindAddress":    "127.0.0.1",
				"zimbraLogHostname":         "log.example.com",
				"zimbraLogToSyslog":         "TRUE",
				"zimbraMtaBlockedExtension": "exe bat com pif scr vbs",
				"zimbraServerHostname":      "mail.example.com",
			},
			"LOCAL": {
				"ldap_url":               "ldap://ldap1.example.com:389 ldap://ldap2.example.com:389",
				"mysql_bind_address":     "127.0.0.1",
				"zimbra_server_hostname": "mail.local.example.com",
			},
			"SERVICE": {
				"antispam":  "TRUE",
				"antivirus": "TRUE",
				"webmail":   "FALSE",
			},
		},
	}
}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraMtaBlockedExtension": "exe bat com pif scr vbs",
			},
		},
	}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraMtaBlockedExtension": "exe bat com pif scr vbs",
			},
		},
	}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraThreadPoolPercentage": "50",
				"zimbraMaxThreads":           "80",
			},
		},
	}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraCheckInterval": "5m",
				"zimbraCleanupFreq":   "2h",
				"zimbraRotateDaily":   "1d",
				"zimbraLargeInterval": "120m",
			},
		},
	}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraUpstreamServers": "server1 server2 server3",
			},
		},
	}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraFeatureEnabled":  "TRUE",
				"zimbraFeatureDisabled": "FALSE",
			},
		},
	}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraLogLevel":   "debug",
				"zimbraAuthMethod": "ldap",
			},
		},
	}

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

func TestTransformEdgeCases(t *testing.T) {
	ctx := context.Background()
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"emptyValue":    "",
				"spacedValue":   "  value with spaces  ",
				"multilineText": "line1\nline2\nline3",
			},
			"LOCAL": {},
		},
	}

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
	mockLookup := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"varKey": "var_value",
			},
			"LOCAL": {
				"localKey": "local_value",
				"varKey":   "local_var_value", // Same key in both
			},
		},
	}

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

func TestTransformComplexScenarios(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Mixed VAR substitution in comment directive",
			input:    "%%uncomment VAR:zimbraLogHostname%%appender.SLOGGER.host = %%VAR:zimbraLogHostname%%",
			expected: "appender.SLOGGER.host = log.example.com\n",
		},
		{
			name:     "VAR and LOCAL in same line",
			input:    "server = %%VAR:zimbraServerHostname%% local_bind = @@mysql_bind_address@@",
			expected: "server = mail.example.com local_bind = 127.0.0.1\n",
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

// Benchmark tests for transformer performance

func BenchmarkTransform_SimpleVarSubstitution(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "bind_address = %%VAR:zimbraLocalBindAddress%%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkTransform_LocalConfigSubstitution(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "mysql_bind_address = @@mysql_bind_address@@"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkTransform_CommentDirective(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "%%comment VAR:zimbraLogToSyslog%%syslog-enabled"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkTransform_BinaryDirective(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "%%binary SERVICE:antispam%%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkTransform_ListDirective(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "%%list VAR:zimbraMtaBlockedExtension |%%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkTransform_ContainsDirective(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "%%contains VAR:zimbraMtaBlockedExtension exe^blocked^allowed%%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkTransform_ComplexLine(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "%%comment SERVICE:antispam%%server = %%VAR:zimbraServerHostname%% bind = @@mysql_bind_address@@"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkTransform_NoSubstitution(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	line := "simple line with no directives"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.Transform(ctx, line)
	}
}

func BenchmarkXformConfig_Comment(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	match := "%%comment VAR:zimbraLogToSyslog%%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.xformConfig(ctx, match)
	}
}

func BenchmarkXformConfig_Binary(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	match := "%%binary SERVICE:antispam%%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.xformConfig(ctx, match)
	}
}

func BenchmarkXformConfig_List(b *testing.B) {
	ctx := context.Background()
	st := &state.State{}
	mockLookup := newMockLookup()
	transformer := NewTransformer(mockLookup, st)
	match := "%%list VAR:zimbraMtaBlockedExtension |%%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = transformer.xformConfig(ctx, match)
	}
}

// === COMPREHENSIVE EDGE CASE TESTS ===

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

// TestSpecialCharactersInValues tests handling of special characters
func TestSpecialCharactersInValues(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"special":   "value with & symbols !@#$%",
				"newline":   "line1\nline2",
				"tabs":      "tab\tseparated",
				"quotes":    "value with \"quotes\"",
				"backslash": "path\\to\\file",
				"unicode":   "unicode: 你好",
			},
		},
	}

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

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zero":     "0",
				"hundred":  "100",
				"negative": "-50",
				"decimal":  "50.5",
				"large":    "999999",
			},
		},
	}

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

// TestLocalConfigSPLITFunction tests SPLIT and PERDITION_LDAP_SPLIT
func TestLocalConfigSPLITFunction(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"LOCAL": {
				"multiword":  "first second third",
				"ldap_urls":  "ldap://host1:389 ldap://host2:389 ldap://host3:389",
				"singleword": "single",
			},
		},
	}

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

// TestExplodeDirectiveRealWorld tests explode with realistic scenarios
func TestExplodeDirectiveRealWorld(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"rbl_servers":   "bl.spamcop.net zen.spamhaus.org",
				"single_server": "blocklist.example.com",
				"empty_servers": "",
			},
		},
	}

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

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"restrictions": "reject_invalid_helo_hostname reject_unknown_sender_domain permit_mynetworks",
				"empty":        "",
			},
		},
	}

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

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"services": "cbpolicyd antispam antivirus",
				"empty":    "",
			},
		},
	}

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

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"extensions": "exe bat com pif scr",
				"single":     "zip",
				"empty":      "",
			},
		},
	}

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

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"invalid":      "invalid_format",
				"zero_minutes": "0m",
				"seconds_only": "30s",
			},
		},
	}

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

// TestMultipleDirectivesInOneLine tests complex line processing
func TestMultipleDirectivesInOneLine(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"host":    "mail.example.com",
				"port":    "8080",
				"enabled": "TRUE",
			},
		},
	}

	transformer := NewTransformer(mock, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Multiple VAR substitutions",
			input:    "server = %%VAR:host%%:%%VAR:port%%",
			expected: "server = mail.example.com:8080\n",
		},
		{
			name:     "Binary and VAR together",
			input:    "enabled=%%binary VAR:enabled%% server=%%VAR:host%%",
			expected: "enabled=1 server=mail.example.com\n",
		},
		{
			name:     "Prefix directive with inline substitution",
			input:    "%%comment VAR:enabled%%server = %%VAR:host%%",
			expected: "#server = mail.example.com\n",
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

	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"loglevel": "debug",
				"mode":     "production",
			},
		},
	}

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

// errorConfigLookup always returns an error for any lookup.
type errorConfigLookup struct{}

func (e *errorConfigLookup) LookUpConfig(_ context.Context, _, _ string) (string, error) {
	return "", fmt.Errorf("lookup error")
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

// TestResolveCommentState_NegateWithValSet exercises the negate + valSet branch.
func TestResolveCommentState_NegateWithValSet(t *testing.T) {
	ctx := context.Background()
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {"level": "debug"},
		},
	}
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
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"enabled":  "TRUE",
				"disabled": "FALSE",
			},
		},
	}
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
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"enabled":  "TRUE",
				"disabled": "FALSE",
			},
		},
	}
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

// TestXformLocalConfig_PerditionLdapSplit_NoURLs exercises the no-hostnames path.
func TestXformLocalConfig_PerditionLdapSplit_NoURLs(t *testing.T) {
	ctx := context.Background()
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"LOCAL": {
				"plain_server": "mailhost.example.com",
			},
		},
	}
	tr := NewTransformer(mock, nil)

	got := tr.Transform(ctx, "@@PERDITION_LDAP_SPLIT plain_server@@")
	want := "mailhost.example.com\n"
	if got != want {
		t.Errorf("PERDITION_LDAP_SPLIT no urls: got %q, want %q", got, want)
	}
}

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
	mock := &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {"bigInterval": "100h"},
		},
	}
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
