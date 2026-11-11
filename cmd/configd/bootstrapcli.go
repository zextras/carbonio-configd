// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/bootstrapcmd"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// BootstrapCmd handles "configd bootstrap".
type BootstrapCmd struct {
	Config BootstrapConfigCmd
	Run    BootstrapRunCmd
}

// BootstrapRunCmd is "configd bootstrap run": configure the node.
type BootstrapRunCmd struct {
	BootstrapConfigCmd
	DryRun bool
}

// BootstrapConfigCmd is "configd bootstrap config": resolve, validate and
// print or save the bootstrap configuration.
type BootstrapConfigCmd struct {
	ConfigPath     string
	LegacyPath     string
	Sets           []string
	NonInteractive bool
	Wizard         string
	Output         string
}

func (a *app) bootstrapCmd() *cobra.Command {
	c := &a.cli.Bootstrap.Config

	config := a.leaf("config", "Resolve, validate and print or save the bootstrap configuration",
		posArgs(nil, nil, false), nil, func() error { return c.Run(a.env, a.std) })
	bootstrapConfigFlags(config, c)

	rc := &a.cli.Bootstrap.Run
	run := a.leaf("run", "Configure this node (the work of the legacy setup.pl)",
		posArgs(nil, nil, false), nil, func() error { return rc.Run(a.env, a.std) })
	bootstrapConfigFlags(run, &rc.BootstrapConfigCmd)
	run.Flags().BoolVar(&rc.DryRun, "dry-run", false, "Print the steps that would apply; change nothing")

	return group("bootstrap <command>", "Bootstrap a Carbonio node", config, run)
}

func bootstrapConfigFlags(config *cobra.Command, c *BootstrapConfigCmd) {
	fs := config.Flags()
	fs.SortFlags = false
	fs.Var(pathValue{&c.ConfigPath}, "config", "YAML configuration file")
	fs.Var(pathValue{&c.LegacyPath}, "legacy-config", "setup.pl configuration file (KEY=VALUE) to import")
	fs.StringArrayVar(&c.Sets, "set", nil, "Set a key: key=value (e.g. ldap.port=636); repeat for several")
	fs.BoolVar(&c.NonInteractive, "non-interactive", false, "Never run the wizard; fail on missing keys")
	enumVar(fs, &c.Wizard, "wizard", bootstrapcmd.WizardMissing, "Wizard questions to ask",
		bootstrapcmd.WizardMissing, bootstrapcmd.WizardAll)
	fs.Var(pathValue{&c.Output}, "output", "Save the configuration to this file (mode 0600, no passwords)")
	mustCompleteFlag(config, "wizard", staticCompletion(bootstrapcmd.WizardMissing, bootstrapcmd.WizardAll))
	noFileCompletion(config, "set")
}

// Run executes "configd bootstrap config".
func (c *BootstrapConfigCmd) Run(env sys.System, std *stdio) error {
	return exitWith(bootstrapcmd.Run(context.Background(), env, std.In, std.Out, std.Err, c.options()))
}

func (c *BootstrapConfigCmd) options() *bootstrapcmd.Options {
	return &bootstrapcmd.Options{
		ConfigPath:     c.ConfigPath,
		LegacyPath:     c.LegacyPath,
		Sets:           c.Sets,
		NonInteractive: c.NonInteractive,
		Wizard:         c.Wizard,
		Output:         c.Output,
		Environ:        os.Environ(),
	}
}

// Run executes "configd bootstrap run".
func (c *BootstrapRunCmd) Run(env sys.System, std *stdio) error {
	return exitWith(bootstrapcmd.RunSetup(context.Background(), env, std.In, std.Out, std.Err,
		&bootstrapcmd.RunOptions{Options: *c.options(), DryRun: c.DryRun}))
}
