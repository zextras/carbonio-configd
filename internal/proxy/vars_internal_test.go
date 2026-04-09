// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - internal tests for unexported vars helpers
package proxy

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestParseDurationToMillis covers all branches of parseDurationToMillis (0% → target)
func TestParseDurationToMillis(t *testing.T) {
	tests := []struct {
		name          string
		val           string
		secondsAsUnit bool
		want          any
	}{
		// Empty string returns as-is
		{name: "empty string", val: "", secondsAsUnit: true, want: ""},

		// Valid Go duration strings
		{name: "milliseconds suffix", val: "500ms", secondsAsUnit: true, want: 500},
		{name: "seconds suffix", val: "10s", secondsAsUnit: true, want: 10000},
		{name: "minutes suffix", val: "5m", secondsAsUnit: true, want: 300000},
		{name: "hours suffix", val: "1h", secondsAsUnit: true, want: 3600000},

		// Plain integer, secondsAsUnit=true → multiply by 1000
		{name: "plain int seconds as unit", val: "30", secondsAsUnit: true, want: 30000},
		{name: "plain int zero seconds as unit", val: "0", secondsAsUnit: true, want: 0},

		// Plain integer, secondsAsUnit=false → keep as-is
		{name: "plain int not seconds", val: "300", secondsAsUnit: false, want: 300},
		{name: "plain int zero not seconds", val: "0", secondsAsUnit: false, want: 0},

		// Invalid string returned unchanged
		{name: "invalid string", val: "notanumber", secondsAsUnit: true, want: "notanumber"},
		{name: "invalid mixed", val: "10x", secondsAsUnit: false, want: "10x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDurationToMillis(tt.val, tt.secondsAsUnit)
			if got != tt.want {
				t.Errorf("parseDurationToMillis(%q, %v) = %v (%T), want %v (%T)",
					tt.val, tt.secondsAsUnit, got, got, tt.want, tt.want)
			}
		})
	}
}

// TestConvertToInteger covers success and error branches of convertToInteger (80% → 100%)
func TestConvertToInteger(t *testing.T) {
	ctx := context.Background()

	t.Run("valid integer string", func(t *testing.T) {
		got, err := convertToInteger(ctx, "42")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 42 {
			t.Errorf("expected 42, got %v", got)
		}
	})

	t.Run("zero", func(t *testing.T) {
		got, err := convertToInteger(ctx, "0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 0 {
			t.Errorf("expected 0, got %v", got)
		}
	})

	t.Run("negative integer", func(t *testing.T) {
		got, err := convertToInteger(ctx, "-5")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != -5 {
			t.Errorf("expected -5, got %v", got)
		}
	})

	t.Run("invalid string returns error", func(t *testing.T) {
		got, err := convertToInteger(ctx, "notanumber")
		if err == nil {
			t.Errorf("expected error, got nil (value=%v)", got)
		}
	})

	t.Run("float string returns error", func(t *testing.T) {
		_, err := convertToInteger(ctx, "3.14")
		if err == nil {
			t.Error("expected error for float string")
		}
	})
}

// TestLookupValueBranches tests all LookupValue branches (89.5% → better)
func TestLookupValueBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("OverrideServer hits server config", func(t *testing.T) {
		g := &Generator{
			ServerConfig: &config.ServerConfig{Data: map[string]string{"myAttr": "42"}},
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		}
		v := &Variable{
			Keyword:      "test",
			OverrideType: OverrideServer,
			ValueType:    ValueTypeInteger,
			Attribute:    "myAttr",
			DefaultValue: 0,
		}
		result, err := g.LookupValue(ctx, v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 42 {
			t.Errorf("expected 42, got %v", result)
		}
	})

	t.Run("OverrideServer falls back to global config", func(t *testing.T) {
		g := &Generator{
			ServerConfig: &config.ServerConfig{Data: map[string]string{}},
			GlobalConfig: &config.GlobalConfig{Data: map[string]string{"myAttr": "99"}},
		}
		v := &Variable{
			Keyword:      "test",
			OverrideType: OverrideServer,
			ValueType:    ValueTypeInteger,
			Attribute:    "myAttr",
			DefaultValue: 0,
		}
		result, err := g.LookupValue(ctx, v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 99 {
			t.Errorf("expected 99, got %v", result)
		}
	})

	t.Run("OverrideLocalConfig hits local config", func(t *testing.T) {
		g := &Generator{
			LocalConfig: &config.LocalConfig{Data: map[string]string{"localAttr": "77"}},
		}
		v := &Variable{
			Keyword:      "test",
			OverrideType: OverrideLocalConfig,
			ValueType:    ValueTypeInteger,
			Attribute:    "localAttr",
			DefaultValue: 0,
		}
		result, err := g.LookupValue(ctx, v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 77 {
			t.Errorf("expected 77, got %v", result)
		}
	})

	t.Run("OverrideLocalConfig falls through to default when attr missing", func(t *testing.T) {
		g := &Generator{
			LocalConfig: &config.LocalConfig{Data: map[string]string{}},
		}
		v := &Variable{
			Keyword:      "test",
			OverrideType: OverrideLocalConfig,
			Attribute:    "missingAttr",
			DefaultValue: "default-val",
		}
		result, err := g.LookupValue(ctx, v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "default-val" {
			t.Errorf("expected default-val, got %v", result)
		}
	})

	t.Run("CustomResolver is called", func(t *testing.T) {
		g := &Generator{}
		v := &Variable{
			Keyword: "custom",
			CustomResolver: func(_ context.Context) (any, error) {
				return "custom-result", nil
			},
		}
		result, err := g.LookupValue(ctx, v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "custom-result" {
			t.Errorf("expected custom-result, got %v", result)
		}
	})
}

// TestGetVariableNotFound tests GetVariable returns error when keyword not found (75% → 100%)
func TestGetVariableNotFound(t *testing.T) {
	g := &Generator{
		Variables: map[string]*Variable{
			"existing.var": {Keyword: "existing.var", Value: "val"},
		},
	}

	_, err := g.GetVariable("nonexistent.var")
	if err == nil {
		t.Error("expected error for missing variable, got nil")
	}

	// Also test successful path
	v, err := g.GetVariable("existing.var")
	if err != nil {
		t.Fatalf("unexpected error for existing var: %v", err)
	}
	if v.Keyword != "existing.var" {
		t.Errorf("expected existing.var, got %q", v.Keyword)
	}
}

// TestResolveAllVariablesError tests ResolveAllVariables returns error on conversion failure (81.8% → better)
func TestResolveAllVariablesError(t *testing.T) {
	ctx := context.Background()
	g := &Generator{
		Variables: map[string]*Variable{
			"bad.int": {
				Keyword:   "bad.int",
				ValueType: ValueTypeInteger,
				Value:     "notanumber",
				// Force LookupValue to call convertValue with bad int
				OverrideType: OverrideConfig,
				Attribute:    "badAttr",
			},
		},
		GlobalConfig: &config.GlobalConfig{
			Data: map[string]string{"badAttr": "notanumber"},
		},
	}

	err := g.ResolveAllVariables(ctx)
	if err == nil {
		t.Error("expected error when variable conversion fails, got nil")
	}
}

// TestConvertValue covers all ValueType branches of convertValue (30.8% → target)
func TestConvertValue(t *testing.T) {
	ctx := context.Background()
	g := &Generator{}

	tests := []struct {
		name    string
		val     string
		vt      ValueType
		want    any
		wantErr bool
	}{
		{name: "integer valid", val: "8", vt: ValueTypeInteger, want: 8},
		{name: "integer invalid", val: "bad", vt: ValueTypeInteger, wantErr: true},
		{name: "long valid", val: "9876543210", vt: ValueTypeLong, want: int64(9876543210)},
		{name: "long invalid", val: "nope", vt: ValueTypeLong, wantErr: true},
		{name: "boolean true", val: "TRUE", vt: ValueTypeBoolean, want: true},
		{name: "boolean false", val: "false", vt: ValueTypeBoolean, want: false},
		{name: "enabler true", val: "TRUE", vt: ValueTypeEnabler, want: true},
		{name: "enabler false", val: "FALSE", vt: ValueTypeEnabler, want: false},
		{name: "string", val: "hello", vt: ValueTypeString, want: "hello"},
		{name: "time seconds as unit", val: "60", vt: ValueTypeTime, want: 60000},
		{name: "time duration string", val: "1m", vt: ValueTypeTime, want: 60000},
		{name: "timeinsec plain int", val: "120", vt: ValueTypeTimeInSec, want: 120},
		{name: "default/unknown type", val: "raw", vt: ValueType(99), want: "raw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.convertValue(ctx, tt.val, tt.vt)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (value=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("convertValue(%q, %v) = %v (%T), want %v (%T)",
					tt.val, tt.vt, got, got, tt.want, tt.want)
			}
		})
	}
}
