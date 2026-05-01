// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package tracing

import (
	"context"
	"fmt"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// Config holds configuration for span-based tracing.
type Config struct {
	OutputPath string // Path to write trace spans
	Format     string // Output format: "json" or "timeline"
}

// ValidateTracingConfig validates the tracing configuration.
func ValidateTracingConfig(cfg *Config) error {
	if cfg.OutputPath == "" {
		return fmt.Errorf("tracing output path cannot be empty")
	}
	if cfg.Format != "json" && cfg.Format != "timeline" {
		return fmt.Errorf("invalid tracing format: %s (must be 'json' or 'timeline')", cfg.Format)
	}
	return nil
}

// StartTracing enables the tracing system.
func StartTracing(cfg *Config) error {
	ctx := context.Background()
	ctx = logger.ContextWithComponent(ctx, "tracing")
	logger.InfoContext(ctx, "Enabling span-based tracing",
		"output_path", cfg.OutputPath,
		"format", cfg.Format)
	Enable()
	return nil
}

// StopTracing exports collected spans and disables tracing.
func StopTracing(cfg *Config) {
	ctx := context.Background()
	ctx = logger.ContextWithComponent(ctx, "tracing")
	if !IsEnabled() {
		return
	}

	logger.InfoContext(ctx, "Exporting tracing spans",
		"output_path", cfg.OutputPath)

	if err := ExportToFile(cfg.OutputPath, cfg.Format); err != nil {
		logger.ErrorContext(ctx, "Failed to export tracing spans",
			"error", err)
		return
	}

	spans := GetSpans()
	logger.InfoContext(ctx, "Exported spans",
		"span_count", len(spans),
		"output_path", cfg.OutputPath)

	Disable()
}
