// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

package tracing

// Config holds configuration for span-based tracing (no-op).
type Config struct {
	OutputPath string
	Format     string
}

// ValidateTracingConfig is a no-op stub when tracing is not enabled.
func ValidateTracingConfig(cfg *Config) error { return nil }

// StartTracing is a no-op stub when tracing is not enabled.
func StartTracing(cfg *Config) error { return nil }

// StopTracing is a no-op stub when tracing is not enabled.
func StopTracing(_ *Config) {
	// No-op: tracing is disabled at build time (build tag !tracing).
}
