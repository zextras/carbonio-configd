// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zextras/carbonio-configd/internal/cli/proxy"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// ProxyCmd handles the "configd proxy" subcommand.
type ProxyCmd struct {
	Conf    ProxyConfCmd
	Gen     ProxyGenCmd
	Enable  ProxyEnableCmd
	Disable ProxyDisableCmd
	Status  ProxyStatusCmd
	// Template holds the offline tools for the nginx templates (no LDAP, no zextras user).
	Template ProxyTemplateCmd
	Rewrite  ProxyRewriteCmd
}

// proxyCmd builds "configd proxy". Without a verb it is a forced rewrite of
// the proxy configs, with the arguments as additional config names.
func (a *app) proxyCmd() *cobra.Command {
	p := &a.cli.Proxy

	cmd := &cobra.Command{
		Use:   "proxy <command>",
		Short: "Proxy protocol management",
		Args:  cobra.ArbitraryArgs,
		PreRunE: func(_ *cobra.Command, args []string) error {
			p.Rewrite.ExtraConfigs = args

			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			a.started = true

			return p.Rewrite.Run(a.env, a.std)
		},
	}

	describeArgs(cmd, [2]string{"[<extra-configs> ...]", "Additional config names"})

	cmd.AddCommand(
		a.proxyConfCmd(),
		a.proxyGenCmd(),
		a.proxyProtocolCmd("enable", "Enable a proxy protocol", &p.Enable.Protocol,
			func() error { return p.Enable.Run(a.env, a.std) }),
		a.proxyProtocolCmd("disable", "Disable a proxy protocol", &p.Disable.Protocol,
			func() error { return p.Disable.Run(a.env, a.std) }),
		a.leaf("status", "Show all protocol statuses", posArgs(nil, nil, false), nil,
			func() error { return p.Status.Run(a.env, a.std) }),
		a.proxyTemplateCmd(),
	)

	return cmd
}

// proxyProtocolCmd builds "proxy enable" and "proxy disable".
func (a *app) proxyProtocolCmd(verb, short string, protocol *string, run func() error) *cobra.Command {
	cmd := a.leaf(verb+" <protocol>", short, posArgs([]string{"<protocol>"}, nil, false),
		func(_ *cobra.Command, args []string) error {
			*protocol = args[0]

			return nil
		}, run)
	cmd.ValidArgsFunction = staticFirstArg(proxyProtocolNames...)

	return describeArgs(cmd, [2]string{"<protocol>", "Protocol (http, https, mail, imap, imaps, pop3, pop3s)"})
}

// runProxy executes a proxy action with the process's stdio.
func runProxy(env sys.System, std *stdio, opts *proxy.Options) error {
	opts.Standalone = standaloneRewrite(env, false)

	return exitWith(proxy.Run(context.Background(), env, std.Out, std.Err, opts))
}

// ProxyConfCmd prints the assembled nginx configuration by following all include
// directives. Go replacement for the legacy zmproxyconf Perl script, which
// spells the filters --nocomments and --noempty.
type ProxyConfCmd struct {
	Markers    bool
	Indent     bool
	NoComments bool
	NoEmpty    bool
	ConfigFile string
}

func (a *app) proxyConfCmd() *cobra.Command {
	c := &a.cli.Proxy.Conf

	cmd := a.leaf("conf [<config-file>]", "Print assembled nginx configuration",
		posArgs(nil, []string{"[<config-file>]"}, false),
		func(_ *cobra.Command, args []string) error {
			c.ConfigFile = ""
			if len(args) > 0 {
				c.ConfigFile = args[0]
			}

			return nil
		},
		func() error { return c.Run(a.env, a.std) })

	fs := cmd.Flags()
	fs.SortFlags = false
	flagAliases(fs, map[string]string{"nocomments": "no-comments", "noempty": "no-empty"})
	fs.BoolVarP(&c.Markers, "markers", "m", false, "Print file inclusion markers")
	fs.BoolVarP(&c.Indent, "indent", "i", false, "Indent included files")
	fs.BoolVarP(&c.NoComments, "no-comments", "n", false, "Do not print comment lines (beginning with #)")
	fs.BoolVarP(&c.NoEmpty, "no-empty", "e", false, "Do not print empty lines")

	return describeArgs(cmd, [2]string{"[<config-file>]", "Path to nginx.conf"})
}

// Run prints the assembled nginx configuration.
func (c *ProxyConfCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{
		Action:     proxy.ActionConf,
		ConfigFile: c.ConfigFile,
		Markers:    c.Markers,
		Indent:     c.Indent,
		NoComments: c.NoComments,
		NoEmpty:    c.NoEmpty,
	})
}

// ProxyGenCmd generates the nginx proxy configuration synchronously in this
// process. Replaces the legacy zmproxyconfgen (Java ProxyConfGen) and takes
// its flags.
type ProxyGenCmd struct {
	Verbose        bool
	WorkDir        string
	TemplateDir    string
	DryRun         bool
	Defaults       bool
	Definitions    bool
	Prefix         string
	TemplatePrefix string
	IncludeDir     string
	Server         string
	ForceDNS       bool
	Config         []string
	Args           []string
}

//nolint:lll // help texts stay on the flag line
func (a *app) proxyGenCmd() *cobra.Command {
	c := &a.cli.Proxy.Gen

	cmd := a.leaf("gen [<args> ...]", "Generate nginx proxy configuration files",
		posArgs(nil, nil, true),
		func(_ *cobra.Command, args []string) error {
			c.Args = args

			return nil
		},
		func() error { return c.Run(a.env, a.std) })

	fs := cmd.Flags()
	fs.SortFlags = false
	fs.BoolVar(&c.Verbose, "verbose", false, "Be verbose (-v, rewritten to --verbose: the root -v is --version)")
	fs.StringVarP(&c.WorkDir, "workdir", "w", "", "Proxy working directory (default /opt/zextras)")
	fs.StringVarP(&c.TemplateDir, "templatedir", "t", "", "Proxy template directory (default $workdir/conf/nginx/templates)")
	fs.BoolVarP(&c.DryRun, "dry-run", "n", false, "Do not write any configuration, just show which files would be written")
	fs.BoolVarP(&c.Defaults, "defaults", "d", false, "Print default variable map")
	fs.BoolVarP(&c.Definitions, "definitions", "D", false, "Print variable map definitions after loading the configuration")
	fs.StringVarP(&c.Prefix, "prefix", "p", "", "Config file prefix (default nginx.conf)")
	fs.StringVarP(&c.TemplatePrefix, "template-prefix", "P", "", "Template file prefix (default $prefix)")
	fs.StringVarP(&c.IncludeDir, "include-dir", "i", "", "Directory (relative to $workdir/conf) where included files are written (default nginx/includes)")
	fs.StringVarP(&c.Server, "server", "s", "", "Generate from the attributes of this server object instead of the local server")
	fs.BoolVarP(&c.ForceDNS, "force-dns-resolution", "f", false, "Stop if the virtual hostname of a domain does not resolve")
	fs.StringArrayVarP(&c.Config, "config", "c", nil, "Override a config variable, `name=value` (repeatable; see -d/-D for the names)")

	noFileCompletion(cmd, "verbose", "server", "prefix", "template-prefix", "include-dir", "config")

	return describeArgs(cmd, [2]string{"[<args> ...]", ""})
}

// Run generates the proxy configs.
func (c *ProxyGenCmd) Run(env sys.System, std *stdio) error {
	overrides, rest := proxy.SplitOverrides(c.Config, c.Args)

	opts := &proxy.Options{
		Action: proxy.ActionGen,
		Gen: &proxy.GenOptions{
			Verbose:        c.Verbose,
			DryRun:         c.DryRun,
			Defaults:       c.Defaults,
			Definitions:    c.Definitions,
			ForceDNS:       c.ForceDNS,
			WorkDir:        c.WorkDir,
			TemplateDir:    c.TemplateDir,
			Prefix:         c.Prefix,
			TemplatePrefix: c.TemplatePrefix,
			IncludeDir:     c.IncludeDir,
			Server:         c.Server,
			Overrides:      overrides,
			Args:           rest,
		},
		Generate: generateProxy(env),
	}

	return runProxy(env, std, opts)
}

// ProxyTemplateCmd groups the template tools: they work on files only, so they
// run anywhere, as any user.
type ProxyTemplateCmd struct {
	Fmt    ProxyTemplateFmtCmd
	Lint   ProxyTemplateLintCmd
	Render ProxyTemplateRenderCmd
}

// proxyTemplateCmd builds "configd proxy template".
func (a *app) proxyTemplateCmd() *cobra.Command {
	t := &a.cli.Proxy.Template

	return group("template <command>", "Format, lint and render the nginx proxy templates",
		a.proxyTemplateFmtCmd(&t.Fmt),
		a.proxyTemplateLintCmd(&t.Lint),
		a.proxyTemplateRenderCmd(&t.Render),
	)
}

// ProxyTemplateFmtCmd is "configd proxy template fmt".
type ProxyTemplateFmtCmd struct {
	Check bool
	Write bool
	Paths []string
}

// templatePathsArg is the positional shared by "fmt" and "lint".
var templatePathsArg = [2]string{"<path> ...", "Template files, or directories searched for *.template files"}

func (a *app) proxyTemplateFmtCmd(c *ProxyTemplateFmtCmd) *cobra.Command {
	cmd := a.leaf("fmt <path> ...", "Re-indent and align nginx templates", posArgs([]string{"<path>"}, nil, true),
		func(_ *cobra.Command, args []string) error {
			if c.Check && c.Write {
				return errors.New("--check and --write can't be used together")
			}

			c.Paths = args

			return nil
		},
		func() error { return c.Run(a.env, a.std) })

	fs := cmd.Flags()
	fs.SortFlags = false
	fs.BoolVar(&c.Check, "check", false, "List the files that would change and print the diff; exit 1 if there are any")
	fs.BoolVarP(&c.Write, "write", "w", false, "Rewrite the files in place")
	cmd.MarkFlagsMutuallyExclusive("check", "write")

	return describeArgs(cmd, templatePathsArg)
}

// Run formats the templates.
func (c *ProxyTemplateFmtCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{
		Action:   proxy.ActionTemplateFmt,
		Template: &proxy.TemplateOptions{Paths: c.Paths, Check: c.Check, Write: c.Write},
	})
}

// ProxyTemplateLintCmd is "configd proxy template lint".
type ProxyTemplateLintCmd struct {
	Paths []string
}

func (a *app) proxyTemplateLintCmd(c *ProxyTemplateLintCmd) *cobra.Command {
	cmd := a.leaf("lint <path> ...", "Report unknown variables, bad enablers and braces, stray whitespace",
		posArgs([]string{"<path>"}, nil, true),
		func(_ *cobra.Command, args []string) error {
			c.Paths = args

			return nil
		},
		func() error { return c.Run(a.env, a.std) })

	return describeArgs(cmd, templatePathsArg)
}

// Run lints the templates.
func (c *ProxyTemplateLintCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{
		Action:   proxy.ActionTemplateLint,
		Template: &proxy.TemplateOptions{Paths: c.Paths},
	})
}

// ProxyTemplateRenderCmd is "configd proxy template render".
type ProxyTemplateRenderCmd struct {
	Values    string
	Templates string
	Out       string
}

//nolint:lll // help texts stay on the flag line
func (a *app) proxyTemplateRenderCmd(c *ProxyTemplateRenderCmd) *cobra.Command {
	cmd := a.leaf("render", "Render nginx templates from a fixture, without LDAP", posArgs(nil, nil, false), nil,
		func() error { return c.Run(a.env, a.std) })

	fs := cmd.Flags()
	fs.SortFlags = false
	fs.Var(pathValue{&c.Values}, "values", "Fixture (YAML): the configuration to render with")
	fs.Var(pathValue{&c.Templates}, "templates", "Template directory")
	fs.Var(pathValue{&c.Out}, "out", "Directory the files are written to, standing for the conf directory (nginx.conf, nginx/includes/...)")

	for _, name := range []string{"values", "templates", "out"} {
		_ = cmd.MarkFlagRequired(name)
	}

	return cmd
}

// Run renders the templates.
func (c *ProxyTemplateRenderCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{
		Action:   proxy.ActionTemplateRender,
		Template: &proxy.TemplateOptions{Values: c.Values, Templates: c.Templates, Out: c.Out},
	})
}

// ProxyEnableCmd enables a proxy protocol.
type ProxyEnableCmd struct {
	Protocol string
}

// Run enables the protocol.
func (c *ProxyEnableCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{Action: proxy.ActionEnable, Protocol: c.Protocol})
}

// ProxyDisableCmd disables a proxy protocol.
type ProxyDisableCmd struct {
	Protocol string
}

// Run disables the protocol.
func (c *ProxyDisableCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{Action: proxy.ActionDisable, Protocol: c.Protocol})
}

// ProxyStatusCmd shows proxy protocol statuses.
type ProxyStatusCmd struct{}

// Run shows the protocol statuses.
func (c *ProxyStatusCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{Action: proxy.ActionStatus})
}

// ProxyRewriteCmd handles the fallthrough case: "configd proxy" without
// enable/disable/status triggers a forced config rewrite for proxy.
type ProxyRewriteCmd struct {
	ExtraConfigs []string
}

// Run rewrites the proxy configs.
func (c *ProxyRewriteCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{Action: proxy.ActionRewrite, ExtraConfigs: c.ExtraConfigs})
}

// proxyGenValueFlags are the ProxyConfGen short options that take a value.
const proxyGenValueFlags = "wtpPisc"

// legacyProxyGenArgs rewrites the legacy "-v" of "configd proxy gen" into
// "--verbose". The root command owns -v (--version) as a global flag, so the
// subcommand cannot declare its own. Bundles
// ("-vn") are split; a bundle stops at the first option taking a value, as in
// commons-cli. Everything outside "proxy gen" is returned untouched.
func legacyProxyGenArgs(args []string) []string {
	if len(args) < 2 || args[0] != "proxy" || args[1] != "gen" {
		return args
	}

	out := append([]string(nil), args[:2]...)

	for _, a := range args[2:] {
		out = append(out, expandVerboseBundle(a)...)
	}

	return out
}

func expandVerboseBundle(a string) []string {
	if len(a) < 2 || a[0] != '-' || a[1] == '-' || !strings.ContainsRune(a, 'v') {
		return []string{a}
	}

	var (
		out  []string
		rest strings.Builder
	)

	for i, c := range a[1:] {
		if strings.ContainsRune(proxyGenValueFlags, c) {
			rest.WriteString(a[1+i:])

			break
		}

		if c == 'v' {
			out = append(out, "--verbose")

			continue
		}

		rest.WriteRune(c)
	}

	if rest.Len() > 0 {
		out = append(out, "-"+rest.String())
	}

	return out
}
