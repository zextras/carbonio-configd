// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !tracing

package main

// TracingArgs is empty when tracing is disabled at build time.
type TracingArgs struct{}

// applyTo is a no-op when tracing is disabled.
func (p *TracingArgs) applyTo(_ *Args) {
	// no-op: tracing disabled at build time
}

// hasTracingEnabled returns false when tracing support is not compiled in.
// This function is used by conditional help text generation.
//
//nolint:unused // Used conditionally via build tags
func hasTracingEnabled() bool {
	return false
}
