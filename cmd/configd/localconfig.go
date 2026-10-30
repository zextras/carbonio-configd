// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/localconfig"
)

const (
	errFmt     = "Error: %v\n"
	modePlain  = "plain"
	modeShell  = "shell"
	modeExport = "export"
	modeNokey  = "nokey"
)

// LocalconfigCmd handles the "configd localconfig" subcommand.
//
// Flag letters, compatibility rules and messages mirror the legacy
// LocalConfigCLI (carbonio-mailbox 4.36.6); see localconfig_compat.go.
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
	info          bool
	all           bool
	reload        bool
	keys          []string

	// given lists the legacy option ids present on the command line.
	given []string

	// stdout and stderr override the process streams (nil: use them).
	stdout io.Writer
	stderr io.Writer
}

func (o *localconfigOpts) out() io.Writer {
	if o.stdout != nil {
		return o.stdout
	}

	return os.Stdout
}

func (o *localconfigOpts) errOut() io.Writer {
	if o.stderr != nil {
		return o.stderr
	}

	return os.Stderr
}

// Run executes the localconfig subcommand.
//
//nolint:unparam // Kong interface requires error return
func (c *LocalconfigCmd) Run() error {
	opts := c.options(os.Args)

	if err := opts.execute(); err != nil {
		opts.reportFailure(err)
		os.Exit(1)
	}

	return nil
}

// reportFailure prints err like legacy Logging.error ("Error: <msg>" on stderr).
// -q silences it; the exit status does not depend on it.
func (o *localconfigOpts) reportFailure(err error) {
	if o.quiet {
		return
	}

	_, _ = fmt.Fprintf(o.errOut(), errFmt, err)

	var failure *lcFailure
	if errors.As(err, &failure) && failure.detail != "" {
		_, _ = fmt.Fprintln(o.errOut(), failure.detail)
	}
}

// options converts the parsed flags into the options of one run. args is the
// process command line, used only to report conflicting options in the order
// the user typed them (legacy checkCompatibleOptions walks them in that order).
func (c *LocalconfigCmd) options(args []string) *localconfigOpts {
	configPath := c.ConfigPath
	if configPath == "" {
		configPath = basePath("conf", "localconfig.xml")
	}

	return &localconfigOpts{
		mode:          c.Mode,
		configPath:    configPath,
		quiet:         c.Quiet,
		showPath:      c.ShowPath,
		showPasswords: c.ShowPasswords,
		showDefaults:  c.ShowDefaults,
		showChanged:   c.ShowChanged,
		edit:          c.Edit,
		unset:         c.Unset,
		random:        c.Random,
		force:         c.Force,
		info:          c.Info,
		all:           c.All,
		reload:        c.Reload,
		keys:          append(slices.Clone(c.Key), c.KeyArgs...),
		given:         orderGivenOptions(c.givenOptions(), optionSequence(args)),
	}
}

// execute runs the operation selected by the options, in the legacy dispatch
// order: -i and --all (no config needed), then the config file is loaded, then
// -e, -u, -p, -l and finally printing. A non-nil error is a fatal legacy
// "Logging.error(...); System.exit(1)".
func (o *localconfigOpts) execute() error {
	switch {
	case o.info:
		return o.runInfo(optInfo, false)
	case o.all:
		return o.runInfo(optAll, true)
	}

	configured, err := o.loadConfigFile()
	if err != nil {
		return err
	}

	switch {
	case o.edit:
		return o.runEdit()
	case o.unset:
		return o.runUnset()
	case o.showPath:
		return o.runShowPath()
	case o.reload:
		return o.runReload()
	default:
		return o.runPrint(configured)
	}
}

// loadConfigFile reads the keys configured in the config file, like the legacy
// LocalConfig constructor: a missing or unreadable file only warns (and means
// an empty configuration); a malformed one is fatal.
func (o *localconfigOpts) loadConfigFile() (map[string]string, error) {
	configured, err := localconfig.LoadLocalConfigFromFile(o.configPath)

	switch {
	case err == nil:
		return configured, nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		o.warnf("local config file `%s' is not readable", o.configPath)

		return map[string]string{}, nil
	default:
		o.warnf("Problem parsing local config file '%s'", o.configPath)

		return nil, failDetail("failed when reading config file", err)
	}
}

// warnf prints a legacy Logging.warn line on stderr unless -q is set.
func (o *localconfigOpts) warnf(format string, args ...any) {
	if !o.quiet {
		_, _ = fmt.Fprintf(o.errOut(), "Warning: "+format+"\n", args...)
	}
}

// saveFailure is the legacy "save to <file> failed" error.
func (o *localconfigOpts) saveFailure(err error) error {
	return failDetail("save to "+o.configPath+" failed", err)
}

// runEdit sets every key=value argument and saves once: legacy sets all keys in
// memory and saves a single time, so a bad argument leaves the file untouched.
func (o *localconfigOpts) runEdit() error {
	if err := o.checkCompatible(optEdit, "qfrc"); err != nil {
		return err
	}

	if len(o.keys) == 0 {
		return failf("insufficient arguments")
	}

	pairs := make([]localconfig.Key, 0, len(o.keys))

	for _, arg := range o.keys {
		key, value, err := resolveEditKeyValue(arg, o.random)
		if err != nil {
			return err
		}

		if localconfig.IsProtected(key) && !o.force {
			return failf("can not edit key %s", key)
		}

		pairs = append(pairs, localconfig.Key{Name: key, Value: value})
	}

	if err := localconfig.SetKeys(o.configPath, pairs); err != nil {
		return o.saveFailure(err)
	}

	return nil
}

// resolveEditKeyValue returns the key and value for a single edit argument.
// When random is true, the arg is treated as a key and a password is generated.
// Otherwise, the arg must be in "key=value" form.
func resolveEditKeyValue(arg string, random bool) (key, value string, err error) {
	if random {
		pw, err := localconfig.GeneratePassword(64)
		if err != nil {
			return "", "", err
		}

		return arg, pw, nil
	}

	eqIdx := strings.IndexByte(arg, '=')
	if eqIdx <= 0 {
		return "", "", failf("argument '%s' not in key=value form", arg)
	}

	return arg[:eqIdx], arg[eqIdx+1:], nil
}

// runUnset removes every named key and saves once (all-or-nothing).
func (o *localconfigOpts) runUnset() error {
	if err := o.checkCompatible(optUnset, "qfc"); err != nil {
		return err
	}

	if len(o.keys) == 0 {
		return failf("insufficient arguments")
	}

	err := localconfig.RemoveKeys(o.configPath, o.keys)

	if notSet, ok := errors.AsType[*localconfig.KeyNotSetError](err); ok {
		return failf("%s", notSet)
	}

	if err != nil {
		return o.saveFailure(err)
	}

	return nil
}

func (o *localconfigOpts) runShowPath() error {
	if err := o.checkCompatible(optPath, "qc"); err != nil {
		return err
	}

	_, err := fmt.Fprintln(o.out(), o.configPath)

	return err
}

// runPrint prints the current values, the defaults (-d) or the changed keys (-n).
func (o *localconfigOpts) runPrint(configured map[string]string) error {
	// Legacy creates the writer before looking at -n/-d: a bad -m wins.
	if err := checkMode(o.mode); err != nil {
		return err
	}

	if err := o.checkCompatible(o.printOption(), "qscmx"); err != nil {
		return err
	}

	config := o.selectValues(configured)

	if !o.showPasswords {
		config = localconfig.MaskPasswords(config)
	}

	return writeOutput(config, o)
}

// printOption returns the legacy main option of the print branch ("" for the
// plain "current values" listing).
func (o *localconfigOpts) printOption() string {
	switch {
	case o.showChanged:
		return optChanged
	case o.showDefaults:
		return optDefault
	default:
		return ""
	}
}

// selectValues picks the entries printed by the legacy printChanged,
// printDefaults or print, given the keys configured in the file.
func (o *localconfigOpts) selectValues(configured map[string]string) map[string]string {
	switch {
	case o.showChanged:
		return o.changedValues(configured)
	case o.showDefaults:
		return o.defaultValues()
	default:
		return o.currentValues(configured)
	}
}

// resolve returns the configured keys over the compiled-in defaults, with every
// ${variable} expanded (legacy mExpanded + KnownKey.getValue).
func resolve(configured map[string]string) map[string]string {
	resolved := maps.Clone(configured)
	localconfig.MergeDefaults(resolved)
	localconfig.Interpolate(resolved)

	return resolved
}

// currentValues is legacy LocalConfig.print: every known or configured key, or
// the listed ones (a key with no value warns "null valued key").
func (o *localconfigOpts) currentValues(configured map[string]string) map[string]string {
	resolved := resolve(configured)
	if len(o.keys) == 0 {
		return resolved
	}

	picked := make(map[string]string, len(o.keys))

	for _, key := range o.keys {
		if value, ok := resolved[key]; ok {
			picked[key] = value
		} else {
			o.warnf("null valued key '%s'", key)
		}
	}

	return picked
}

// defaultValues is legacy LocalConfig.printDefaults: the raw compiled-in
// defaults of every known key, or of the listed ones (others warn).
func (o *localconfigOpts) defaultValues() map[string]string {
	defaults := make(map[string]string)
	localconfig.MergeDefaults(defaults)

	if len(o.keys) == 0 {
		return defaults
	}

	picked := make(map[string]string, len(o.keys))

	for _, key := range o.keys {
		if value, ok := defaults[key]; ok {
			picked[key] = value
		} else {
			o.warnf("not a known key '%s'", key)
		}
	}

	return picked
}

// changedValues is legacy LocalConfig.printChanged. The candidates are the keys
// configured in the file (or the listed ones, configured or not); a known key
// is dropped when its expanded configured value equals its raw, unexpanded
// default, so a default containing ${...} is reported as changed.
func (o *localconfigOpts) changedValues(configured map[string]string) map[string]string {
	resolved := resolve(configured)

	keys := o.keys
	if len(keys) == 0 {
		keys = slices.Sorted(maps.Keys(configured))
	}

	changed := make(map[string]string, len(keys))

	for _, key := range keys {
		if def, known := localconfig.Defaults[key]; known && resolved[key] == def {
			continue
		}

		if value, ok := resolved[key]; ok {
			changed[key] = value
		} else {
			o.warnf("null valued key '%s'", key)
		}
	}

	return changed
}

// outputModes are the formats accepted by -m (legacy ConfigWriter.getInstance).
var outputModes = []string{modePlain, modeShell, modeExport, modeNokey, outputFormatXML}

// checkMode rejects an unknown -m value with the legacy ConfigWriter message.
// An empty mode is the default (plain).
func checkMode(mode string) error {
	if mode == "" || slices.Contains(outputModes, mode) {
		return nil
	}

	return failf("failed to create writer %s", mode)
}

func writeOutput(config map[string]string, opts *localconfigOpts) error {
	w := opts.out()

	switch opts.mode {
	case modePlain, "":
		localconfig.FormatPlain(w, config)
	case modeShell:
		localconfig.FormatShell(w, config)
	case modeExport:
		localconfig.FormatExport(w, config)
	case modeNokey:
		localconfig.FormatNokey(w, config, opts.keys)
	case outputFormatXML:
		return localconfig.FormatXML(w, config)
	default:
		return checkMode(opts.mode)
	}

	return nil
}
