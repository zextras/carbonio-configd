// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling

package main

import "github.com/zextras/carbonio-configd/internal/tracing"

// ProfilingConfig is re-exported from internal/tracing for CLI use.
type ProfilingConfig = tracing.ProfilingConfig

// StartProfiling delegates to internal/tracing.
func StartProfiling(config *ProfilingConfig) error {
	return tracing.StartProfiling(config)
}

// StopProfiling delegates to internal/tracing.
func StopProfiling(config *ProfilingConfig) {
	tracing.StopProfiling(config)
}

// ValidateProfilingConfig delegates to internal/tracing.
func ValidateProfilingConfig(config *ProfilingConfig) error {
	return tracing.ValidateProfilingConfig(config)
}

// GenerateProfilePath delegates to internal/tracing.
func GenerateProfilePath(baseDir, profileType string) string {
	return tracing.GenerateProfilePath(baseDir, profileType)
}
