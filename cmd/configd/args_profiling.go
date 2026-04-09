// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling

package main

// ProfilingArgs holds profiling-related command-line flags.
// Only compiled when built with -tags profiling.
// Embedded in CLI via embed:"" tag — fields become root-level flags.
type ProfilingArgs struct {
	CPUProfile      string `name:"cpuprofile" help:"Write CPU profile to file"`
	MemProfile      string `name:"memprofile" help:"Write memory profile to file"`
	Trace           string `name:"trace" help:"Write execution trace to file"`
	ProfileDuration int    `name:"profile-duration" default:"0" help:"Profile duration in seconds (0 = entire execution)"`
	Once            bool   `name:"once" help:"Run once and exit (for profiling/testing)"`
}

// applyTo populates the corresponding fields in Args.
func (p *ProfilingArgs) applyTo(args *Args) {
	args.CPUProfile = p.CPUProfile
	args.MemProfile = p.MemProfile
	args.Trace = p.Trace
	args.ProfileDuration = p.ProfileDuration
	args.Once = p.Once
}

// hasProfilingEnabled returns true when profiling support is compiled in.
func hasProfilingEnabled() bool {
	return true
}
