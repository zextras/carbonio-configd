// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/zextras/carbonio-configd/internal/localconfig"
)

const errFmt = "Error: %v\n"

// LocalconfigCmd handles the "configd localconfig" subcommand.
type LocalconfigCmd struct {
	Mode          string   `short:"m" default:"plain" help:"Output mode: plain, shell, export, nokey, xml"`
	ConfigPath    string   `short:"c" default:"/opt/zextras/conf/localconfig.xml" help:"Path to localconfig.xml"`
	Quiet         bool     `short:"q" help:"Quiet mode (suppress warnings)"`
	ShowPath      bool     `short:"p" help:"Print config file path and exit"`
	ShowPasswords bool     `short:"s" help:"Show password values (default: masked)"`
	ShowDefaults  bool     `short:"d" help:"Show default values instead of current"`
	ShowChanged   bool     `short:"n" help:"Show only keys changed from defaults"`
	Edit          bool     `short:"e" help:"Edit: set key=value pairs"`
	Unset         bool     `short:"u" help:"Unset (remove) keys"`
	Random        bool     `short:"r" help:"Set key to random password (use with -e)"`
	Force         bool     `short:"f" name:"force" help:"Allow editing dangerous keys (legacy zmlocalconfig -f)"`
	Expand        bool     `short:"x" hidden:"" help:"Expand variables (accepted for compatibility)"`
	Key           []string `short:"k" name:"key" help:"Key to retrieve (can be repeated)" sep:"none"`
	KeyArgs       []string `arg:"" optional:"" help:"Keys or key=value pairs"`
}

// Run executes the localconfig subcommand.
//
//nolint:unparam // Kong interface requires error return
func (c *LocalconfigCmd) Run() error {
	opts := &localconfigOpts{
		mode:          c.Mode,
		configPath:    c.ConfigPath,
		quiet:         c.Quiet,
		showPath:      c.ShowPath,
		showPasswords: c.ShowPasswords,
		showDefaults:  c.ShowDefaults,
		showChanged:   c.ShowChanged,
		edit:          c.Edit,
		unset:         c.Unset,
		random:        c.Random,
		force:         c.Force,
		keys:          append(c.Key, c.KeyArgs...),
	}

	if opts.showPath {
		fmt.Println(opts.configPath)
		return nil
	}

	if opts.random && !opts.edit {
		fmt.Fprintln(os.Stderr, "Error: -r must be used with -e")
		os.Exit(1)
	}

	if opts.edit {
		handleEdit(opts)
		return nil
	}

	if opts.unset {
		handleUnset(opts)
		return nil
	}

	config := loadConfig(opts)
	config = applyFilters(config, opts)
	config = filterKeys(config, opts)

	if !opts.showPasswords {
		config = localconfig.MaskPasswords(config)
	}

	writeOutput(config, opts)

	return nil
}

type localconfigOpts struct {
	mode          string
	configPath    string
	quiet         bool
	showPath      bool
	showPasswords bool
	showDefaults  bool
	showChanged   bool
	edit          bool
	unset         bool
	random        bool
	force         bool
	keys          []string
}

// dangerousKeys lists keys that require -force to edit.
var dangerousKeys = map[string]bool{
	"zimbra_ldap_password":                 true,
	"ldap_root_password":                   true,
	"zimbra_mysql_password":                true,
	"mysql_root_password":                  true,
	"zimbra_ldap_userdn":                   true,
	"ldap_url":                             true,
	"ldap_master_url":                      true,
	"zimbra_server_hostname":               true,
	"zimbra_require_interprocess_security": true,
}

func handleEdit(opts *localconfigOpts) {
	if len(opts.keys) == 0 {
		fmt.Fprintln(os.Stderr, "Error: insufficient arguments")
		os.Exit(1)
	}

	for _, arg := range opts.keys {
		key, value := resolveEditKeyValue(arg, opts.random)

		if dangerousKeys[key] && !opts.force {
			fmt.Fprintf(os.Stderr, "Error: can not edit key %s\n", key)
			os.Exit(1)
		}

		if err := localconfig.SetKey(opts.configPath, key, value); err != nil {
			fmt.Fprintf(os.Stderr, errFmt, err)
			os.Exit(1)
		}
	}
}

// resolveEditKeyValue returns the key and value for a single edit argument.
// When random is true, the arg is treated as a key and a password is generated.
// Otherwise, the arg must be in "key=value" form.
func resolveEditKeyValue(arg string, random bool) (key, value string) {
	if random {
		pw, err := localconfig.GeneratePassword(64)
		if err != nil {
			fmt.Fprintf(os.Stderr, errFmt, err)
			os.Exit(1)
		}

		return arg, pw
	}

	eqIdx := strings.IndexByte(arg, '=')
	if eqIdx <= 0 {
		fmt.Fprintf(os.Stderr, "Error: argument %q not in key=value form\n", arg)
		os.Exit(1)
	}

	return arg[:eqIdx], arg[eqIdx+1:]
}

func handleUnset(opts *localconfigOpts) {
	if len(opts.keys) == 0 {
		fmt.Fprintln(os.Stderr, "Error: insufficient arguments")
		os.Exit(1)
	}

	for _, key := range opts.keys {
		if err := localconfig.RemoveKey(opts.configPath, key); err != nil {
			fmt.Fprintf(os.Stderr, errFmt, err)
			os.Exit(1)
		}
	}
}

func loadConfig(opts *localconfigOpts) map[string]string {
	config, err := localconfig.LoadResolvedConfigFromFile(opts.configPath)
	if err != nil {
		if !opts.quiet {
			fmt.Fprintf(os.Stderr, errFmt, err)
		}

		os.Exit(1)
	}

	return config
}

func applyFilters(config map[string]string, opts *localconfigOpts) map[string]string {
	if opts.showDefaults {
		defaults := make(map[string]string)
		localconfig.MergeDefaults(defaults)

		return defaults
	}

	if opts.showChanged {
		defaults := make(map[string]string)
		localconfig.MergeDefaults(defaults)

		changed := make(map[string]string)

		for k, v := range config {
			if dv, ok := defaults[k]; !ok || dv != v {
				changed[k] = v
			}
		}

		return changed
	}

	return config
}

func filterKeys(config map[string]string, opts *localconfigOpts) map[string]string {
	if len(opts.keys) == 0 {
		return config
	}

	filtered := make(map[string]string, len(opts.keys))
	for _, k := range opts.keys {
		if v, ok := config[k]; ok {
			filtered[k] = v
		} else if !opts.quiet {
			fmt.Fprintf(os.Stderr, "Warning: key %q not found\n", k)
		}
	}

	return filtered
}

func writeOutput(config map[string]string, opts *localconfigOpts) {
	switch opts.mode {
	case "plain":
		localconfig.FormatPlain(os.Stdout, config)
	case "shell":
		localconfig.FormatShell(os.Stdout, config)
	case "export":
		localconfig.FormatExport(os.Stdout, config)
	case "nokey":
		localconfig.FormatNokey(os.Stdout, config, opts.keys)
	case "xml":
		if err := localconfig.FormatXML(os.Stdout, config); err != nil {
			fmt.Fprintf(os.Stderr, errFmt, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr,
			"Error: unknown mode %q (use plain, shell, export, nokey, xml)\n", opts.mode)
		os.Exit(1)
	}
}
