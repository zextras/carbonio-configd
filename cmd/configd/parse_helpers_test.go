// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

// recordRuns replaces the RunE of every runnable command under cmd with one
// that only records the command path: the arguments are parsed, validated and
// bound as for a real run (those are PreRunE hooks), and nothing is executed.
func recordRuns(cmd *cobra.Command, record func(path string)) {
	if cmd.RunE != nil {
		cmd.RunE = func(c *cobra.Command, _ []string) error {
			record(c.CommandPath())

			return nil
		}
	}

	for _, sub := range cmd.Commands() {
		recordRuns(sub, record)
	}
}

// parseArgv parses argv (without the program name) as run does and returns the
// resulting CLI and the path of the command that would run ("daemon" for the
// root, whose default is the daemon), or the parse error. The command does not
// run.
func parseArgv(t *testing.T, argv ...string) (*CLI, string, error) {
	t.Helper()

	cli := &CLI{}
	root, _ := newRootCmd("configd", cli, systest.New().System(), &stdio{Out: io.Discard, Err: io.Discard})

	ran := ""
	recordRuns(root, func(path string) { ran = path })

	root.SetArgs(legacyProxyGenArgs(argv))

	err := root.Execute()

	ran = strings.TrimSpace(strings.TrimPrefix(ran, "configd"))
	if ran == "" && err == nil {
		ran = "daemon"
	}

	return cli, ran, err
}
