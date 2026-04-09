// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package main

import (
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// TracingConfig holds configuration for span-based tracing.
type TracingConfig struct {
	OutputPath string // Path to write trace spans
	Format     string // Output format: "json" or "timeline"
}

// ValidateTracingConfig validates the tracing configuration.
func ValidateTracingConfig(cfg *TracingConfig) error {
	if cfg.OutputPath == "" {
		return fmt.Errorf("tracing output path cannot be empty")
	}
	if cfg.Format != "json" && cfg.Format != "timeline" {
		return fmt.Errorf("invalid tracing format: %s (must be 'json' or 'timeline')", cfg.Format)
	}
	return nil
}

// StartTracing enables the tracing system.
func StartTracing(cfg *TracingConfig) error {
	ctx := context.Background()
	ctx = logger.ContextWithComponent(ctx, "tracing")
	logger.InfoContext(ctx, "Enabling span-based tracing",
		"output_path", cfg.OutputPath,
		"format", cfg.Format)
	tracing.Enable()
	return nil
}

// StopTracing exports collected spans and disables tracing.
func StopTracing(cfg *TracingConfig) {
	ctx := context.Background()
	ctx = logger.ContextWithComponent(ctx, "tracing")
	if !tracing.IsEnabled() {
		return
	}

	logger.InfoContext(ctx, "Exporting tracing spans",
		"output_path", cfg.OutputPath)

	if err := tracing.ExportToFile(cfg.OutputPath, cfg.Format); err != nil {
		logger.ErrorContext(ctx, "Failed to export tracing spans",
			"error", err)
		return
	}

	spans := tracing.GetSpans()
	logger.InfoContext(ctx, "Exported spans",
		"span_count", len(spans),
		"output_path", cfg.OutputPath)

	tracing.Disable()
}
