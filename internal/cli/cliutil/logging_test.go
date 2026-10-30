// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cliutil

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/zextras/carbonio-configd/internal/logger"
)

func TestConfigureLogFormat(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected logger.LogFormat
		warns    bool
	}{
		{"json format", "json", logger.FormatJSON, false},
		{"text format", "text", logger.FormatText, false},
		{"empty defaults to text", "", logger.FormatText, false},
		{"unknown defaults to text", "invalid", logger.FormatText, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CONFIGD_LOG_FORMAT", tt.envValue)

			var stderr bytes.Buffer

			cfg := logger.DefaultConfig()
			ConfigureLogFormat(cfg, &stderr)

			if cfg.Format != tt.expected {
				t.Errorf("expected format %v, got %v", tt.expected, cfg.Format)
			}

			if tt.warns {
				if want := "Warning: Unknown log format 'invalid', defaulting to text\n"; stderr.String() != want {
					t.Errorf("stderr = %q, want %q", stderr.String(), want)
				}
			} else if stderr.Len() != 0 {
				t.Errorf("unexpected stderr %q", stderr.String())
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
		{"debug level", "debug", logger.LogLevelDebug},
		{"info level", "info", logger.LogLevelInfo},
		{"warn level", "warn", logger.LogLevelWarn},
		{"warning level", "warning", logger.LogLevelWarn},
		{"error level", "error", logger.LogLevelError},
		{"empty defaults to info", "", logger.LogLevelInfo},
		{"unknown defaults to info", "invalid", logger.LogLevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CONFIGD_LOG_LEVEL", tt.envValue)

			cfg := logger.DefaultConfig()
			ConfigureLogLevel(cfg)

			if cfg.Level != tt.expected {
				t.Errorf("expected level %v, got %v", tt.expected, cfg.Level)
			}
		})
	}
}

func TestInitLogging_ReturnsContext(t *testing.T) {
	t.Setenv("CONFIGD_LOG_FORMAT", "json")
	t.Setenv("CONFIGD_LOG_LEVEL", "debug")

	if ctx := InitLogging(&bytes.Buffer{}); ctx == nil {
		t.Error("expected non-nil context from InitLogging")
	}
}

// TestInitCLILogging_OverrideRoutesToStderr pins that an explicit
// CONFIGD_LOG_LEVEL sends the logs to the command's stderr, so they never mix
// with the stdout table.
func TestInitCLILogging_OverrideRoutesToStderr(t *testing.T) {
	t.Setenv("CONFIGD_LOG_LEVEL", "error")
	t.Setenv("CONFIGD_LOG_FORMAT", "text")

	var stderr bytes.Buffer

	InitCLILogging(&stderr)
	logger.ErrorContext(context.Background(), "boom-from-test")

	if !bytes.Contains(stderr.Bytes(), []byte("boom-from-test")) {
		t.Errorf("stderr = %q, want the logged error", stderr.String())
	}
}

func TestInitCLILogging_QuietByDefault(t *testing.T) {
	var stderr bytes.Buffer

	InitCLILogging(&stderr)
	logger.ErrorContext(context.Background(), "hidden-from-test")

	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing without CONFIGD_LOG_LEVEL", stderr.String())
	}
}
