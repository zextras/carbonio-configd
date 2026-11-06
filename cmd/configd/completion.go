// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/parser"
	"github.com/zextras/carbonio-configd/internal/services"
	configtls "github.com/zextras/carbonio-configd/internal/tls"
)

// The values the commands accept for a fixed set of choices. The usage of the
// commands that take them states the same lists.
var (
	proxyProtocolNames = []string{"http", "https", "mail", "imap", "imaps", "pop3", "pop3s"}
	initComponentNames = []string{"mta", "proxy"}
)

// mailModeNames lists the values of "configd tls [mode]".
func mailModeNames() []string {
	names := make([]string, len(configtls.AllModes))
	for i, m := range configtls.AllModes {
		names[i] = string(m)
	}

	return names
}

// completer is the signature of cobra's argument and flag-value completions.
type completer = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// withPrefix keeps the candidates that start with toComplete.
func withPrefix(candidates []string, toComplete string) []string {
	return slices.DeleteFunc(slices.Clone(candidates), func(c string) bool {
		return !strings.HasPrefix(c, toComplete)
	})
}

// staticCompletion completes from a fixed list.
func staticCompletion(values ...string) completer {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return withPrefix(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// staticFirstArg completes the first positional argument from a fixed list and
// offers nothing after it.
func staticFirstArg(values ...string) completer {
	complete := staticCompletion(values...)

	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return complete(cmd, args, toComplete)
	}
}

// mustCompleteFlag registers the completion of a flag value; the flags are
// defined in the same function, so a failure is a programming error.
func mustCompleteFlag(cmd *cobra.Command, flag string, fn completer) {
	if err := cmd.RegisterFlagCompletionFunc(flag, fn); err != nil {
		panic(fmt.Sprintf("completion of --%s: %v", flag, err))
	}
}

// noFileCompletion turns the file fallback off for flags whose values are not
// paths.
func noFileCompletion(cmd *cobra.Command, flags ...string) {
	for _, flag := range flags {
		mustCompleteFlag(cmd, flag, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
	}
}

// completeFirstService completes the service name that opens the arguments of
// the service verbs and of "status".
func (a *app) completeFirstService(
	_ *cobra.Command, args []string, toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := services.NewController(a.env).AllServiceNames()

	return withPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeSections completes the config section names of zmconfigd.cf, one at
// a time. "rewrite" also takes "all".
func (a *app) completeSections(withAll bool) completer {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		names := configSectionNames()
		if withAll {
			names = append(names, "all")
		}

		names = slices.DeleteFunc(names, func(n string) bool { return slices.Contains(args, n) })

		return withPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// configSectionNames reads the section names from the installed zmconfigd.cf.
// A file that cannot be read or parsed completes nothing.
func configSectionNames() []string {
	ctx := context.Background()

	mta, err := parser.NewParser(parser.NewLexer(ctx, strings.NewReader(""))).
		Parse(ctx, cliutil.BasePath("conf", "zmconfigd.cf"))
	if err != nil || mta == nil {
		return nil
	}

	return slices.Sorted(maps.Keys(mta.Sections))
}

// completeLocalconfigKeys completes the localconfig key names.
func completeLocalconfigKeys(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if strings.Contains(toComplete, "=") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return withPrefix(localconfig.KnownKeys(), toComplete), cobra.ShellCompDirectiveNoFileComp
}
