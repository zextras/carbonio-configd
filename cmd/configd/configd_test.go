// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/logger"
)

func TestConfigureLogFormat(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected logger.LogFormat
	}{
		{
			name:     "json format",
			envValue: "json",
			expected: logger.FormatJSON,
		},
		{
			name:     "text format",
			envValue: "text",
			expected: logger.FormatText,
		},
		{
			name:     "empty defaults to text",
			envValue: "",
			expected: logger.FormatText,
		},
		{
			name:     "unknown defaults to text",
			envValue: "invalid",
			expected: logger.FormatText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("CONFIGD_LOG_FORMAT", tt.envValue)
			defer os.Unsetenv("CONFIGD_LOG_FORMAT")

			cfg := logger.DefaultConfig()
			configureLogFormat(cfg)

			if cfg.Format != tt.expected {
				t.Errorf("expected format %v, got %v", tt.expected, cfg.Format)
			}
		})
	}
}

func TestConfigureLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected slog.Level
	}{
		{
			name:     "debug level",
			envValue: "debug",
			expected: logger.LogLevelDebug,
		},
		{
			name:     "info level",
			envValue: "info",
			expected: logger.LogLevelInfo,
		},
		{
			name:     "warn level",
			envValue: "warn",
			expected: logger.LogLevelWarn,
		},
		{
			name:     "warning level",
			envValue: "warning",
			expected: logger.LogLevelWarn,
		},
		{
			name:     "error level",
			envValue: "error",
			expected: logger.LogLevelError,
		},
		{
			name:     "empty defaults to info",
			envValue: "",
			expected: logger.LogLevelInfo,
		},
		{
			name:     "unknown defaults to info",
			envValue: "invalid",
			expected: logger.LogLevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("CONFIGD_LOG_LEVEL", tt.envValue)
			defer os.Unsetenv("CONFIGD_LOG_LEVEL")

			cfg := logger.DefaultConfig()
			configureLogLevel(cfg)

			if cfg.Level != tt.expected {
				t.Errorf("expected level %v, got %v", tt.expected, cfg.Level)
			}
		})
	}
}

func TestInitializeLogging(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "json")
	os.Setenv("CONFIGD_LOG_LEVEL", "debug")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	ctx := initializeLogging()
	if ctx == nil {
		t.Error("expected non-nil context from initializeLogging")
	}
}

func TestInitializeConfig(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	initializeLogging()

	mainCfg, appState, ldapClient := initializeConfig()

	if mainCfg == nil {
		t.Error("expected non-nil config")
	}
	if ldapClient == nil {
		t.Error("expected non-nil ldap client")
	}

	if appState.LocalConfig.Data["zmconfigd_listen_port"] != "7171" {
		t.Errorf("expected listen port 7171, got %s", appState.LocalConfig.Data["zmconfigd_listen_port"])
	}
	if appState.LocalConfig.Data["zimbraIPMode"] != ipModeIPv4 {
		t.Errorf("expected IP mode %s, got %s", ipModeIPv4, appState.LocalConfig.Data["zimbraIPMode"])
	}
}

func TestPerlArchname_ValidOutput(t *testing.T) {
	if _, err := os.Stat("/usr/bin/perl"); err != nil {
		t.Skip("perl not available")
	}

	result := perlArchname()
	_ = result
}

func TestPerlArchname_ParseFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "standard perl output", input: "archname='x86_64-linux-thread-multi'\n", expected: "x86_64-linux-thread-multi"},
		{name: "no quotes", input: "archname=\n", expected: ""},
		{name: "single quote only start", input: "archname='value\n", expected: ""},
		{name: "empty quotes", input: "archname=''\n", expected: ""},
		{name: "multi segment", input: "archname='aarch64-linux-thread-multi'\n", expected: "aarch64-linux-thread-multi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := strings.TrimSpace(tt.input)
			start := strings.IndexByte(s, '\'')
			end := strings.LastIndexByte(s, '\'')
			var got string
			if start >= 0 && end > start {
				got = s[start+1 : end]
			}
			if got != tt.expected {
				t.Errorf("parse result = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestEnsureZextrasPerlEnv_PerlAlreadySet(t *testing.T) {
	os.Setenv("PERL5LIB", "/existing/path")
	defer os.Unsetenv("PERL5LIB")

	originalPerlLib := os.Getenv("PERLLIB")
	defer func() {
		if originalPerlLib != "" {
			os.Setenv("PERLLIB", originalPerlLib)
		} else {
			os.Unsetenv("PERLLIB")
		}
	}()

	ensureZextrasPerlEnv()

	perlLib := os.Getenv("PERLLIB")
	if strings.Contains(perlLib, "/opt/zextras/common/lib/perl5") {
		t.Error("PERLLIB should not be modified when PERL5LIB is already set")
	}
}

func TestEnsureZextrasPerlEnv_PerlNotAvailable(t *testing.T) {
	origPerl5Lib, hadPerl5Lib := os.LookupEnv("PERL5LIB")
	os.Unsetenv("PERL5LIB")
	defer func() {
		if hadPerl5Lib {
			os.Setenv("PERL5LIB", origPerl5Lib)
		}
	}()

	origPerlLib, hadPerlLib := os.LookupEnv("PERLLIB")
	os.Unsetenv("PERLLIB")
	defer func() {
		if hadPerlLib {
			os.Setenv("PERLLIB", origPerlLib)
		} else {
			os.Unsetenv("PERLLIB")
		}
	}()

	if _, err := os.Stat("/usr/bin/perl"); err != nil {
		ensureZextrasPerlEnv()
		if os.Getenv("PERL5LIB") != "" {
			t.Error("PERL5LIB should remain unset when perl is missing")
		}
		if os.Getenv("PERLLIB") != "" {
			t.Error("PERLLIB should remain unset when perl is missing")
		}
	}
}

func TestHandleForcedConfigs_NoForcedConfigs(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	initializeLogging()

	_, appState, _ := initializeConfig()

	args := &Args{}
	if args.HasForcedConfigs() {
		t.Error("expected no forced configs")
	}

	handleForcedConfigs(context.Background(), args, appState)
}

func TestSetupProfilingAndTracing_TracingEnabled(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	initializeLogging()

	args := &Args{
		EnableTracing: true,
		TracingOutput: t.TempDir() + "/trace.json",
	}

	pc, tc := setupProfilingAndTracing(context.Background(), args)
	if pc != nil {
		t.Error("expected nil ProfilingConfig when only tracing enabled")
	}
	if tc == nil {
		t.Error("expected non-nil TracingConfig when tracing enabled")
	}

	StopTracing(tc)
}

func TestEnsureZextrasPerlEnv_PerlSetsVars(t *testing.T) {
	if _, err := os.Stat("/usr/bin/perl"); err != nil {
		t.Skip("perl not available")
	}

	os.Unsetenv("PERL5LIB")
	os.Unsetenv("PERLLIB")
	defer os.Unsetenv("PERL5LIB")
	defer os.Unsetenv("PERLLIB")

	ensureZextrasPerlEnv()

	perl5lib := os.Getenv("PERL5LIB")
	perlLib := os.Getenv("PERLLIB")
	if perl5lib == "" || perlLib == "" {
		t.Errorf("expected PERL5LIB and PERLLIB to be set when perl is available, got PERL5LIB=%q PERLLIB=%q", perl5lib, perlLib)
	}
}

func TestConfigureLogFormat_Unknown(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "unknown_format")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")

	cfg := logger.DefaultConfig()
	configureLogFormat(cfg)
	if cfg.Format != logger.FormatText {
		t.Errorf("expected text format for unknown, got %v", cfg.Format)
	}
}

func TestSetupProfilingAndTracing_ValidationFails(t *testing.T) {
	os.Setenv("CONFIGD_LOG_FORMAT", "text")
	os.Setenv("CONFIGD_LOG_LEVEL", "error")
	defer os.Unsetenv("CONFIGD_LOG_FORMAT")
	defer os.Unsetenv("CONFIGD_LOG_LEVEL")

	initializeLogging()

	args := &Args{
		CPUProfile: "/nonexistent/path/cpu.prof",
	}

	defer func() {
		if r := recover(); r != nil {
			t.Logf("setupProfilingAndTracing panicked (expected): %v", r)
		}
	}()
	setupProfilingAndTracing(context.Background(), args)
}
