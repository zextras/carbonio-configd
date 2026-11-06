// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	lccli "github.com/zextras/carbonio-configd/internal/cli/localconfig"
)

// LocalconfigCmd handles the "configd localconfig" subcommand.
//
// Flag letters, compatibility rules and messages mirror the legacy
// LocalConfigCLI (carbonio-mailbox 4.36.6); the logic lives in
// internal/cli/localconfig.
type LocalconfigCmd struct {
	Mode          string
	ConfigPath    string
	Quiet         bool
	ShowPath      bool
	ShowPasswords bool
	ShowDefaults  bool
	ShowChanged   bool
	Edit          bool
	Unset         bool
	Random        bool
	Force         bool
	Expand        bool
	Info          bool
	All           bool
	Reload        bool
	Key           []string
	KeyArgs       []string
}

// localconfigModes are the output modes of --mode.
var localconfigModes = []string{"plain", "shell", "export", "nokey", "xml"}

func (a *app) localconfigCmd() *cobra.Command {
	c := &a.cli.Localconfig

	cmd := a.leaf("localconfig [<key-args> ...]", "Manage local configuration",
		posArgs(nil, nil, true),
		func(_ *cobra.Command, args []string) error {
			c.KeyArgs = args

			return nil
		},
		func() error { return c.Run(a.std) })

	fs := cmd.Flags()
	fs.SortFlags = false
	// The long names of the legacy zmlocalconfig keep working next to the names
	// of the fields.
	flagAliases(fs, map[string]string{
		"format": "mode", "config": "config-path", "path": "show-path", "show": "show-passwords",
		"default": "show-defaults", "changed": "show-changed",
	})

	fs.StringVarP(&c.Mode, "mode", "m", "", "Output mode: plain (default), shell, export, nokey, xml")
	fs.StringVarP(&c.ConfigPath, "config-path", "c", "", "Path to localconfig.xml")
	fs.BoolVarP(&c.Quiet, "quiet", "q", false, "Quiet mode (suppress warnings and errors)")
	fs.BoolVarP(&c.ShowPath, "show-path", "p", false, "Print config file path and exit")
	fs.BoolVarP(&c.ShowPasswords, "show-passwords", "s", false, "Show password values (default: masked)")
	fs.BoolVarP(&c.ShowDefaults, "show-defaults", "d", false, "Show default values instead of current")
	fs.BoolVarP(&c.ShowChanged, "show-changed", "n", false, "Show only keys changed from defaults")
	fs.BoolVarP(&c.Edit, "edit", "e", false, "Edit: set key=value pairs")
	fs.BoolVarP(&c.Unset, "unset", "u", false, "Unset (remove) keys")
	fs.BoolVarP(&c.Random, "random", "r", false, "Set key to random password (use with -e)")
	fs.BoolVarP(&c.Force, "force", "f", false, "Allow editing dangerous keys (legacy zmlocalconfig -f)")
	fs.BoolVarP(&c.Expand, "expand", "x", false, "Expand ${key} references (shell and export always expand)")
	fs.BoolVarP(&c.Info, "info", "i", false, "Show documentation for the keys listed in [args]")
	fs.BoolVar(&c.All, "all", false, "Show documentation for all keys, including unsupported ones")
	fs.BoolVarP(&c.Reload, "reload", "l", false, "Send a SOAP request to the server to reload its local config")
	fs.StringArrayVarP(&c.Key, "key", "k", nil, "Key to retrieve (can be repeated)")
	_ = fs.MarkHidden("all")

	mustCompleteFlag(cmd, "mode", staticCompletion(localconfigModes...))
	mustCompleteFlag(cmd, "key", completeLocalconfigKeys)
	cmd.ValidArgsFunction = completeLocalconfigKeys

	return describeArgs(cmd, [2]string{"[<key-args> ...]", "Keys or key=value pairs"})
}

// Run executes the localconfig subcommand; a failure exits the process with
// the status chosen by the CLI.
func (c *LocalconfigCmd) Run(std *stdio) error {
	return exitWith(lccli.Run(context.Background(), std.Out, std.Err, c.options(std.Args)))
}

func (c *LocalconfigCmd) options(args []string) *lccli.Options {
	keys := make([]string, 0, len(c.Key)+len(c.KeyArgs))
	keys = append(append(keys, c.Key...), c.KeyArgs...)

	return &lccli.Options{
		Mode:          c.Mode,
		ConfigPath:    c.ConfigPath,
		Quiet:         c.Quiet,
		ShowPath:      c.ShowPath,
		ShowPasswords: c.ShowPasswords,
		ShowDefaults:  c.ShowDefaults,
		ShowChanged:   c.ShowChanged,
		Edit:          c.Edit,
		Unset:         c.Unset,
		Random:        c.Random,
		Force:         c.Force,
		Expand:        c.Expand,
		Info:          c.Info,
		All:           c.All,
		Reload:        c.Reload,
		Keys:          keys,
		DefaultPath:   cliutil.BasePath("conf", "localconfig.xml"),
		Args:          args,
	}
}
