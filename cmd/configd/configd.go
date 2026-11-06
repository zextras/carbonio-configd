// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
func ensureZextrasPerlEnv(ctx context.Context, env sys.System) error {
	if _, ok := os.LookupEnv("PERL5LIB"); ok {
		return nil
	}

	archname := perlArchname(ctx, env)
	if archname == "" {
		return nil
	}

	base := cliutil.BasePath("common", "lib", "perl5")
	lib := filepath.Join(base, archname) + ":" + base

	var errs []error

	for _, key := range []string{"PERLLIB", "PERL5LIB"} {
		if err := os.Setenv(key, lib); err != nil {
			errs = append(errs, fmt.Errorf("set %s: %w", key, err))
		}
	}

	return errors.Join(errs...)
}

// perlArchnameTimeout bounds the "perl -V:archname" probe: a wedged perl must
// not keep every configd command from starting.
const perlArchnameTimeout = 10 * time.Second

// perlArchname asks the system perl for its architecture tag
// (e.g. "x86_64-linux-thread-multi" or "aarch64-linux-thread-multi"). Returns
// empty string when perl is missing, times out (perlArchnameTimeout) or its
// output cannot be parsed; callers treat that as "skip env setup".
func perlArchname(ctx context.Context, env sys.System) string {
	ctx, cancel := context.WithTimeout(ctx, perlArchnameTimeout)
	defer cancel()

	out, err := sys.Output(ctx, env.Exec, "/usr/bin/perl", "-V:archname")
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

// main runs the command line and exits with its status: the only place that
// ends the process.
func main() {
	os.Exit(run(os.Args, sys.Real(), os.Stdout, os.Stderr))
}

// run executes the command line argv (os.Args-shaped) on env, writing to
// stdout and stderr, and returns the process exit status. It is main with the
// process boundary injected, so the status and the stderr text of every
// command and error path can be checked in-process.
func run(argv []string, env sys.System, stdout, stderr io.Writer) int {
	std := &stdio{Out: stdout, Err: stderr, Args: argv}

	// Shell completion asks for a list of words, and must stay quick and quiet.
	if !isCompletionRequest(argv[1:]) {
		if err := ensureZextrasPerlEnv(context.Background(), env); err != nil {
			_, _ = fmt.Fprintf(stderr, "Warning: perl environment not set: %v\n", err)
		}
	}

	name := filepath.Base(argv[0])
	cli := &CLI{}
	root, a := newRootCmd(name, cli, env, std)
	root.SetArgs(legacyProxyGenArgs(argv[1:]))

	err := root.Execute()

	return a.exitStatus(err, name, stderr)
}

// isCompletionRequest reports whether args ask for a completion: the hidden
// __complete verbs the generated scripts call, or the completion command.
func isCompletionRequest(args []string) bool {
	return len(args) > 0 && slices.Contains(
		[]string{cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd, "completion"}, args[0])
}
