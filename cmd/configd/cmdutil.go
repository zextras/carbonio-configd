// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// app is what the commands share once the command tree is built: the parsed
// CLI, the host and the standard streams. started flips when a command begins
// to run, which is what separates a usage error ("configd: error: ...") from a
// failure of the command itself ("Error: ...").
type app struct {
	cli     *CLI
	env     sys.System
	std     *stdio
	started bool
}

// leaf builds a runnable command. bind (optional) copies the positional
// arguments into the command struct before run: it is a PreRunE, so a test that
// swaps RunE still sees the parsed arguments.
func (a *app) leaf(use, short string, args cobra.PositionalArgs, bind func(cmd *cobra.Command, args []string) error,
	run func() error,
) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Args:    args,
		PreRunE: bind,
		RunE: func(*cobra.Command, []string) error {
			a.started = true

			return run()
		},
	}
}

// group builds a command that only holds subcommands. Without one it fails the
// way a missing verb always did, with the list of the verbs; an argument that is
// not a verb is unexpected. Cobra only validates the arguments of a runnable
// command, hence the RunE that is never reached.
func group(use, short string, subs ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := posArgs(nil, nil, false)(cmd, args); err != nil {
				return err
			}

			return fmt.Errorf("expected one of %s", subcommandList(cmd))
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}

	cmd.AddCommand(subs...)

	return cmd
}

// maxListedSubcommands is how many verbs the "expected one of" message names
// before it elides the rest.
const maxListedSubcommands = 5

// subcommandList quotes the visible subcommands of cmd, eliding past
// maxListedSubcommands.
func subcommandList(cmd *cobra.Command) string {
	var names []string

	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() {
			names = append(names, fmt.Sprintf("%q", sub.Name()))
		}
	}

	if len(names) > maxListedSubcommands {
		names = append(names[:maxListedSubcommands], "...")
	}

	return strings.Join(names, ", ")
}

// posArgs validates positional arguments: the required ones must be there,
// then up to len(optional) more are taken, or any number when variadic (the
// last named positional then takes the rest). The messages are the ones every
// command has always printed: `expected "<name>"` and `unexpected argument x`.
func posArgs(required, optional []string, variadic bool) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < len(required) {
			missing := slices.Clone(required[len(args):])
			if variadic && len(optional) == 0 {
				missing[len(missing)-1] += " ..."
			}

			return fmt.Errorf("expected %q", strings.Join(missing, " "))
		}

		if limit := len(required) + len(optional); !variadic && len(args) > limit {
			return fmt.Errorf("unexpected argument %s", args[limit])
		}

		return nil
	}
}

// describeArgs appends the "Arguments:" block of the help to cmd. Each pair is
// a positional's name and its description.
func describeArgs(cmd *cobra.Command, pairs ...[2]string) *cobra.Command {
	var sb strings.Builder

	sb.WriteString(cmd.Short)
	sb.WriteString("\n\nArguments:\n")

	width := 0
	for _, p := range pairs {
		width = max(width, len(p[0]))
	}

	for _, p := range pairs {
		fmt.Fprintf(&sb, "  %-*s  %s\n", width, p[0], p[1])
	}

	cmd.Long = strings.TrimRight(sb.String(), "\n")

	return cmd
}

// flagAliases makes the long names in aliases (alias -> canonical name) accepted
// on fs. They are not flags of their own: help, completion and the parsed state
// only know the canonical name.
func flagAliases(fs *pflag.FlagSet, aliases map[string]string) {
	fs.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if canonical, ok := aliases[name]; ok {
			return pflag.NormalizedName(canonical)
		}

		return pflag.NormalizedName(name)
	})
}

// pathValue is a string flag whose value is made absolute, with a leading
// "~/" expanded to the home directory, as the former "type:path" tag did. An
// empty value stays empty.
type pathValue struct{ dst *string }

func (p pathValue) String() string {
	if p.dst == nil {
		return ""
	}

	return *p.dst
}

func (p pathValue) Set(s string) error {
	*p.dst = expandPath(s)

	return nil
}

// flagTypePath is the Type of a pathValue, shown as the placeholder of the flag.
const flagTypePath = "path"

func (pathValue) Type() string { return flagTypePath }

// expandPath makes path absolute and expands a leading "~/".
func expandPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if u, err := user.Current(); err == nil {
			return filepath.Join(u.HomeDir, rest)
		}

		return path
	}

	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}

	return path
}

// enumValue is a string flag restricted to a set of values.
type enumValue struct {
	dst     *string
	allowed []string
}

func (e *enumValue) String() string {
	if e.dst == nil {
		return ""
	}

	return *e.dst
}

func (e *enumValue) Set(s string) error {
	if !slices.Contains(e.allowed, s) {
		quoted := make([]string, len(e.allowed))
		for i, a := range e.allowed {
			quoted[i] = fmt.Sprintf("%q", a)
		}

		return fmt.Errorf("must be one of %s but got %q", strings.Join(quoted, ","), s)
	}

	*e.dst = s

	return nil
}

func (e *enumValue) Type() string { return strings.Join(e.allowed, "|") }

// enumVar registers an enum flag whose default is def.
func enumVar(fs *pflag.FlagSet, dst *string, name, def, usage string, allowed ...string) {
	*dst = def

	fs.Var(&enumValue{dst: dst, allowed: allowed}, name, usage)
}

// flagUsages formats the flags of fs for the help: one line each, switches
// without a value placeholder, a default shown when it is not the zero value.
// (pflag's own FlagUsages annotates every switch with "[=true|false]", which
// the command line does not accept as a separate word.)
func flagUsages(fs *pflag.FlagSet) string {
	type row struct{ left, usage string }

	var rows []row

	width := 0

	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}

		left := "      --" + f.Name
		if f.Shorthand != "" {
			left = "  -" + f.Shorthand + ", --" + f.Name
		}

		varname, usage := pflag.UnquoteUsage(f)
		if f.Value.Type() != "bool" && varname != "" {
			left += " " + varname
		}

		if !slices.Contains([]string{"", "false", "0", "0s", "[]"}, f.DefValue) {
			if f.Value.Type() == "string" || f.Value.Type() == flagTypePath {
				usage += fmt.Sprintf(" (default %q)", f.DefValue)
			} else {
				usage += fmt.Sprintf(" (default %s)", f.DefValue)
			}
		}

		width = max(width, len(left))
		rows = append(rows, row{left, usage})
	})

	var sb strings.Builder

	for _, r := range rows {
		fmt.Fprintf(&sb, "%-*s  %s\n", width, r.left, r.usage)
	}

	return sb.String()
}
