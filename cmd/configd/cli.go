// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"
	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// CLI is the root command struct parsed by Kong.
type CLI struct {
	Version         kong.VersionFlag `name:"version" short:"v" help:"Print version information and exit"`
	DisableRestarts bool             `name:"disable-restarts" help:"Disable all service restarts (dry-run mode)"`

	ProfilingArgs `embed:""`
	TracingArgs   `embed:""`

	Localconfig LocalconfigCmd               `cmd:"" help:"Manage local configuration"`
	Service     ServiceCmd                   `cmd:"" help:"Manage system services"`
	Status      StatusCmd                    `cmd:"" help:"Show service status"`
	Control     ControlCmd                   `cmd:"" help:"Orchestrate all services"`
	Init        InitCmd                      `cmd:"" help:"Initialize components"`
	Proxy       ProxyCmd                     `cmd:"" help:"Proxy protocol management"`
	TLS         TLSCmd                       `cmd:"" name:"tls" help:"Manage Carbonio mail mode (legacy zmtlsctl)"`
	Rewrite     RewriteCmd                   `cmd:"" help:"Force configuration rewrite"`
	Release     ReleaseCmd                   `cmd:"" help:"Show Carbonio release and package versions"`
	Completion  kongplete.InstallCompletions `cmd:"" help:"Install shell completions"`
	Daemon      DaemonCmd                    `cmd:"" default:"1" hidden:""`
}

// DaemonCmd handles the default behavior: run the daemon.
type DaemonCmd struct{}

// Run executes the daemon command.
func (c *DaemonCmd) Run(cli *CLI, env sys.System) error {
	if !cliutil.RequireZextras(os.Stderr) {
		return exitStatus(1)
	}

	ctx := cliutil.InitLogging(os.Stderr)
	ctx = logger.ContextWithComponent(ctx, "main")

	notifier, err := sdnotify.New()
	if err != nil {
		logger.ErrorContext(ctx, "Failed to initialize sd_notify", "error", err)
	}

	if notifier != nil {
		logger.InfoContext(ctx, "SD notify enabled, will notify systemd on readiness")
	}

	args := cli.toArgs()

	profilingConfig, tracingConfig, err := setupProfilingAndTracing(ctx, args)
	if err != nil {
		return exitStatus(1)
	}

	mainCfg, appState, ldapClient := initializeConfig()

	exitCode := RunMainLoop(ctx, env, mainCfg, appState, ldapClient, args, notifier)

	logger.InfoContext(ctx, "Process exited", "program", mainCfg.Progname)

	// Stop profiling/tracing explicitly before returning the exit status so the
	// profile is flushed before main exits the process. Both helpers are nil-safe.
	StopProfiling(profilingConfig)
	StopTracing(tracingConfig)

	return exitWith(exitCode)
}

// RewriteCmd sends a forced configuration rewrite to the running daemon.
type RewriteCmd struct {
	ConfigNames []string `arg:"" required:"" help:"Config names to force rewrite (e.g., mta, proxy, all)"`
}

// Run executes the rewrite command.
func (c *RewriteCmd) Run(_ *CLI, env sys.System) error {
	return exitWith(rewrite.Run(context.Background(), os.Stdout, os.Stderr, &rewrite.Options{
		Configs:    c.ConfigNames,
		Standalone: standaloneRewrite(env, false),
	}))
}

// toArgs converts the parsed CLI globals to an Args struct.
func (c *CLI) toArgs() *Args {
	args := &Args{
		DisableRestarts: c.DisableRestarts,
	}

	c.ProfilingArgs.applyTo(args)
	c.TracingArgs.applyTo(args)

	return args
}

// formatVersion builds a version string from build metadata.
func formatVersion() string {
	vi := GetVersionInfo()

	version := fmt.Sprintf("configd %s", vi.Version)

	if vi.Revision != "" {
		short := vi.Revision
		if len(short) > 12 {
			short = short[:12]
		}

		version += fmt.Sprintf(" (%s", short)

		if vi.Modified {
			version += ", dirty"
		}

		if vi.Time != "" {
			version += fmt.Sprintf(", %s", vi.Time)
		}

		version += ")"
	}

	return version
}
