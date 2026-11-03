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
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	// IPModeIPv4 is the zimbraIPMode value of an IPv4-only host.
	IPModeIPv4 = "ipv4"
	// IPModeIPv6 is the zimbraIPMode value of an IPv6-only host.
	IPModeIPv6        = "ipv6"
	defaultListenPort = 7171

	// defaultReplyTimeout bounds the wait for a reply when localconfig sets no
	// zimbra_configrewrite_timeout. It exceeds the daemon's 60s REWRITE timeout,
	// so the daemon's ERROR REWRITE TIMEOUT arrives before the client gives up.
	defaultReplyTimeout = 120 * time.Second
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

// Forced sends REWRITE for configs to the running daemon, waits until the
// daemon has processed them, and returns the exit code: 0 on success, 1 when
// the daemon answers ERROR (for instance ERROR REWRITE TIMEOUT) or the
// exchange fails. Only when nothing listens on either loopback family does
// standalone rewrite the configs in-process: legacy configrewrite falls back
// to "zmconfigd <configs>", so zmtlsctl, zmproxyctl and forced rewrites keep
// working while the daemon is stopped. A daemon that is up but answers with an
// error is never bypassed: a second rewriter would race it on the same files.
func Forced(ctx context.Context, configs []string, standalone Standalone) int {
	// An unreadable localconfig leaves the legacy defaults; the standalone run
	// reports the real error when it loads the configuration itself.
	lc, _ := localconfig.LoadResolvedConfig()

	return viaDaemon(ctx, configs, ListenerAddress(lc), standalone)
}

// viaDaemon is Forced with the daemon endpoint resolved.
func viaDaemon(ctx context.Context, configs []string, ep Endpoint, standalone Standalone) int {
	ctx = logger.ContextWithComponent(ctx, "main")

	_, err := Request(ctx, "REWRITE", configs, ep)

	switch {
	case err == nil:
		logger.InfoContext(ctx, "Completed configuration update",
			"program", "configd", //nolint:goconst // semantic use differs from service name
			"configs", configs,
			"contacted_service", true)

		return 0
	case errors.Is(err, ErrUnreachable):
		logger.InfoContext(ctx, "configd service not reachable, rewriting in-process",
			"port", ep.Port,
			"configs", configs)

		return standalone(ctx, configs)
	default:
		logger.ErrorContext(ctx, "Configuration update failed",
			"configs", configs,
			"error", err)

		return 1
	}
}

// ListenerAddress returns where the daemon listens, from the resolved
// localconfig lc: port zmconfigd_listen_port (default 7171) and the
// zimbraIPMode hint (a localconfig key only when an operator set it; the
// attribute itself lives in LDAP, so Request tries both loopback families).
// The reply timeout follows zimbra_configrewrite_timeout, the limit the legacy
// configrewrite used.
func ListenerAddress(lc map[string]string) Endpoint {
	port, _ := strconv.Atoi(lc["zmconfigd_listen_port"])
	if port == 0 {
		port = defaultListenPort
	}

	ep := Endpoint{Port: port, IPMode: lc["zimbraIPMode"]}

	if secs, err := strconv.Atoi(lc["zimbra_configrewrite_timeout"]); err == nil && secs > 0 {
		ep.Timeout = time.Duration(secs) * time.Second
	}

	return ep
}
