// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package localconfig implements the "configd localconfig" command line: a
// drop-in port of the legacy zmlocalconfig (LocalConfigCLI). It owns flag
// compatibility checks, output formats and exit status; the cmd/configd
// subcommand only maps parsed flags onto Options.
package localconfig

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	lcfg "github.com/zextras/carbonio-configd/internal/localconfig"
)

const (
	errFmt     = "Error: %v\n"
	modePlain  = "plain"
	modeShell  = "shell"
	modeExport = "export"
	modeNokey  = "nokey"
	modeXML    = "xml"
)

// Options are the parsed command-line flags of "configd localconfig".
//
// Flag letters, compatibility rules and messages mirror the legacy
// LocalConfigCLI (carbonio-mailbox 4.36.6); see compat.go.
type Options struct {
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

	// Keys are the keys (or key=value pairs) to operate on: the -k values
	// followed by the positional arguments.
	Keys []string

	// DefaultPath is the node's own localconfig.xml. It is used when
	// ConfigPath is empty and, always, by -l.
	DefaultPath string

	// Args is the process command line, used only to report conflicting
	// options in the order the user typed them (legacy checkCompatibleOptions
	// walks them in that order).
	Args []string
}

type localconfigOpts struct {
	mode           string
	configPath     string
	nodeConfigPath string
	quiet          bool
	showPath       bool
	showPasswords  bool
	showDefaults   bool
	showChanged    bool
	edit           bool
	unset          bool
	random         bool
	force          bool
	expand         bool

	// nullKeys are the keys of the file without a <value>.
	nullKeys []string
	info     bool
	all      bool
	reload   bool
	keys     []string

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

// Run executes the localconfig subcommand and returns the process exit status:
// 0 on success, 1 on a fatal legacy error (printed on stderr unless -q).
func Run(ctx context.Context, stdout, stderr io.Writer, opts *Options) int {
	o := opts.command(stdout, stderr)

	if err := o.execute(ctx); err != nil {
		o.reportFailure(err)

		return 1
	}

	return 0
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

// command converts the parsed flags into the state of one run.
func (c *Options) command(stdout, stderr io.Writer) *localconfigOpts {
	configPath := c.ConfigPath
	if configPath == "" {
		configPath = c.DefaultPath
	}

	return &localconfigOpts{
		mode:           c.Mode,
		configPath:     configPath,
		nodeConfigPath: c.DefaultPath,
		quiet:          c.Quiet,
		showPath:       c.ShowPath,
		showPasswords:  c.ShowPasswords,
		showDefaults:   c.ShowDefaults,
		showChanged:    c.ShowChanged,
		edit:           c.Edit,
		unset:          c.Unset,
		random:         c.Random,
		force:          c.Force,
		expand:         c.Expand,
		info:           c.Info,
		all:            c.All,
		reload:         c.Reload,
		keys:           slices.Clone(c.Keys),
		given:          orderGivenOptions(c.givenOptions(), optionSequence(c.Args)),
		stdout:         stdout,
		stderr:         stderr,
	}
}

// execute runs the operation selected by the options, in the legacy dispatch
// order: -i and --all (no config needed), then the config file is loaded, then
// -e, -u, -p, -l and finally printing. A non-nil error is a fatal legacy
// "Logging.error(...); System.exit(1)".
func (o *localconfigOpts) execute(ctx context.Context) error {
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
		return o.runReload(ctx)
	default:
		return o.runPrint(configured)
	}
}

// loadConfigFile reads the keys configured in the config file, like the legacy
// LocalConfig constructor: a missing or unreadable file only warns (and means
// an empty configuration); a malformed one is fatal.
func (o *localconfigOpts) loadConfigFile() (map[string]string, error) {
	configured, nullKeys, err := lcfg.LoadLocalConfigFile(o.configPath)
	o.nullKeys = nullKeys

	switch {
	case err == nil:
		// Legacy expands every key while loading: one bad reference fails
		// every invocation, even -e.
		if err := lcfg.NewLegacy(configured).Validate(); err != nil {
			return nil, failDetail("failed with error in config file", err)
		}

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

	pairs := make([]lcfg.Key, 0, len(o.keys))

	for _, arg := range o.keys {
		key, value, err := resolveEditKeyValue(arg, o.random)
		if err != nil {
			return err
		}

		if lcfg.IsProtected(key) && !o.force {
			return failf("can not edit key %s", key)
		}

		pairs = append(pairs, lcfg.Key{Name: key, Value: value})
	}

	if err := lcfg.SetKeys(o.configPath, pairs); err != nil {
		return o.saveFailure(err)
	}

	return nil
}

// resolveEditKeyValue returns the key and value for a single edit argument.
// When random is true, the arg is treated as a key and a password is generated.
// Otherwise, the arg must be in "key=value" form.
func resolveEditKeyValue(arg string, random bool) (key, value string, err error) {
	if random {
		pw, err := lcfg.GeneratePassword(64)
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

	err := lcfg.RemoveKeys(o.configPath, o.keys)

	if notSet, ok := errors.AsType[*lcfg.KeyNotSetError](err); ok {
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

	// Legacy ConfigWriter: shell and export never hide passwords.
	if o.hidePasswords() {
		config = lcfg.MaskPasswords(config)
	}

	return writeOutput(config, o)
}

// expandValues reports whether values are printed expanded: always for the
// shell and export writers, otherwise only with -x.
func (o *localconfigOpts) expandValues() bool {
	return o.expand || o.mode == modeShell || o.mode == modeExport
}

// hidePasswords reports whether "password" keys are masked: every writer but
// shell and export, unless -s.
func (o *localconfigOpts) hidePasswords() bool {
	return !o.showPasswords && o.mode != modeShell && o.mode != modeExport
}

// value is the legacy writer.expand() ? get(key) : getRaw(key); ok is false
// for a key without a value.
func (o *localconfigOpts) value(lc *lcfg.Legacy, key string) (string, bool) {
	if o.expandValues() {
		v, ok, _ := lc.Expand(key) // validated at load time

		return v, ok
	}

	return lc.Raw(key)
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
	lc := lcfg.NewLegacy(configured)

	switch {
	case o.showChanged:
		return o.changedValues(lc, configured)
	case o.showDefaults:
		return o.defaultValues(lc)
	default:
		return o.currentValues(lc)
	}
}

// currentValues is legacy LocalConfig.print: every known or configured key, or
// the listed ones (a listed key with no value warns "null valued key"; in the
// full listing such keys are silently skipped).
func (o *localconfigOpts) currentValues(lc *lcfg.Legacy) map[string]string {
	if len(o.keys) == 0 {
		all := make(map[string]string)

		for _, key := range lc.Keys() {
			if v, ok := o.value(lc, key); ok {
				all[key] = v
			}
		}

		return all
	}

	picked := make(map[string]string, len(o.keys))

	for _, key := range o.keys {
		if value, ok := o.value(lc, key); ok {
			picked[key] = value
		} else {
			o.warnf("null valued key '%s'", key)
		}
	}

	return picked
}

// defaultValues is legacy LocalConfig.printDefaults: the compiled-in defaults
// of every known key, or of the listed ones (others warn). A null default is
// skipped silently. With -x the default is expanded against the configured
// values, like legacy.
func (o *localconfigOpts) defaultValues(lc *lcfg.Legacy) map[string]string {
	keys := o.keys
	if len(keys) == 0 {
		keys = slices.Sorted(maps.Keys(lcfg.Defaults))
	}

	picked := make(map[string]string, len(keys))

	for _, key := range keys {
		def, ok := lcfg.Defaults[key]

		switch {
		case !ok && lcfg.IsNullKey(key):
		case !ok:
			o.warnf("not a known key '%s'", key)
		case o.expandValues():
			picked[key], _, _ = lc.Expand(key)
		default:
			picked[key] = def
		}
	}

	return picked
}

// changedValues is legacy LocalConfig.printChanged. The candidates are the keys
// configured in the file (or the listed ones, configured or not); a known key
// is dropped when its expanded configured value equals its raw, unexpanded
// default, so a default containing ${...} is reported as changed.
func (o *localconfigOpts) changedValues(lc *lcfg.Legacy, configured map[string]string) map[string]string {
	keys := o.keys
	if len(keys) == 0 {
		keys = slices.Sorted(maps.Keys(configured))
		keys = slices.Sorted(slices.Values(append(keys, o.nullKeys...)))
	}

	changed := make(map[string]string, len(keys))

	for _, key := range keys {
		if def, known := lcfg.Defaults[key]; known {
			if expanded, ok, _ := lc.Expand(key); ok && expanded == def {
				continue
			}
		}

		if value, ok := o.value(lc, key); ok {
			changed[key] = value
		} else {
			o.warnf("null valued key '%s'", key)
		}
	}

	return changed
}

// outputModes are the formats accepted by -m (legacy ConfigWriter.getInstance).
var outputModes = []string{modePlain, modeShell, modeExport, modeNokey, modeXML}

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

	// Listed keys print in the order they were given; no keys: sorted.
	switch opts.mode {
	case modePlain, "":
		lcfg.FormatPlain(w, config, opts.keys)
	case modeShell:
		lcfg.FormatShell(w, config, opts.keys)
	case modeExport:
		lcfg.FormatExport(w, config, opts.keys)
	case modeNokey:
		lcfg.FormatNokey(w, config, opts.keys)
	case modeXML:
		return lcfg.FormatXML(w, config, opts.keys)
	default:
		return checkMode(opts.mode)
	}

	return nil
}
