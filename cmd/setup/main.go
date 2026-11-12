// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command carbonio-bootstrap configures a Carbonio node: it resolves the
// bootstrap configuration (YAML file, environment, flags, wizard) and runs
// the bootstrap steps.
//
//	carbonio-bootstrap [run]   configure this node
//	carbonio-bootstrap config  resolve, validate and print or save the configuration
//
// It is a binary of its own, apart from configd, so the full-screen terminal
// interface is not linked into the daemon.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/bootstrapcmd"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Exit statuses besides 0 and the status of the command itself.
const exitUsage = 2

// flags are the options of "config" and "run".
type flags struct {
	config, legacy, output string
	sets                   []string
	nonInteractive, plain  bool
	wizard                 string
	dryRun, verbose        bool
}

func main() {
	// The bootstrap reports through its own step output and log file; the
	// shared packages' routine INFO lines would only clutter the terminal
	// (and break the full-screen interface), so they log warnings only.
	logger.InitStructuredLogging(&logger.Config{Format: logger.FormatText, Level: slog.LevelWarn, Output: os.Stderr})
	os.Exit(run(os.Args[1:], sys.Real(), os.Stdin, os.Stdout, os.Stderr))
}

// run is main with the process boundary injected: it returns the exit status.
func run(args []string, env sys.System, stdin io.Reader, stdout, stderr io.Writer) int {
	status := 0
	root := newRootCmd(env, stdin, stdout, stderr, &status)
	root.SetArgs(legacyArgs(args))
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return exitUsage
	}

	return status
}

// legacyArgs maps the options of the former shell script: "-c FILE" is
// "--legacy-config FILE --non-interactive", and "-d" (debug) is accepted and
// ignored, the run always logs to /opt/zextras/log/bootstrap.*.log.
func legacyArgs(args []string) []string {
	out := make([]string, 0, len(args)+1)

	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-d":
		case a == "-c" && i+1 < len(args):
			out = append(out, "--legacy-config", args[i+1], "--non-interactive")
			i++
		default:
			out = append(out, a)
		}
	}

	return out
}

func newRootCmd(env sys.System, stdin io.Reader, stdout, stderr io.Writer, status *int) *cobra.Command {
	var rf flags

	root := &cobra.Command{
		Use:   "carbonio-bootstrap [command]",
		Short: "Configure this Carbonio node",
		Long: "Configure this Carbonio node: without a command it runs the bootstrap, " +
			"like \"carbonio-bootstrap run\".",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			return runSetup(&rf, env, stdin, stdout, stderr, status)
		},
	}

	addFlags(root, &rf, true)

	var cf flags

	config := &cobra.Command{
		Use:   "config",
		Short: "Resolve, validate and print or save the bootstrap configuration",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := cf.check(); err != nil {
				return err
			}

			*status = bootstrapcmd.Run(context.Background(), env, stdin, stdout, stderr, cf.options())

			return nil
		},
	}

	addFlags(config, &cf, false)

	var sf flags

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Configure this node (the work of the legacy setup.pl)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runSetup(&sf, env, stdin, stdout, stderr, status)
		},
	}

	addFlags(runCmd, &sf, true)
	root.AddCommand(config, runCmd)

	return root
}

func addFlags(cmd *cobra.Command, f *flags, run bool) {
	fs := cmd.Flags()
	fs.SortFlags = false
	fs.StringVar(&f.config, "config", "", "YAML configuration file")
	fs.StringVar(&f.legacy, "legacy-config", "", "setup.pl configuration file (KEY=VALUE) to import")
	fs.StringArrayVar(&f.sets, "set", nil, "Set a key: key=value (e.g. ldap.port=636); repeat for several")
	fs.BoolVar(&f.nonInteractive, "non-interactive", false, "Never run the wizard; fail on missing keys")
	fs.BoolVar(&f.plain, "plain", false, "Line-based wizard and output instead of the full-screen interface")
	fs.StringVar(&f.wizard, "wizard", bootstrapcmd.WizardMissing, "Wizard questions to ask: missing|all")
	fs.BoolVar(&f.verbose, "verbose", false, "List every legacy key that is not imported; log the full zmcertmgr output")
	fs.StringVar(&f.output, "output", "", "Save the configuration to this file (mode 0600, no passwords)")

	if run {
		fs.BoolVar(&f.dryRun, "dry-run", false, "Print the steps that would apply; change nothing")
	}

	words := func(w ...string) cobra.CompletionFunc {
		return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return w, cobra.ShellCompDirectiveNoFileComp
		}
	}

	_ = cmd.RegisterFlagCompletionFunc("wizard", words(bootstrapcmd.WizardMissing, bootstrapcmd.WizardAll))
	_ = cmd.RegisterFlagCompletionFunc("set", words())
}

func runSetup(f *flags, env sys.System, stdin io.Reader, stdout, stderr io.Writer, status *int) error {
	if err := f.check(); err != nil {
		return err
	}

	*status = bootstrapcmd.RunSetup(context.Background(), env, stdin, stdout, stderr,
		&bootstrapcmd.RunOptions{Options: *f.options(), DryRun: f.dryRun})

	return nil
}

// check validates the flags that cobra takes as free text.
func (f *flags) check() error {
	if !slices.Contains([]string{bootstrapcmd.WizardMissing, bootstrapcmd.WizardAll}, f.wizard) {
		return fmt.Errorf("--wizard must be %q or %q, not %q", bootstrapcmd.WizardMissing, bootstrapcmd.WizardAll, f.wizard)
	}

	return nil
}

func (f *flags) options() *bootstrapcmd.Options {
	return &bootstrapcmd.Options{
		ConfigPath:     absPath(f.config),
		LegacyPath:     absPath(f.legacy),
		Sets:           f.sets,
		NonInteractive: f.nonInteractive,
		Plain:          f.plain,
		Verbose:        f.verbose,
		Wizard:         f.wizard,
		Output:         absPath(f.output),
		Environ:        os.Environ(),
	}
}

// absPath makes a relative path absolute ("" stays empty).
func absPath(p string) string {
	if p == "" {
		return p
	}

	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}

	return p
}
