// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package rewrite implements "configd rewrite" and the forced-rewrite path
// shared by the commands that finish by regenerating configuration (tls,
// proxy): ask the running daemon to REWRITE, or rewrite in-process when no
// daemon answers.
package rewrite

import (
	"context"
	"io"
	"strconv"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	// IPModeIPv4 is the zimbraIPMode value of an IPv4-only host.
	IPModeIPv4        = "ipv4"
	defaultListenPort = 7171
)

// Standalone runs one forced rewrite cycle of configs in this process, as the
// legacy "zmconfigd <configs>" standalone mode does, and returns its exit code.
type Standalone func(ctx context.Context, configs []string) int

// Options are the parsed arguments of "configd rewrite".
type Options struct {
	// Configs are the config names to force (e.g. mta, proxy, all).
	Configs []string

	// Standalone rewrites in-process when no daemon answers.
	Standalone Standalone
}

// Run sends a forced configuration rewrite to the running daemon and returns
// the process exit status.
func Run(_ context.Context, _, stderr io.Writer, opts *Options) int {
	if !cliutil.RequireZextras(stderr) {
		return 1
	}

	ctx := cliutil.InitLogging(stderr)

	return Forced(ctx, opts.Configs, opts.Standalone)
}

// Forced sends REWRITE for configs to the running daemon and returns the exit
// code. When no daemon answers, standalone rewrites the configs in-process:
// legacy configrewrite falls back to "zmconfigd <configs>", and configd.py run
// with arguments processes them itself when contact_service fails
// (carbonio-core 4.6.7), so zmtlsctl, zmproxyctl and forced rewrites keep
// working while the daemon is stopped.
func Forced(ctx context.Context, configs []string, standalone Standalone) int {
	// An unreadable localconfig leaves the legacy defaults; the standalone run
	// reports the real error when it loads the configuration itself.
	lc, _ := localconfig.LoadResolvedConfig()
	listenPort, ipMode := ListenerAddress(lc)

	return viaDaemon(ctx, configs, listenPort, ipMode, standalone)
}

// viaDaemon is Forced with the listener address resolved.
func viaDaemon(ctx context.Context, configs []string, listenPort int, ipMode string, standalone Standalone) int {
	ctx = logger.ContextWithComponent(ctx, "main")

	if ContactService("REWRITE", configs, listenPort, ipMode) {
		logger.InfoContext(ctx, "configd service not reachable, rewriting in-process",
			"port", listenPort,
			"configs", configs)

		return standalone(ctx, configs)
	}

	logger.InfoContext(ctx, "Completed configuration update",
		"program", "configd", //nolint:goconst // semantic use differs from service name
		"configs", configs,
		"contacted_service", true)

	return 0
}

// ListenerAddress returns the daemon listener port and IP mode from the
// resolved localconfig lc, defaulting to 7171 over IPv4 like the legacy listener.
func ListenerAddress(lc map[string]string) (port int, ipMode string) {
	port, _ = strconv.Atoi(lc["zmconfigd_listen_port"])
	if port == 0 {
		port = defaultListenPort
	}

	ipMode = lc["zimbraIPMode"]
	if ipMode == "" {
		ipMode = IPModeIPv4
	}

	return port, ipMode
}
