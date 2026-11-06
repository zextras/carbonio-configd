// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/ldapcmd"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// LDAPCmd handles "configd ldap": the zmprov verbs configd and its callers
// need, spoken straight to LDAP. The connection flags apply to every verb;
// without them the credentials come from the effective localconfig
// (localconfig.xml and CONFIGD_LC_* environment).
type LDAPCmd struct {
	URL          []string
	BindDN       string
	PasswordFile string
	JSON         bool

	Gacf   LDAPGacfCmd
	Gcf    LDAPGcfCmd
	Gs     LDAPGsCmd
	Mcf    LDAPMcfCmd
	Search LDAPSearchCmd
	Wait   LDAPWaitCmd

	// dial is the seam of the tests: the connection opener. Unset, the verbs
	// use a pooled client.
	dial ldapcmd.Dial
}

// ldapScopes are the values of "ldap search --scope".
var ldapScopes = []string{"base", "one", "sub"}

// ldapCmd builds "configd ldap". The connection flags are global to its verbs:
// they are accepted before and after the verb, except for mcf, whose arguments
// are taken verbatim.
func (a *app) ldapCmd() *cobra.Command {
	l := &a.cli.LDAP

	verb := func(use, short string, args cobra.PositionalArgs, bind func(*cobra.Command, []string) error,
		run func(*LDAPCmd, sys.System, *stdio) error,
	) *cobra.Command {
		return a.leaf(use, short, args, bind, func() error { return run(l, a.env, a.std) })
	}

	gcf := verb("gcf <attrs> ...", "Print attributes of the global config",
		posArgs([]string{"<attrs>"}, nil, true),
		func(_ *cobra.Command, args []string) error {
			l.Gcf.Attrs = args

			return nil
		}, l.Gcf.Run)
	describeArgs(gcf, [2]string{"<attrs> ...", "Attributes to print"})

	gs := verb("gs [<server>]", "Print a server entry with global inheritance",
		posArgs(nil, []string{"[<server>]"}, false),
		func(_ *cobra.Command, args []string) error {
			l.Gs.Server = ""
			if len(args) > 0 {
				l.Gs.Server = args[0]
			}

			return nil
		}, l.Gs.Run)
	describeArgs(gs, [2]string{"[<server>]", "Server hostname (default: zimbra_server_hostname)"})

	// mcf takes its arguments verbatim, so a "-attr" removal is not mistaken
	// for a flag; the connection flags go before "mcf".
	mcf := verb("mcf <changes> ...", "Modify the global config: [+|-]attr value ...",
		posArgs([]string{"<changes>"}, nil, true),
		func(_ *cobra.Command, args []string) error {
			l.Mcf.Changes = args

			return nil
		}, l.Mcf.Run)
	mcf.DisableFlagParsing = true
	describeArgs(mcf, [2]string{"<changes> ...", "[+|-]attr value pairs: + adds, - removes, none replaces"})

	cmd := group("ldap <command>", "Query and modify LDAP",
		verb("gacf", "Print the global config", posArgs(nil, nil, false), nil, l.Gacf.Run),
		gcf,
		gs,
		mcf,
		a.ldapSearchCmd(l),
		a.ldapWaitCmd(l),
	)

	pf := cmd.PersistentFlags()
	pf.SortFlags = false
	pf.StringArrayVar(&l.URL, "url", nil, "LDAP URL; repeat for failover (default: ldap_master_url, ldap_url)")
	pf.StringVar(&l.BindDN, "bind-dn", "", "Bind DN (default: zimbra_ldap_userdn)")
	pf.StringVar(&l.PasswordFile, "password-file", "", "File holding the bind password (default: zimbra_ldap_password)")
	pf.BoolVar(&l.JSON, "json", false, "Print JSON instead of attr: value lines")
	noFileCompletion(cmd, "url", "bind-dn")

	return cmd
}

func (a *app) ldapSearchCmd(l *LDAPCmd) *cobra.Command {
	c := &l.Search

	cmd := a.leaf("search", "Search LDAP", posArgs(nil, nil, false), nil,
		func() error { return c.Run(l, a.env, a.std) })

	fs := cmd.Flags()
	fs.SortFlags = false
	fs.StringVar(&c.Base, "base", "", "Search base (default: cn=zimbra)")
	fs.StringVar(&c.Filter, "filter", "", "LDAP filter (default: (objectClass=*))")
	enumVar(fs, &c.Scope, "scope", "sub", "Search scope", ldapScopes...)
	fs.StringArrayVar(&c.Attr, "attr", nil, "Attribute to return; repeat for several (default: all)")
	mustCompleteFlag(cmd, "scope", staticCompletion(ldapScopes...))
	noFileCompletion(cmd, "base", "filter", "attr")

	return cmd
}

func (a *app) ldapWaitCmd(l *LDAPCmd) *cobra.Command {
	c := &l.Wait

	cmd := a.leaf("wait", "Wait until LDAP (ldap_url) accepts connections", posArgs(nil, nil, false), nil,
		func() error { return c.Run(l, a.env, a.std) })
	cmd.Flags().DurationVar(&c.Timeout, "timeout", ldapWaitTimeout, "Give up after this long")
	noFileCompletion(cmd, "timeout")

	return cmd
}

// ldapWaitTimeout is the default of "ldap wait --timeout".
const ldapWaitTimeout = 120 * time.Second

// LDAPWaitCmd is "configd ldap wait", the bounded LDAP wait of the systemd
// unit. It succeeds at once when no LDAP URL is configured, as the daemon
// starts without one.
type LDAPWaitCmd struct {
	Timeout time.Duration
}

// Run executes "configd ldap wait".
func (c *LDAPWaitCmd) Run(parent *LDAPCmd, env sys.System, std *stdio) error {
	return exitWith(ldapcmd.Wait(context.Background(), std.Err, &ldapcmd.WaitOptions{
		URLs:    parent.URL,
		FS:      env.FS,
		Timeout: c.Timeout,
	}))
}

// LDAPGacfCmd is "configd ldap gacf".
type LDAPGacfCmd struct{}

// Run executes "configd ldap gacf".
func (c *LDAPGacfCmd) Run(parent *LDAPCmd, env sys.System, std *stdio) error {
	return parent.run(env, std, parent.options(ldapcmd.VerbGacf, nil))
}

// LDAPGcfCmd is "configd ldap gcf".
type LDAPGcfCmd struct {
	Attrs []string
}

// Run executes "configd ldap gcf".
func (c *LDAPGcfCmd) Run(parent *LDAPCmd, env sys.System, std *stdio) error {
	return parent.run(env, std, parent.options(ldapcmd.VerbGcf, c.Attrs))
}

// LDAPGsCmd is "configd ldap gs".
type LDAPGsCmd struct {
	Server string
}

// Run executes "configd ldap gs".
func (c *LDAPGsCmd) Run(parent *LDAPCmd, env sys.System, std *stdio) error {
	var args []string
	if c.Server != "" {
		args = []string{c.Server}
	}

	return parent.run(env, std, parent.options(ldapcmd.VerbGs, args))
}

// LDAPMcfCmd is "configd ldap mcf". Its arguments are taken verbatim, so a
// "-attr" removal is not mistaken for a flag; connection flags go before "mcf".
type LDAPMcfCmd struct {
	Changes []string
}

// Run executes "configd ldap mcf".
func (c *LDAPMcfCmd) Run(parent *LDAPCmd, env sys.System, std *stdio) error {
	// The command is passthrough, so a habitual "--" arrives as an argument.
	changes := c.Changes
	if len(changes) > 0 && changes[0] == "--" {
		changes = changes[1:]
	}

	return parent.run(env, std, parent.options(ldapcmd.VerbMcf, changes))
}

// LDAPSearchCmd is "configd ldap search".
type LDAPSearchCmd struct {
	Base   string
	Filter string
	Scope  string
	Attr   []string
}

// Run executes "configd ldap search".
func (c *LDAPSearchCmd) Run(parent *LDAPCmd, env sys.System, std *stdio) error {
	opts := parent.options(ldapcmd.VerbSearch, nil)
	opts.Base, opts.Filter, opts.Scope, opts.Attrs = c.Base, c.Filter, c.Scope, c.Attr

	return parent.run(env, std, opts)
}

func (c *LDAPCmd) options(verb ldapcmd.Verb, args []string) *ldapcmd.Options {
	return &ldapcmd.Options{
		Verb:         verb,
		Args:         args,
		URLs:         c.URL,
		BindDN:       c.BindDN,
		PasswordFile: c.PasswordFile,
		JSON:         c.JSON,
		Dial:         c.dial,
	}
}

// run executes one verb on env's filesystem and turns its status into main's
// exit error.
func (c *LDAPCmd) run(env sys.System, std *stdio, opts *ldapcmd.Options) error {
	opts.FS = env.FS

	return exitWith(ldapcmd.Run(context.Background(), std.Out, std.Err, opts))
}
