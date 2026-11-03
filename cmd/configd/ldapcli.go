// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"io"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/ldapcmd"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// LDAPCmd handles "configd ldap": the zmprov verbs configd and its callers
// need, spoken straight to LDAP. The connection flags apply to every verb;
// without them the credentials come from the effective localconfig
// (localconfig.xml and CONFIGD_LC_* environment).
type LDAPCmd struct {
	URL          []string `name:"url" help:"LDAP URL; repeat for failover (default: ldap_master_url, ldap_url)" sep:"none"`
	BindDN       string   `name:"bind-dn" help:"Bind DN (default: zimbra_ldap_userdn)"`
	PasswordFile string   `name:"password-file" help:"File holding the bind password (default: zimbra_ldap_password)"`
	JSON         bool     `name:"json" help:"Print JSON instead of zmprov-style attr: value lines"`

	Gacf   LDAPGacfCmd   `cmd:"" help:"Print the global config (zmprov gacf)"`
	Gcf    LDAPGcfCmd    `cmd:"" help:"Print attributes of the global config (zmprov gcf)"`
	Gs     LDAPGsCmd     `cmd:"" help:"Print a server entry with global inheritance (zmprov gs)"`
	Mcf    LDAPMcfCmd    `cmd:"" passthrough:"" help:"Modify the global config: [+|-]attr value ... (zmprov mcf)"`
	Search LDAPSearchCmd `cmd:"" help:"Search LDAP"`

	// dial and stdout are the seams of the tests: the connection opener and
	// where the verbs print. Unset, the verbs use a pooled client and os.Stdout.
	dial   ldapcmd.Dial
	stdout io.Writer
}

// LDAPGacfCmd is "configd ldap gacf".
type LDAPGacfCmd struct{}

// Run executes "configd ldap gacf".
func (c *LDAPGacfCmd) Run(parent *LDAPCmd, env sys.System) error {
	return parent.run(env, parent.options(ldapcmd.VerbGacf, nil))
}

// LDAPGcfCmd is "configd ldap gcf".
type LDAPGcfCmd struct {
	Attrs []string `arg:"" help:"Attributes to print"`
}

// Run executes "configd ldap gcf".
func (c *LDAPGcfCmd) Run(parent *LDAPCmd, env sys.System) error {
	return parent.run(env, parent.options(ldapcmd.VerbGcf, c.Attrs))
}

// LDAPGsCmd is "configd ldap gs".
type LDAPGsCmd struct {
	Server string `arg:"" optional:"" help:"Server hostname (default: zimbra_server_hostname)"`
}

// Run executes "configd ldap gs".
func (c *LDAPGsCmd) Run(parent *LDAPCmd, env sys.System) error {
	var args []string
	if c.Server != "" {
		args = []string{c.Server}
	}

	return parent.run(env, parent.options(ldapcmd.VerbGs, args))
}

// LDAPMcfCmd is "configd ldap mcf". Its arguments are taken verbatim, so a
// "-attr" removal is not mistaken for a flag; connection flags go before "mcf".
type LDAPMcfCmd struct {
	Changes []string `arg:"" help:"[+|-]attr value pairs: + adds, - removes, none replaces"`
}

// Run executes "configd ldap mcf".
func (c *LDAPMcfCmd) Run(parent *LDAPCmd, env sys.System) error {
	// The command is passthrough, so a habitual "--" arrives as an argument.
	changes := c.Changes
	if len(changes) > 0 && changes[0] == "--" {
		changes = changes[1:]
	}

	return parent.run(env, parent.options(ldapcmd.VerbMcf, changes))
}

// LDAPSearchCmd is "configd ldap search".
type LDAPSearchCmd struct {
	Base   string   `name:"base" help:"Search base (default: cn=zimbra)"`
	Filter string   `name:"filter" help:"LDAP filter (default: (objectClass=*))"`
	Scope  string   `name:"scope" enum:"base,one,sub" default:"sub" help:"Search scope"`
	Attr   []string `name:"attr" help:"Attribute to return; repeat for several (default: all)" sep:"none"`
}

// Run executes "configd ldap search".
func (c *LDAPSearchCmd) Run(parent *LDAPCmd, env sys.System) error {
	opts := parent.options(ldapcmd.VerbSearch, nil)
	opts.Base, opts.Filter, opts.Scope, opts.Attrs = c.Base, c.Filter, c.Scope, c.Attr

	return parent.run(env, opts)
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
func (c *LDAPCmd) run(env sys.System, opts *ldapcmd.Options) error {
	opts.FS = env.FS

	stdout := c.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	return exitWith(ldapcmd.Run(context.Background(), stdout, os.Stderr, opts))
}
