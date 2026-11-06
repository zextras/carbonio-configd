// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build tracing

package main

import "github.com/spf13/pflag"

// TracingArgs holds tracing-related command-line flags.
// Only compiled when built with -tags tracing.
// Embedded in CLI: its flags are registered on the root as global flags.
type TracingArgs struct {
	EnableTracing bool
	TracingOutput string
}

// register binds the fields to the global flags.
func (p *TracingArgs) register(fs *pflag.FlagSet) {
	fs.BoolVar(&p.EnableTracing, "enable-tracing", false, "Enable span-based tracing")
	fs.StringVar(&p.TracingOutput, "tracing-output", "trace-spans.json", "Output file for tracing spans")
}

// applyTo populates the corresponding fields in Args.
func (p *TracingArgs) applyTo(args *Args) {
	args.EnableTracing = p.EnableTracing
	args.TracingOutput = p.TracingOutput
}
