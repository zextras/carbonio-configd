// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	lccli "github.com/zextras/carbonio-configd/internal/cli/localconfig"
)

// LocalconfigCmd handles the "configd localconfig" subcommand.
//
// Flag letters, compatibility rules and messages mirror the legacy
// LocalConfigCLI (carbonio-mailbox 4.36.6); the logic lives in
// internal/cli/localconfig.
type LocalconfigCmd struct {
	Mode          string   `short:"m" aliases:"format" help:"Output mode: plain (default), shell, export, nokey, xml"`
	ConfigPath    string   `short:"c" aliases:"config" help:"Path to localconfig.xml"`
	Quiet         bool     `short:"q" help:"Quiet mode (suppress warnings and errors)"`
	ShowPath      bool     `short:"p" aliases:"path" help:"Print config file path and exit"`
	ShowPasswords bool     `short:"s" aliases:"show" help:"Show password values (default: masked)"`
	ShowDefaults  bool     `short:"d" aliases:"default" help:"Show default values instead of current"`
	ShowChanged   bool     `short:"n" aliases:"changed" help:"Show only keys changed from defaults"`
	Edit          bool     `short:"e" help:"Edit: set key=value pairs"`
	Unset         bool     `short:"u" help:"Unset (remove) keys"`
	Random        bool     `short:"r" help:"Set key to random password (use with -e)"`
	Force         bool     `short:"f" name:"force" help:"Allow editing dangerous keys (legacy zmlocalconfig -f)"`
	Expand        bool     `short:"x" hidden:"" help:"Expand variables (accepted for compatibility)"`
	Info          bool     `short:"i" name:"info" help:"Show documentation for the keys listed in [args]"`
	All           bool     `name:"all" hidden:"" help:"Show documentation for all keys, including unsupported ones"`
	Reload        bool     `short:"l" name:"reload" help:"Send a SOAP request to the server to reload its local config"`
	Key           []string `short:"k" name:"key" help:"Key to retrieve (can be repeated)" sep:"none"`
	KeyArgs       []string `arg:"" optional:"" help:"Keys or key=value pairs"`
}

// Run executes the localconfig subcommand; a failure exits the process with
// the status chosen by the CLI.
func (c *LocalconfigCmd) Run() error {
	return exitWith(lccli.Run(context.Background(), os.Stdout, os.Stderr, c.options(os.Args)))
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
