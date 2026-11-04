// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/proxy"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// ProxyCmd handles the "configd proxy" subcommand.
type ProxyCmd struct {
	Conf    ProxyConfCmd    `cmd:"" help:"Print assembled nginx configuration (zmproxyconf replacement)"`
	Gen     ProxyGenCmd     `cmd:"" help:"Generate nginx proxy configuration files (zmproxyconfgen replacement)"`
	Enable  ProxyEnableCmd  `cmd:"" help:"Enable a proxy protocol"`
	Disable ProxyDisableCmd `cmd:"" help:"Disable a proxy protocol"`
	Status  ProxyStatusCmd  `cmd:"" help:"Show all protocol statuses"`
	// Template holds the offline tools for the nginx templates (no LDAP, no zextras user).
	Template ProxyTemplateCmd `cmd:"" help:"Format, lint and render the nginx proxy templates"`
	Rewrite  ProxyRewriteCmd  `cmd:"" default:"withargs" hidden:""`
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
	Markers    bool   `short:"m" help:"Print file inclusion markers"`
	Indent     bool   `short:"i" help:"Indent included files"`
	NoComments bool   `short:"n" aliases:"nocomments" help:"Do not print comment lines (beginning with #)"`
	NoEmpty    bool   `short:"e" aliases:"noempty" help:"Do not print empty lines"`
	ConfigFile string `arg:"" optional:"" help:"Path to nginx.conf"`
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
//
//nolint:lll // kong help texts stay on the tag line
type ProxyGenCmd struct {
	Verbose        bool     `name:"verbose" help:"Be verbose (-v, rewritten to --verbose: the root -v is --version)"`
	WorkDir        string   `short:"w" name:"workdir" help:"Proxy working directory (default /opt/zextras)"`
	TemplateDir    string   `short:"t" name:"templatedir" help:"Proxy template directory (default $workdir/conf/nginx/templates)"`
	DryRun         bool     `short:"n" name:"dry-run" help:"Do not write any configuration, just show which files would be written"`
	Defaults       bool     `short:"d" name:"defaults" help:"Print default variable map"`
	Definitions    bool     `short:"D" name:"definitions" help:"Print variable map definitions after loading the configuration"`
	Prefix         string   `short:"p" name:"prefix" help:"Config file prefix (default nginx.conf)"`
	TemplatePrefix string   `short:"P" name:"template-prefix" help:"Template file prefix (default $prefix)"`
	IncludeDir     string   `short:"i" name:"include-dir" help:"Directory (relative to $workdir/conf) where included files are written (default nginx/includes)"`
	Server         string   `short:"s" name:"server" help:"Generate from the attributes of this server object instead of the local server"`
	ForceDNS       bool     `short:"f" name:"force-dns-resolution" help:"Stop if the virtual hostname of a domain does not resolve"`
	Config         []string `short:"c" name:"config" sep:"none" help:"Override a config variable, name=value (repeatable; see -d/-D for the names)"`
	Args           []string `arg:"" optional:"" hidden:""`
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
	Fmt    ProxyTemplateFmtCmd    `cmd:"" help:"Re-indent and align nginx templates"`
	Lint   ProxyTemplateLintCmd   `cmd:"" help:"Report unknown variables, bad enablers and braces, stray whitespace"`
	Render ProxyTemplateRenderCmd `cmd:"" help:"Render nginx templates from a fixture, without LDAP"`
}

// ProxyTemplateFmtCmd is "configd proxy template fmt".
//
//nolint:lll // kong help texts stay on the tag line
type ProxyTemplateFmtCmd struct {
	Check bool     `help:"List the files that would change and print the diff; exit 1 if there are any" xor:"mode"`
	Write bool     `short:"w" help:"Rewrite the files in place" xor:"mode"`
	Paths []string `arg:"" name:"path" help:"Template files, or directories searched for *.template files"`
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
	Paths []string `arg:"" name:"path" help:"Template files, or directories searched for *.template files"`
}

// Run lints the templates.
func (c *ProxyTemplateLintCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{
		Action:   proxy.ActionTemplateLint,
		Template: &proxy.TemplateOptions{Paths: c.Paths},
	})
}

// ProxyTemplateRenderCmd is "configd proxy template render".
//
//nolint:lll // kong help texts stay on the tag line
type ProxyTemplateRenderCmd struct {
	Values    string `required:"" type:"path" help:"Fixture (YAML): the configuration to render with"`
	Templates string `required:"" type:"path" help:"Template directory"`
	Out       string `required:"" type:"path" help:"Directory the files are written to, standing for the conf directory (nginx.conf, nginx/includes/...)"`
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
	Protocol string `arg:"" help:"Protocol (http, https, mail, imap, imaps, pop3, pop3s)"`
}

// Run enables the protocol.
func (c *ProxyEnableCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{Action: proxy.ActionEnable, Protocol: c.Protocol})
}

// ProxyDisableCmd disables a proxy protocol.
type ProxyDisableCmd struct {
	Protocol string `arg:"" help:"Protocol (http, https, mail, imap, imaps, pop3, pop3s)"`
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
	ExtraConfigs []string `arg:"" optional:"" help:"Additional config names"`
}

// Run rewrites the proxy configs.
func (c *ProxyRewriteCmd) Run(env sys.System, std *stdio) error {
	return runProxy(env, std, &proxy.Options{Action: proxy.ActionRewrite, ExtraConfigs: c.ExtraConfigs})
}

// proxyGenValueFlags are the ProxyConfGen short options that take a value.
const proxyGenValueFlags = "wtpPisc"

// legacyProxyGenArgs rewrites the legacy "-v" of "configd proxy gen" into
// "--verbose". The root command owns -v (--version), and kong resolves a short
// flag against the root, so the subcommand cannot declare its own. Bundles
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
