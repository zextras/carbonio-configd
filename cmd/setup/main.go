// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command carbonio-bootstrap configures a Carbonio node: it resolves the
// bootstrap configuration (YAML file, environment, flags, wizard) and runs
// the bootstrap steps.
//
//	carbonio-bootstrap [run]   configure this node
//	carbonio-bootstrap config  resolve, validate and print or save the configuration
//	carbonio-bootstrap gal-sync-account | ssh-keygen | update-authkeys
//	                           what the legacy setup.pl calls instead of zmgsautil,
//	                           zmsshkeygen and zmupdateauthkeys
//	carbonio-bootstrap rc <host> [command words...]
//	                           zmrc: a zmrcd command on another server, over SSH, natively
//	carbonio-bootstrap stat <name> [options]
//	carbonio-bootstrap stat run <collector>...
//	                           the zmstat-* statistics collectors (proc, cpu, vm, io, df,
//	                           fd, allprocs, mysql, nginx, mtaqueue, ldap), natively, one
//	                           or several in one process
//
// It is a binary of its own, apart from configd, so the full-screen terminal
// interface is not linked into the daemon.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

// toolCommands are the commands that replace scripts and parse their own
// arguments (proxyconfig has a -c and a -d of its own).
var toolCommands = []string{"ssh-keygen", "update-authkeys", "syslog", "dhparam", "proxyconfig", "ldap-passwd",
	"ldap-schema", "ldap-update-ldif", "ldap-enable-replica", "ldap-enable-mmr", "iptool", "iniutil", "certmgr",
	"stat", "rc"}

// legacyArgs maps the options of the former shell script: "-c FILE" is
// "--legacy-config FILE --non-interactive", and "-d" (debug) is accepted and
// ignored, the run always logs to /opt/zextras/log/bootstrap.*.log. The
// arguments of a tool command are left as they are.
func legacyArgs(args []string) []string {
	if len(args) > 0 && slices.Contains(toolCommands, args[0]) {
		return args
	}

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

	tio := &toolIO{env: env, stdin: stdin, stdout: stdout, stderr: stderr, status: status}
	root.AddCommand(config, runCmd, newGalSyncCmd(env, stdout, stderr, status),
		newToolCmd("ssh-keygen [rsa]", "Generate the zextras SSH key and store its public key (zmsshkeygen)",
			bootstrapcmd.RunSSHKeygen, tio),
		newToolCmd("update-authkeys [-a]",
			"Update ~zextras/.ssh/authorized_keys from the servers' keys; -a first on every server (zmupdateauthkeys)",
			bootstrapcmd.RunUpdateAuthKeys, tio),
		newStdinToolCmd("dhparam [install|get|set ...]",
			"install (or no argument, for setup.pl): the RFC 7919 ffdhe2048 dhparam recorded in zimbraSSLDHParam; "+
				"get and set are zmdhparam",
			bootstrapcmd.RunDHParam, tio),
		newStdinToolCmd("ldap-passwd [-h] [-r|-a|-b|-n|-p|[-c] -l] [password]",
			"Change an LDAP password in localconfig, the directory and carbonio.ldif (zmldappasswd); "+
				"without a password argument it is read from stdin",
			bootstrapcmd.RunLDAPPasswd, tio),
		newToolCmd("syslog", "Set up rsyslog for Carbonio logging, local or remote (zmsyslogsetup)",
			bootstrapcmd.RunSyslog, tio),
		newToolCmd("certmgr <command> [options]",
			"Create, verify, deploy and view the SSL certificates and the CA of the node (zmcertmgr)",
			bootstrapcmd.RunCertMgr, tio),
		newToolCmd("proxyconfig", "Configure the reverse proxy attributes, the options of libexec/zmproxyconfig",
			bootstrapcmd.RunProxyConfig, tio),
		newToolCmd("ldap-schema", "Install the slapd schemas (zmldapschema)",
			bootstrapcmd.RunLDAPSchema, tio),
		newToolCmd("ldap-update-ldif", "Bring the LDAP directory to the LDIF of the installed version (zmldapupdateldif)",
			bootstrapcmd.RunLDAPUpdateLDIF, tio),
		newToolCmd("ldap-enable-replica", "Enable replication: the provider on the master, a replica elsewhere "+
			"(zmldapenablereplica)", bootstrapcmd.RunLDAPEnableReplica, tio),
		newToolCmd("ldap-enable-mmr [-r RID] [-s SID] -m MMRURI",
			"Enable multi-master replication with another master (zmldapenable-mmr)",
			bootstrapcmd.RunLDAPEnableMMR, tio),
		newToolCmd("iptool [-d [-m ipmode]]",
			"Apply zimbraIPMode to the Java options and the MySQL bind address (zmiptool)",
			bootstrapcmd.RunIPTool, tio),
		newToolCmd("iniutil [options] inifile", "Read or edit one ini file (zminiutil)",
			bootstrapcmd.RunIniUtil, tio),
		newStdinToolCmd("rc <host> [command words...]",
			"Run a zmrcd command on another server over SSH: the words, or stdin, are what zmrcd reads (zmrc)",
			bootstrapcmd.RunRC, tio),
		newToolCmd("stat <name> [options] | stat run <collector>...",
			"Run a statistics collector: the zmstat-<name> scripts (proc, cpu, vm, io, df, fd, allprocs, "+
				"mysql, nginx, mtaqueue, ldap), as the zextras user; \"run\" runs several in one process",
			bootstrapcmd.RunStat, tio))

	return root
}

// newGalSyncCmd is "gal-sync-account": zmgsautil createAccount for the
// InternalGAL of a domain, as setup.pl calls it.
func newGalSyncCmd(env sys.System, stdout, stderr io.Writer, status *int) *cobra.Command {
	var domain, server string

	cmd := &cobra.Command{
		Use:   "gal-sync-account --domain DOMAIN --server SERVER",
		Short: "Create the GAL sync account of a domain (zmgsautil createAccount)",
		Long: "Create galsync.<random>@DOMAIN with the InternalGAL data source on SERVER and print " +
			"\"<name>\\t<id>\". Must run as root.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if strings.TrimSpace(domain) == "" || strings.TrimSpace(server) == "" {
				return errors.New("--domain and --server must not be empty")
			}

			*status = bootstrapcmd.RunGalSyncAccount(context.Background(), env, stdout, stderr, domain, server)

			return nil
		},
	}

	cmd.Flags().StringVar(&domain, "domain", "", "Domain whose GAL the account synchronises")
	cmd.Flags().StringVar(&server, "server", "", "Mailbox server that hosts the account")
	_ = cmd.MarkFlagRequired("domain")
	_ = cmd.MarkFlagRequired("server")

	return cmd
}

// toolIO is the process the tool commands run in: the system, the standard
// streams and where the exit status goes.
type toolIO struct {
	env            sys.System
	stdin          io.Reader
	stdout, stderr io.Writer
	status         *int
}

// newToolCmd is a command that replaces a legacy script: it runs fn as root
// or zextras with the arguments as the script took them (no cobra flags, so
// "-a" reaches fn).
func newToolCmd(use, short string,
	fn func(context.Context, sys.System, io.Writer, io.Writer, []string) int, tio *toolIO,
) *cobra.Command {
	return newStdinToolCmd(use, short,
		func(ctx context.Context, env sys.System, _ io.Reader, stdout, stderr io.Writer, args []string) int {
			return fn(ctx, env, stdout, stderr, args)
		}, tio)
}

// newStdinToolCmd is newToolCmd for a script that reads standard input (a
// password, which must not be on a command line).
func newStdinToolCmd(use, short string,
	fn func(context.Context, sys.System, io.Reader, io.Writer, io.Writer, []string) int, tio *toolIO,
) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		Long:               short + ". Runs as root or zextras.",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			*tio.status = fn(context.Background(), tio.env, tio.stdin, tio.stdout, tio.stderr, args)

			return nil
		},
	}
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
