// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"

	"github.com/zextras/carbonio-configd/internal/rununit"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// RunUnitCmd is the ExecStart of the carbonio-run-exec@ and
// carbonio-run-forking@ template units: it runs the command a launch left in
// its spec file (see package rununit). Not meant to be run by hand.
type RunUnitCmd struct {
	Kind     string `arg:"" enum:"exec,forking" help:"Template kind: exec or forking"`
	Instance string `arg:"" help:"Unit instance (the service name)"`
}

// Run executes the unit's command. On success for kind exec it never returns:
// the process image is replaced.
func (c *RunUnitCmd) Run(_ *CLI, _ sys.System, std *stdio) error {
	if err := rununit.Main(rununit.DefaultDir, rununit.Kind(c.Kind), c.Instance); err != nil {
		_, _ = fmt.Fprintf(std.Err, "configd run-unit: %v\n", err)

		return exitStatus(1)
	}

	return nil
}
