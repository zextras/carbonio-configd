// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

// Args holds the runtime configuration for configd's daemon mode.
// Populated from the parsed CLI struct via CLI.toArgs().
type Args struct {
	ForcedConfigs   []string
	CPUProfile      string
	MemProfile      string
	Trace           string
	ProfileDuration int    // Duration in seconds, 0 means profile entire execution
	EnableTracing   bool   // Enable span-based tracing (requires build tag)
	TracingOutput   string // Output file for tracing spans (default: trace-spans.json)
	DisableRestarts bool   // Disable all service restarts (dry-run mode)
	Once            bool   // Run once and exit (for profiling/testing)
}

// HasForcedConfigs checks if there are any forced configuration names.
func (a *Args) HasForcedConfigs() bool {
	return len(a.ForcedConfigs) > 0
}
