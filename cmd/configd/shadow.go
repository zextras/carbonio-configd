// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/configmgr"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/shadow"
	"github.com/zextras/carbonio-configd/internal/state"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// errShadowFlag is returned for a global flag whose output file would be a
// side effect of the shadow run.
var errShadowFlag = errors.New("is not supported by \"configd shadow\": it must not write files outside --shadow-dir")

// ShadowCmd runs the daemon cycle next to a real configd without any side
// effect: it only logs the plan and, optionally, writes the would-be files.
type ShadowCmd struct {
	Dir  string
	Once bool
}

func (a *app) shadowCmd() *cobra.Command {
	c := &a.cli.Shadow

	cmd := a.leaf("shadow", "Run the cycle next to a real configd, without side effects", posArgs(nil, nil, false), nil,
		func() error { return c.Run(a.cli, a.env, a.std) })

	fs := cmd.Flags()
	fs.Var(pathValue{&c.Dir}, "shadow-dir", "Write the would-be files and plan JSON under `DIR`")
	fs.BoolVar(&c.Once, "single-cycle", false, "Run a single cycle and exit (status 1 when it failed)")

	return cmd
}

// Run executes the shadow command. It never exits on its own: like the daemon
// it runs until SIGINT or SIGTERM.
func (c *ShadowCmd) Run(cli *CLI, env sys.System, std *stdio) error {
	if !cliutil.RequireZextras(std.Err) {
		return exitStatus(1)
	}

	args := cli.toArgs()
	if err := rejectSideEffectFlags(args); err != nil {
		return err
	}

	ctx := cliutil.InitLogging(std.Err)
	ctx = logger.ContextWithComponent(ctx, "main")

	// The runtime gets the read-only system: whatever the manager, or anything
	// it calls (the service hooks included), tries to write, run or signal
	// fails with sys.ErrReadOnly. The only writable filesystem is the one the
	// shadow report goes through.
	rt, err := buildRuntime(ctx, env, &runtimeOpts{readOnly: true})
	if err != nil {
		logger.FatalContext(ctx, "Failed to initialize config", "error", err)
	}

	src := &shadowSource{
		ConfigManager: rt.cm,
		state:         rt.state,
		load: func(ctx context.Context) error {
			_, _, err := runLoadAndParse(ctx, rt.cm, rt.cfg)

			return err
		},
	}

	return exitWith(runShadow(ctx, env, rt.cfg, rt.state, src, c))
}

// rejectSideEffectFlags refuses the profiling and tracing outputs.
func rejectSideEffectFlags(args *Args) error {
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"--cpuprofile", args.CPUProfile != ""},
		{"--memprofile", args.MemProfile != ""},
		{"--trace", args.Trace != ""},
		{"--enable-tracing", args.EnableTracing},
	} {
		if f.set {
			return fmt.Errorf("%s %w", f.name, errShadowFlag)
		}
	}

	return nil
}

// runShadow wires the signal handling, which is the only host interaction the
// shadow run adds, and runs the cycles. It returns the process exit status.
func runShadow(
	ctx context.Context,
	env sys.System,
	mainCfg *config.Config,
	appState *state.State,
	src shadow.Source,
	c *ShadowCmd,
) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// No sd_notify: the real daemon owns the unit's lifecycle.
	reloadChan := make(chan struct{}, 1)
	SetupSignalHandler(ctx, appState, cancel, reloadChan, nil)

	sh, err := shadow.New(&shadow.Options{
		Source:   src,
		Clock:    env.Clock,
		Interval: func() time.Duration { return time.Duration(mainCfg.Interval) * time.Second },
		Reload:   reloadChan,
		Once:     c.Once,
		Dir:      c.Dir,
		Out:      env.FS,
		BaseDir:  mainCfg.BaseDir,
	})
	if err != nil {
		logger.ErrorContext(ctx, "Cannot start shadow mode", "error", err)

		return 1
	}

	logger.InfoContext(ctx, "Shadow mode: planning only, nothing will be written, run or restarted",
		"shadow_dir", c.Dir, "once", c.Once)

	if err := sh.Run(ctx); err != nil {
		return 1
	}

	return 0
}

// shadowSource adapts the ConfigManager to shadow.Source. Plan and
// RenderRewrite are the manager's own.
type shadowSource struct {
	*configmgr.ConfigManager

	state *state.State
	load  func(context.Context) error
}

// Load implements shadow.Source.
func (s *shadowSource) Load(ctx context.Context) error { return s.load(ctx) }

// CycleDone implements shadow.Source: the first successful plan ends the
// first run, exactly as the daemon loop does.
func (s *shadowSource) CycleDone() { s.state.SetFirstRun(false) }
