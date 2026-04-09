// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !profiling

package main

import (
	"time"
)

// ProfilingConfig holds profiling configuration.
// This is a no-op version when profiling is disabled at build time.
type ProfilingConfig struct {
	CPUProfilePath  string
	MemProfilePath  string
	TracePath       string
	ProfileDuration time.Duration
}

// StartProfiling is a no-op when profiling is disabled.
func StartProfiling(config *ProfilingConfig) error {
	return nil
}

// StopProfiling is a no-op when profiling is disabled.
func StopProfiling(config *ProfilingConfig) {
	// No-op
}

// ValidateProfilingConfig is a no-op when profiling is disabled.
func ValidateProfilingConfig(config *ProfilingConfig) error {
	return nil
}
