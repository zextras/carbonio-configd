// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

package main

import "github.com/zextras/carbonio-configd/internal/tracing"

// TracingConfig is re-exported from internal/tracing for CLI use.
type TracingConfig = tracing.Config

// ValidateTracingConfig delegates to internal/tracing (no-op).
func ValidateTracingConfig(cfg *TracingConfig) error {
	return tracing.ValidateTracingConfig(cfg)
}

// StartTracing delegates to internal/tracing (no-op).
func StartTracing(cfg *TracingConfig) error {
	return tracing.StartTracing(cfg)
}

// StopTracing delegates to internal/tracing (no-op).
func StopTracing(cfg *TracingConfig) {
	tracing.StopTracing(cfg)
}
