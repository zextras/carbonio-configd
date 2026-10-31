// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cliutil

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// OutputFormatJSON is the CONFIGD_LOG_FORMAT value (and tracing output
// format) selecting JSON.
const OutputFormatJSON = "json"

// ConfigureLogFormat applies CONFIGD_LOG_FORMAT (text or json) to logConfig.
// An unknown value falls back to text with a warning on stderr.
func ConfigureLogFormat(logConfig *logger.Config, stderr io.Writer) {
	logFormat := os.Getenv("CONFIGD_LOG_FORMAT")
	switch logFormat {
	case OutputFormatJSON:
		logConfig.Format = logger.FormatJSON
	case "text", "":
		logConfig.Format = logger.FormatText
	default:
		_, _ = fmt.Fprintf(stderr, "Warning: Unknown log format '%s', defaulting to text\n", logFormat)

		logConfig.Format = logger.FormatText
	}
}

// ConfigureLogLevel applies CONFIGD_LOG_LEVEL (debug, info, warn, error) to
// logConfig; an unknown value means info.
func ConfigureLogLevel(logConfig *logger.Config) {
	logLevel := os.Getenv("CONFIGD_LOG_LEVEL")
	switch logLevel {
	case "debug":
		logConfig.Level = logger.LogLevelDebug
	case "info", "":
		logConfig.Level = logger.LogLevelInfo
	case "warn", "warning":
		logConfig.Level = logger.LogLevelWarn
	case "error":
		logConfig.Level = logger.LogLevelError
	default:
		logConfig.Level = logger.LogLevelInfo
	}
}

// InitLogging configures structured logging from the environment, like the
// daemon does, and returns a context carrying a fresh correlation ID.
func InitLogging(stderr io.Writer) context.Context {
	logConfig := logger.DefaultConfig()
	ConfigureLogFormat(logConfig, stderr)
	ConfigureLogLevel(logConfig)
	logger.InitStructuredLogging(logConfig)

	return logger.NewCorrelationID(context.Background())
}

// InitCLILogging configures structured logging for CLI subcommands.
//
// Default is error-only + /dev/null output so a normal `zmcontrol stop`
// stays quiet and leaves the stdout surface exclusively for the user-facing
// Console lines.
//
// Operators can raise verbosity on demand via CONFIGD_LOG_LEVEL (one of
// debug, info, warn, error) and CONFIGD_LOG_FORMAT (text or json) — the
// same knobs the daemon honors. When a level is set the output is routed to
// stderr so the logs appear alongside the CLI's normal output without
// interleaving them on stdout. This makes otherwise-hidden messages
// (e.g. the sd_notify "Graceful shutdown acknowledged" info log, the
// watchdog's restart decisions, legacy-mode diagnostics) visible without
// requiring code changes.
func InitCLILogging(stderr io.Writer) {
	cfg := logger.DefaultConfig()
	cfg.AddSource = false

	if _, override := os.LookupEnv("CONFIGD_LOG_LEVEL"); override {
		ConfigureLogLevel(cfg)
		ConfigureLogFormat(cfg, stderr)
		cfg.Output = stderr
	} else {
		cfg.Level = logger.LogLevelError

		if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
			cfg.Output = devNull
		}
	}

	logger.InitStructuredLogging(cfg)
}
