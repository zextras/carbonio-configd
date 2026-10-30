// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zextras/carbonio-configd/internal/cache"
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
	Dir  string `name:"shadow-dir" type:"path" placeholder:"DIR" help:"Write the would-be files and plan JSON under DIR"`
	Once bool   `name:"single-cycle" help:"Run a single cycle and exit (status 1 when it failed)"`
}

// Run executes the shadow command. It never exits on its own: like the daemon
// it runs until SIGINT or SIGTERM.
func (c *ShadowCmd) Run(cli *CLI, env sys.System) error {
	if !cliutil.RequireZextras(os.Stderr) {
		return exitStatus(1)
	}

	args := cli.toArgs()
	if err := rejectSideEffectFlags(args); err != nil {
		return err
	}

	ctx := cliutil.InitLogging(os.Stderr)
	ctx = logger.ContextWithComponent(ctx, "main")

	mainCfg, appState, ldapClient := initializeConfig()

	// The ConfigManager gets the read-only system: whatever it, or anything it
	// calls, tries to write, run or signal fails with sys.ErrReadOnly. The
	// only writable filesystem is the one the shadow report goes through.
	cm := configmgr.NewConfigManager(ctx, mainCfg, appState, ldapClient, cache.New(ctx, false), sys.ReadOnly(env))
	src := &shadowSource{
		ConfigManager: cm,
		state:         appState,
		load: func(ctx context.Context) error {
			_, _, err := runLoadAndParse(ctx, cm, mainCfg)

			return err
		},
	}

	return exitWith(runShadow(ctx, env, mainCfg, appState, src, c))
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
