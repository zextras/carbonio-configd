// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"log/slog"
	"os"
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
