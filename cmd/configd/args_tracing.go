// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package main

// TracingArgs holds tracing-related command-line flags.
// Only compiled when built with -tags tracing.
// Embedded in CLI via embed:"" tag — fields become root-level flags.
type TracingArgs struct {
	EnableTracing bool   `name:"enable-tracing" help:"Enable span-based tracing"`
	TracingOutput string `name:"tracing-output" default:"trace-spans.json" help:"Output file for tracing spans"`
}

// applyTo populates the corresponding fields in Args.
func (p *TracingArgs) applyTo(args *Args) {
	args.EnableTracing = p.EnableTracing
	args.TracingOutput = p.TracingOutput
}

// hasTracingEnabled returns true when tracing support is compiled in.
func hasTracingEnabled() bool {
	return true
}
