// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/security"
	"github.com/zextras/carbonio-configd/internal/state"
)

const ipModeIPv4 = "ipv4"

func configureLogFormat(logConfig *logger.Config) {
	logFormat := os.Getenv("CONFIGD_LOG_FORMAT")
	switch logFormat {
	case "json":
		logConfig.Format = logger.FormatJSON
	case "text", "":
		logConfig.Format = logger.FormatText
	default:
		fmt.Fprintf(os.Stderr, "Warning: Unknown log format '%s', defaulting to text\n", logFormat)

		logConfig.Format = logger.FormatText
	}
}

func configureLogLevel(logConfig *logger.Config) {
	logLevel := os.Getenv("CONFIGD_LOG_LEVEL")
	switch logLevel {
	case "debug":
		logConfig.Level = logger.LogLevelDebug
	case "info", "":
		logConfig.Level = logger.LogLevelInfo
	case "warn", "warning":
		logConfig.Level = logger.LogLevelWarn
	case "error":
		logConfig.Level = logger.LogLevelError
	default:
		logConfig.Level = logger.LogLevelInfo
	}
}

// requireZextras enforces that the current user is strictly the 'zextras' user.
// Root is not accepted. Call this at the start of any Run() that must not be
// executed by arbitrary users (daemon, service control, rewrite, proxy write, init).
func requireZextras() {
	if err := security.MustCheckUserPermissions(); err != nil {
		os.Exit(1)
	}
}

func initializeLogging() context.Context {
	logConfig := logger.DefaultConfig()
	configureLogFormat(logConfig)
	configureLogLevel(logConfig)
	logger.InitStructuredLogging(logConfig)

	return logger.NewCorrelationID(context.Background())
}

func initializeConfig() (*config.Config, *state.State, *ldap.Ldap) {
	mainCfg, err := config.NewConfig()
	if err != nil {
		logger.FatalContext(context.Background(), "Failed to initialize config", "error", err)
	}

	appState := state.NewState()
	ldapClient := ldap.NewLdap(context.Background(), mainCfg)

	appState.SetConfigs(appState.LocalConfig,
		appState.GlobalConfig, appState.MiscConfig, appState.ServerConfig, appState.MtaConfig)

	// Load initial local config to get listen port and IP mode for contact_service
	appState.LocalConfig.Data["zmconfigd_listen_port"] = "7171"
	appState.LocalConfig.Data["zimbraIPMode"] = ipModeIPv4

	return mainCfg, appState, ldapClient
}

// handleForcedConfigs handles forced configuration rewrites if provided.
// When config names are given on the command line, configd acts as a TCP client:
// it sends a REWRITE command to the running daemon and exits.
// This is the replacement for: echo "REWRITE mta proxy" | nc localhost 7171
func handleForcedConfigs(ctx context.Context, args *Args, appState *state.State) {
	ctx = logger.ContextWithComponent(ctx, "main")

	if !args.HasForcedConfigs() {
		return
	}

	listenPort, _ := strconv.Atoi(appState.LocalConfig.Data["zmconfigd_listen_port"])
	ipMode := appState.LocalConfig.Data["zimbraIPMode"]

	if ContactService("REWRITE", args.ForcedConfigs, listenPort, ipMode) {
		logger.ErrorContext(ctx, "Failed to contact configd service",
			"port", listenPort,
			"configs", args.ForcedConfigs)
		fmt.Fprintf(os.Stderr,
			"Error: could not contact configd on port %d. Is carbonio-configd.service running?\n",
			listenPort)
		os.Exit(1)
	}

	logger.InfoContext(ctx, "Completed configuration update",
		"program", "configd", //nolint:goconst // semantic use differs from service name
		"configs", args.ForcedConfigs,
		"contacted_service", true)
	os.Exit(0)
}

// setupProfilingAndTracing sets up profiling and tracing if requested in args.
func setupProfilingAndTracing(ctx context.Context, args *Args) (*ProfilingConfig, *TracingConfig) {
	ctx = logger.ContextWithComponent(ctx, "main")

	var profilingConfig *ProfilingConfig

	var tracingConfig *TracingConfig

	if args.CPUProfile != "" || args.MemProfile != "" || args.Trace != "" {
		profilingConfig = &ProfilingConfig{
			CPUProfilePath:  args.CPUProfile,
			MemProfilePath:  args.MemProfile,
			TracePath:       args.Trace,
			ProfileDuration: time.Duration(args.ProfileDuration) * time.Second,
		}

		if err := ValidateProfilingConfig(profilingConfig); err != nil {
			logger.ErrorContext(ctx, "Invalid profiling configuration", "error", err)
			os.Exit(1)
		}

		if err := StartProfiling(profilingConfig); err != nil {
			logger.ErrorContext(ctx, "Failed to start profiling", "error", err)
			os.Exit(1)
		}
	}

	if args.EnableTracing {
		tracingConfig = &TracingConfig{
			OutputPath: args.TracingOutput,
			Format:     "json",
		}

		if err := ValidateTracingConfig(tracingConfig); err != nil {
			logger.ErrorContext(ctx, "Invalid tracing configuration", "error", err)
			StopProfiling(profilingConfig)
			os.Exit(1) //nolint:gocritic // StopProfiling called manually before exit
		}

		if err := StartTracing(tracingConfig); err != nil {
			logger.ErrorContext(ctx, "Failed to start tracing", "error", err)
			os.Exit(1)
		}
	}

	return profilingConfig, tracingConfig
}

// ensureZextrasPerlEnv injects PERLLIB / PERL5LIB into the process env when
// unset so that child processes we spawn (amavisd, zmstat-* perl workers,
// antispam-mysql.server, and any other perl-based tool) can locate Carbonio's
// bundled CPAN modules under /opt/zextras/common/lib/perl5. The zextras
// user's .bashrc sets these vars for interactive shells; daemons invoked by
// systemd (or by configd itself) do not source .bashrc, so without this
// amavisd fails with "Can't locate Amavis/Boot.pm in @INC".
//
// Existing values are preserved so operators can override for debugging.
func ensureZextrasPerlEnv() {
	if _, ok := os.LookupEnv("PERL5LIB"); ok {
		return
	}

	archname := perlArchname()
	if archname == "" {
		return
	}

	base := "/opt/zextras/common/lib/perl5"
	lib := base + "/" + archname + ":" + base

	_ = os.Setenv("PERLLIB", lib)
	_ = os.Setenv("PERL5LIB", lib)
}

// perlArchname asks the system perl for its architecture tag
// (e.g. "x86_64-linux-thread-multi" or "aarch64-linux-thread-multi"). Returns
// empty string when perl is missing or the output cannot be parsed; callers
// treat that as "skip env setup".
func perlArchname() string {
	//nolint:noctx,gosec // fixed binary path, runs once at startup
	out, err := exec.Command("/usr/bin/perl", "-V:archname").Output()
	if err != nil {
		return ""
	}

	s := strings.TrimSpace(string(out))

	start := strings.IndexByte(s, '\'')

	end := strings.LastIndexByte(s, '\'')
	if start < 0 || end <= start {
		return ""
	}

	return s[start+1 : end]
}

func main() {
	ensureZextrasPerlEnv()

	cli := &CLI{}

	parser, err := kong.New(cli,
		kong.Description("Configuration management daemon for Carbonio"),
		kong.Vars{"version": formatVersion()},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Must be called before Parse so the binary can respond to COMP_LINE/COMP_POINT
	// (set by bash's `complete -C`) and print completions instead of running normally.
	kongplete.Complete(parser)

	ctx, parseErr := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(parseErr)

	if err := ctx.Run(cli); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
