// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build profiling

package main

import "github.com/spf13/pflag"

// ProfilingArgs holds profiling-related command-line flags.
// Only compiled when built with -tags profiling.
// Embedded in CLI: its flags are registered on the root as global flags.
type ProfilingArgs struct {
	CPUProfile      string
	MemProfile      string
	Trace           string
	ProfileDuration int
	Once            bool
}

// register binds the fields to the global flags.
func (p *ProfilingArgs) register(fs *pflag.FlagSet) {
	fs.StringVar(&p.CPUProfile, "cpuprofile", "", "Write CPU profile to file")
	fs.StringVar(&p.MemProfile, "memprofile", "", "Write memory profile to file")
	fs.StringVar(&p.Trace, "trace", "", "Write execution trace to file")
	fs.IntVar(&p.ProfileDuration, "profile-duration", 0, "Profile duration in seconds (0 = entire execution)")
	fs.BoolVar(&p.Once, "once", false, "Run once and exit (for profiling/testing)")
}

// applyTo populates the corresponding fields in Args.
func (p *ProfilingArgs) applyTo(args *Args) {
	args.CPUProfile = p.CPUProfile
	args.MemProfile = p.MemProfile
	args.Trace = p.Trace
	args.ProfileDuration = p.ProfileDuration
	args.Once = p.Once
}
