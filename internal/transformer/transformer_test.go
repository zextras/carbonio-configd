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

func newMockLookup() *testutil.MockConfigLookup {
	return testutil.NewMockConfigLookupWithData(map[string]map[string]string{
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
	})
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

// TestMultipleDirectivesInOneLine tests complex line processing
func TestMultipleDirectivesInOneLine(t *testing.T) {
	ctx := context.Background()
	st := &state.State{}

	mock := testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"host":    "mail.example.com",
			"port":    "8080",
			"enabled": "TRUE",
		},
	})

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
