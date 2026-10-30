// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !profiling

package main

// ProfilingArgs is empty when profiling is disabled at build time.
type ProfilingArgs struct{}

// applyTo is a no-op when profiling is disabled.
func (p *ProfilingArgs) applyTo(_ *Args) {
	// no-op: profiling disabled at build time
}
