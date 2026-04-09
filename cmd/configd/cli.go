// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"
	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sdnotify"
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
	Release     VersionCmd                   `cmd:"" help:"Show Carbonio release and package versions"`
	Completion  kongplete.InstallCompletions `cmd:"" help:"Install shell completions"`
	Daemon      DaemonCmd                    `cmd:"" default:"1" hidden:""`
}

// DaemonCmd handles the default behavior: run the daemon.
type DaemonCmd struct{}

// Run executes the daemon command.
func (c *DaemonCmd) Run(cli *CLI) error {
	requireZextras()

	ctx := initializeLogging()
	ctx = logger.ContextWithComponent(ctx, "main")

	commands.Initialize()

	notifier, err := sdnotify.New()
	if err != nil {
		logger.ErrorContext(ctx, "Failed to initialize sd_notify", "error", err)
	}

	if notifier != nil {
		logger.InfoContext(ctx, "SD notify enabled, will notify systemd on readiness")
	}

	args := cli.toArgs()

	profilingConfig, tracingConfig := setupProfilingAndTracing(ctx, args)
	if profilingConfig != nil {
		defer StopProfiling(profilingConfig)
	}

	if tracingConfig != nil {
		defer StopTracing(tracingConfig)
	}

	mainCfg, appState, ldapClient := initializeConfig()

	RunMainLoop(ctx, mainCfg, appState, ldapClient, args, notifier)

	logger.InfoContext(ctx, "Process exited", "program", mainCfg.Progname)

	return nil
}

// RewriteCmd sends a forced configuration rewrite to the running daemon.
type RewriteCmd struct {
	ConfigNames []string `arg:"" required:"" help:"Config names to force rewrite (e.g., mta, proxy, all)"`
}

// Run executes the rewrite command.
//
//nolint:unparam // Kong interface requires error return; handleForcedConfigs calls os.Exit
func (c *RewriteCmd) Run(_ *CLI) error {
	requireZextras()

	ctx := initializeLogging()
	ctx = logger.ContextWithComponent(ctx, "main")

	_, appState, _ := initializeConfig()

	args := &Args{
		ForcedConfigs: c.ConfigNames,
	}

	handleForcedConfigs(ctx, args, appState)

	return nil
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
