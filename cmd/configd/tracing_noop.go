// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

package main

// TracingConfig holds configuration for span-based tracing (no-op).
type TracingConfig struct {
	OutputPath string
	Format     string
}

// ValidateTracingConfig is a no-op stub when tracing is not enabled.
func ValidateTracingConfig(cfg *TracingConfig) error {
	return nil
}

// StartTracing is a no-op stub when tracing is not enabled.
func StartTracing(cfg *TracingConfig) error {
	return nil
}

// StopTracing is a no-op stub when tracing is not enabled.
func StopTracing(cfg *TracingConfig) {
	// no-op: tracing disabled at build time
}
