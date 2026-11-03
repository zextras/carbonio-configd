// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"
	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// localHostname returns the server hostname the localconfig layers name: the
// CONFIGD_LC_zimbra_server_hostname overlay (a pod whose hostname is not the
// LDAP server entry's), else the zimbra_server_hostname key of localconfig.xml,
// which legacy zmhostname prints. It is "" when neither sets it (the LC.java
// default "localhost" is deliberately not used: it would hide the real
// hostname on hosts whose localconfig predates the key).
func localHostname() string {
	if host, ok := localconfig.EnvOverride(cliutil.LCKeyZimbraServerHostname); ok && host != "" {
		return host
	}

	raw, err := localconfig.LoadLocalConfigFromFile(localconfig.DefaultConfigPath())
	if err != nil {
		return ""
	}

	localconfig.Interpolate(raw)

	return raw[cliutil.LCKeyZimbraServerHostname]
}

// runStandaloneRewrite runs one forced cycle in this process, as the legacy
// "zmconfigd <configs>" standalone mode does.
func runStandaloneRewrite(ctx context.Context, env sys.System, args *Args) int {
	rt, err := buildRuntime(ctx, env, &runtimeOpts{disableRestarts: args.DisableRestarts})
	if err != nil {
		logger.FatalContext(ctx, "Failed to initialize config", "error", err)
	}

	return RunMainLoop(ctx, rt, args, nil)
}

// setupProfilingAndTracing sets up profiling and tracing if requested in args.
// A failure is logged and returned: the daemon then exits with status 1.
func setupProfilingAndTracing(
	ctx context.Context,
	args *Args,
) (profilingConfig *ProfilingConfig, tracingConfig *TracingConfig, err error) {
	ctx = logger.ContextWithComponent(ctx, "main")

	if args.CPUProfile != "" || args.MemProfile != "" || args.Trace != "" {
		profilingConfig = &ProfilingConfig{
			CPUProfilePath:  args.CPUProfile,
			MemProfilePath:  args.MemProfile,
			TracePath:       args.Trace,
			ProfileDuration: time.Duration(args.ProfileDuration) * time.Second,
		}

		if err := ValidateProfilingConfig(profilingConfig); err != nil {
			logger.ErrorContext(ctx, "Invalid profiling configuration", "error", err)

			return nil, nil, err
		}

		if err := StartProfiling(profilingConfig); err != nil {
			logger.ErrorContext(ctx, "Failed to start profiling", "error", err)

			return nil, nil, err
		}
	}

	if args.EnableTracing {
		tracingConfig = &TracingConfig{
			OutputPath: args.TracingOutput,
			Format:     cliutil.OutputFormatJSON,
		}

		if err := ValidateTracingConfig(tracingConfig); err != nil {
			logger.ErrorContext(ctx, "Invalid tracing configuration", "error", err)
			StopProfiling(profilingConfig)

			return nil, nil, err
		}

		if err := StartTracing(tracingConfig); err != nil {
			logger.ErrorContext(ctx, "Failed to start tracing", "error", err)

			return nil, nil, err
		}
	}

	return profilingConfig, tracingConfig, nil
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
func ensureZextrasPerlEnv(env sys.System) {
	if _, ok := os.LookupEnv("PERL5LIB"); ok {
		return
	}

	archname := perlArchname(env)
	if archname == "" {
		return
	}

	base := cliutil.BasePath("common", "lib", "perl5")
	lib := filepath.Join(base, archname) + ":" + base

	_ = os.Setenv("PERLLIB", lib)
	_ = os.Setenv("PERL5LIB", lib)
}

// perlArchname asks the system perl for its architecture tag
// (e.g. "x86_64-linux-thread-multi" or "aarch64-linux-thread-multi"). Returns
// empty string when perl is missing or the output cannot be parsed; callers
// treat that as "skip env setup".
func perlArchname(env sys.System) string {
	out, err := sys.Output(context.Background(), env.Exec, "/usr/bin/perl", "-V:archname")
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
	env := sys.Real()

	ensureZextrasPerlEnv(env)

	cli := &CLI{}

	parser, err := kong.New(cli,
		kong.Description("Configuration management daemon for Carbonio"),
		kong.Vars{"version": formatVersion()},
		kong.Bind(env),
		kong.Exit(exitKong),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Must be called before Parse so the binary can respond to COMP_LINE/COMP_POINT
	// (set by bash's `complete -C`) and print completions instead of running normally.
	kongplete.Complete(parser)

	ctx, parseErr := parser.Parse(legacyProxyGenArgs(os.Args[1:]))
	parser.FatalIfErrorf(parseErr)

	os.Exit(exitCode(ctx.Run(cli), os.Stderr))
}
