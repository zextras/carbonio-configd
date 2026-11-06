// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !profiling

package main

import "github.com/spf13/pflag"

// ProfilingArgs is empty when profiling is disabled at build time.
type ProfilingArgs struct{}

// register is a no-op when profiling is disabled at build time.
func (p *ProfilingArgs) register(_ *pflag.FlagSet) {
	// no-op: without the profiling build tag there are no profiling flags to add
}

// applyTo is a no-op when profiling is disabled.
func (p *ProfilingArgs) applyTo(_ *Args) {
	// no-op: profiling disabled at build time
}
