// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package bootstrapcmd implements "carbonio-bootstrap config": it resolves the
// bootstrap configuration from the YAML file (or a legacy setup.pl config
// file), the CARBONIO_BOOTSTRAP_* environment, the --set flags and the
// wizard, validates it, and prints or saves it.
package bootstrapcmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/zextras/carbonio-configd/internal/bootstrap"
	"github.com/zextras/carbonio-configd/internal/bootstrap/tui"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Wizard modes of the --wizard flag.
const (
	WizardMissing = "missing"
	WizardAll     = "all"
)

// saveMode is the permission of a saved configuration: it names the secret
// files, so it is kept private.
const saveMode = 0o600

// Options are the parsed flags of "carbonio-bootstrap config".
type Options struct {
	// ConfigPath is the YAML configuration file.
	ConfigPath string
	// LegacyPath is a setup.pl config file (KEY=VALUE), the -c of
	// carbonio-bootstrap.
	LegacyPath string
	// Sets are "key=value" overrides.
	Sets []string
	// NonInteractive never runs the wizard.
	NonInteractive bool
	// Wizard is WizardMissing or WizardAll.
	Wizard string
	// Output saves the configuration there instead of printing it.
	Output string
	// Plain keeps the line wizard and the line output on a terminal.
	Plain bool
	// Verbose lists every legacy key that is not imported.
	Verbose bool
	// Environ is the process environment ("NAME=value").
	Environ []string
}

// LegacyDrops collects the legacy keys a run did not import, for the caller
// to report (nil Deps.Drops: the command reports them itself on stderr).
type LegacyDrops struct {
	List []bootstrap.LegacyDrop
}

// Deps are the parts of the node the command reads besides sys.System.
type Deps struct {
	Host     bootstrap.Host
	Resolver bootstrap.Resolver
	// Terminal reports whether the wizard can run: stdin and stdout are a
	// terminal.
	Terminal bool
	// ReadSecret reads a line without echo (nil: plain line).
	ReadSecret func() (string, error)
	// Existing are the values of an existing install (key path to value),
	// ranked below every operator layer.
	Existing map[string]string
	// Screens are the full-screen pages; nil when the terminal cannot show
	// them (the line wizard and the plain output are used).
	Screens *Screens
	// Session is what the screens of "run" share (nil for "config").
	Session *Session
	// Drops, if set, collects the legacy keys not imported instead of
	// printing a note about them.
	Drops *LegacyDrops
	// LogPath is the log file of the run ("" in a dry run).
	LogPath string
	// Display is how the plain output of "run" is drawn.
	Display Display
}

// Display is what the plain output of "run" may use of the terminal.
type Display struct {
	// Live redraws a line for the running step; Color paints the statuses.
	Live, Color bool
	// Width is the terminal width (0: unknown).
	Width int
}

// displayOf tells what w, the output of "run", can show: nothing fancy
// unless it is a terminal that is not "dumb"; colour also needs NO_COLOR
// unset.
func displayOf(w io.Writer, environ []string) Display {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return Display{}
	}

	if slices.Contains(environ, "TERM=dumb") {
		return Display{}
	}

	d := Display{Live: true, Color: !noColor(environ)}
	if width, _, err := term.GetSize(int(f.Fd())); err == nil {
		d.Width = width
	}

	return d
}

// useScreens: the full-screen pages replace the line wizard and the line
// output unless the operator or the terminal asks for plain text.
func useScreens(opts *Options, deps *Deps) bool {
	return deps.Screens != nil && !opts.Plain && !opts.NonInteractive
}

// Run executes the command and returns the exit status: 0 when the
// configuration is valid, 1 otherwise.
func Run(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer, opts *Options) int {
	deps := realDeps(ctx, env, stdin, stdout)

	return run(ctx, env, stdin, stdout, stderr, opts, &deps)
}

func realDeps(ctx context.Context, env sys.System, stdin io.Reader, stdout io.Writer) Deps {
	d := Deps{
		Host: &systemHost{ctx: ctx, env: env}, Resolver: net.DefaultResolver,
		Display: displayOf(stdout, os.Environ()),
	}

	in, inOK := stdin.(*os.File)
	out, outOK := stdout.(*os.File)

	if inOK && outOK && term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd())) {
		d.Terminal = true

		if os.Getenv("TERM") != "dumb" {
			d.Screens = &Screens{Configure: tui.Configure, Apply: tui.Apply}
		}

		d.ReadSecret = func() (string, error) {
			b, err := term.ReadPassword(int(in.Fd()))

			return string(b), err
		}
	}

	return d
}

func run(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer,
	opts *Options, deps *Deps,
) int {
	r, ok := resolveValid(ctx, env, stdin, stdout, stderr, opts, deps)
	if !ok {
		return 1
	}

	if opts.Output == "" {
		out, err := r.Render(bootstrap.Print)
		if err != nil {
			printErrors(stderr, err)

			return 1
		}

		_, _ = stdout.Write(out)

		return 0
	}

	if err := save(env, r, opts.Output); err != nil {
		printErrors(stderr, err)

		return 1
	}

	_, _ = fmt.Fprintf(stdout, "Configuration saved to %s\n", opts.Output)

	return 0
}

// resolveValid resolves and validates the configuration, printing the
// errors to stderr.
func resolveValid(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer,
	opts *Options, deps *Deps,
) (*bootstrap.Resolved, bool) {
	r, err := resolve(ctx, env, stdin, stdout, stderr, opts, deps)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "Error: cannot build the bootstrap configuration:")
		printErrors(stderr, err)

		return nil, false
	}

	if err := bootstrap.Validate(&r.Config); err != nil {
		_, _ = fmt.Fprintln(stderr, "Error: the bootstrap configuration is not valid:")
		printErrors(stderr, err)

		return nil, false
	}

	return r, true
}

// resolve builds the configuration from every layer, running the wizard
// when it applies.
func resolve(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer,
	opts *Options, deps *Deps,
) (*bootstrap.Resolved, error) {
	if opts.ConfigPath != "" && opts.LegacyPath != "" {
		return nil, errors.New("--config and --legacy-config are mutually exclusive")
	}

	r := bootstrap.NewResolved(env.FS.ReadFile)

	if err := applyLayers(env, r, stderr, opts, deps); err != nil {
		return nil, err
	}

	if !wantWizard(r, opts, deps) {
		if opts.Wizard == WizardAll && !opts.NonInteractive {
			return nil, errors.New("--wizard=all needs a terminal")
		}

		return r, nil
	}

	if err := runWizard(ctx, env, stdin, stdout, r, opts, deps); err != nil {
		return nil, err
	}

	return r, nil
}

// applyLayers applies the existing install, the files, the environment, the
// --set flags and the defaults, in increasing precedence.
func applyLayers(env sys.System, r *bootstrap.Resolved, stderr io.Writer, opts *Options, deps *Deps) error {
	if err := r.ApplyExisting(deps.Existing); err != nil {
		return err
	}

	if err := applyFiles(env, r, stderr, opts, deps.Drops); err != nil {
		return err
	}

	if err := r.ApplyEnv(opts.Environ); err != nil {
		return err
	}

	if err := r.ApplySets(opts.Sets); err != nil {
		return err
	}

	return r.ApplyDefaults(deps.Host)
}

// runWizard asks the questions and, unless the configuration goes to
// --output, offers to save the answers.
func runWizard(ctx context.Context, env sys.System, stdin io.Reader, stdout io.Writer,
	r *bootstrap.Resolved, opts *Options, deps *Deps,
) error {
	if useScreens(opts, deps) {
		return runConfigure(ctx, env, stdin, stdout, r, opts, deps)
	}

	w := &bootstrap.Wizard{
		In:         bufio.NewReader(stdin),
		Out:        stdout,
		ReadSecret: deps.ReadSecret,
		Resolver:   deps.Resolver,
		Host:       deps.Host,
	}

	mode := bootstrap.AskUnset
	if opts.Wizard == WizardAll {
		mode = bootstrap.AskAll
	}

	if err := w.Run(ctx, r, mode); err != nil {
		return err
	}

	if opts.Output == "" {
		return offerSave(env, w, r)
	}

	return nil
}

// applyFiles applies the legacy file or the YAML file, reporting the legacy
// keys that are not imported: to drops, or on stderr as one summary line
// (every key with --verbose).
func applyFiles(env sys.System, r *bootstrap.Resolved, stderr io.Writer, opts *Options, drops *LegacyDrops) error {
	if opts.LegacyPath != "" {
		data, err := env.FS.ReadFile(opts.LegacyPath)
		if err != nil {
			return err
		}

		dropped, err := r.ApplyLegacy(data)
		if err != nil {
			return inFile(opts.LegacyPath, err)
		}

		if drops != nil {
			drops.List = dropped
		} else {
			noteDrops(stderr, dropped, opts.Verbose)
		}
	}

	return applyYAML(env, r, opts)
}

func applyYAML(env sys.System, r *bootstrap.Resolved, opts *Options) error {
	if opts.ConfigPath == "" {
		return nil
	}

	data, err := env.FS.ReadFile(opts.ConfigPath)
	if err != nil {
		return err
	}

	if err := r.ApplyYAML(data); err != nil {
		return inFile(opts.ConfigPath, err)
	}

	return nil
}

// noteDrops tells on w how many legacy keys were not imported, and which
// with verbose.
func noteDrops(w io.Writer, drops []bootstrap.LegacyDrop, verbose bool) {
	if len(drops) == 0 {
		return
	}

	if !verbose {
		noun := "keys"
		if len(drops) == 1 {
			noun = "key"
		}

		_, _ = fmt.Fprintf(w, "Note: %d legacy %s not imported (--verbose lists them)\n", len(drops), noun)

		return
	}

	for _, d := range drops {
		_, _ = fmt.Fprintf(w, "Note: legacy key %s not imported: %s\n", d.Key, d.Reason)
	}
}

// wantWizard: on a terminal, unless --non-interactive, the wizard runs when
// a required key is missing, when no file, environment variable or flag gave
// any key, or on --wizard=all.
func wantWizard(r *bootstrap.Resolved, opts *Options, deps *Deps) bool {
	if opts.NonInteractive || !deps.Terminal {
		return false
	}

	if opts.Wizard == WizardAll || len(bootstrap.Missing(&r.Config)) > 0 {
		return true
	}

	for _, f := range bootstrap.Fields {
		if r.Source(f.Path) >= bootstrap.SourceLegacy {
			return false
		}
	}

	return true
}

func offerSave(env sys.System, w *bootstrap.Wizard, r *bootstrap.Resolved) error {
	_, _ = fmt.Fprint(w.Out, "Save these answers to a file (no passwords are written)? [path, Enter: skip]: ")

	path, err := w.In.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	if path = strings.TrimSpace(path); path == "" {
		return nil
	}

	if err := save(env, r, path); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(w.Out, "Configuration saved to %s\n", path)

	return nil
}

func save(env sys.System, r *bootstrap.Resolved, path string) error {
	data, err := r.Render(bootstrap.Save)
	if err != nil {
		return err
	}

	return env.FS.WriteFileAtomic(path, data, saveMode)
}

// inFile prefixes every error joined in err with the file it is about.
func inFile(path string, err error) error {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return fmt.Errorf("%s: %w", path, err)
	}

	errs := joined.Unwrap()
	out := make([]error, len(errs))

	for i, e := range errs {
		out[i] = fmt.Errorf("%s: %w", path, e)
	}

	return errors.Join(out...)
}

// printErrors writes err one problem per line (errors.Join puts each joined
// error on its own line).
func printErrors(w io.Writer, err error) {
	for line := range strings.SplitSeq(err.Error(), "\n") {
		_, _ = fmt.Fprintf(w, "  %s\n", line)
	}
}
