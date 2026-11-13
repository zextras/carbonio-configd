// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/statcmd"
	"github.com/zextras/carbonio-configd/internal/stat"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// StatCmd handles "configd stat": the statistics collectors, the Go port of
// the zmstat-* scripts of carbonio-core.
type StatCmd struct {
	// Args are the words after "stat", verbatim: a collector and its options,
	// or "run" and the collectors.
	Args []string
}

func (a *app) statCmd() *cobra.Command {
	c := &a.cli.Stat

	cmd := a.leaf("stat <name> [options] | stat run <collector>...",
		"Run a statistics collector (zmstat-<name>), or several in one process",
		posArgs(nil, nil, true),
		func(_ *cobra.Command, args []string) error {
			c.Args = args

			return nil
		},
		func() error { return c.Run(a.env, a.std) })
	// The options are the collector's own (-i, -x, --log ...), as the script
	// took them.
	cmd.DisableFlagParsing = true
	cmd.ValidArgsFunction = staticFirstArg(append([]string{"run"}, stat.Names()...)...)

	return describeArgs(cmd,
		[2]string{"name", "Collector: " + strings.Join(stat.Names(), ", ")},
		[2]string{"[options]", "The options of the zmstat-<name> script (-i interval, -l log, ...)"},
		[2]string{"run <collector>...", "Run several collectors in one process; each argument is the command line of one, " +
			"e.g. \"io -x\""})
}

// Run executes the stat command: as the zextras user (root for fd), until
// SIGTERM or SIGINT; the exit status is the collector's.
func (c *StatCmd) Run(env sys.System, std *stdio) error {
	return exitWith(statcmd.Run(context.Background(), env, std.Out, std.Err, c.Args))
}
