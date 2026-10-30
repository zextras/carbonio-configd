// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"testing"
)

func TestCLIToArgs(t *testing.T) {
	cli := &CLI{DisableRestarts: true}
	args := cli.toArgs()
	if !args.DisableRestarts {
		t.Fatal("expected DisableRestarts to be propagated")
	}
}

func TestProfilingArgs_ApplyTo(t *testing.T) {
	p := &ProfilingArgs{}
	args := &Args{}
	p.applyTo(args) // no-op, must not panic
}

func TestTracingArgs_ApplyTo(t *testing.T) {
	tr := &TracingArgs{}
	args := &Args{}
	tr.applyTo(args) // no-op, must not panic
}

func TestStopProfiling_NilConfig(t *testing.T) {
	// StopProfiling with a nil config must not panic
	StopProfiling(nil)
}

func TestStopProfiling_EmptyConfig(t *testing.T) {
	p := &ProfilingConfig{}
	StopProfiling(p)
}

func TestStopTracing_NilConfig(t *testing.T) {
	StopTracing(nil)
}

func TestStopTracing_EmptyConfig(t *testing.T) {
	tr := &TracingConfig{}
	StopTracing(tr)
}

func TestSetupProfilingAndTracing_NoFlags(t *testing.T) {
	ctx := context.Background()
	args := &Args{}
	pc, tc, err := setupProfilingAndTracing(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pc != nil {
		t.Error("expected nil ProfilingConfig when no profiling flags set")
	}
	if tc != nil {
		t.Error("expected nil TracingConfig when no tracing flags set")
	}
}
