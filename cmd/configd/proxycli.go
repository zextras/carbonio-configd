// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"

	"github.com/zextras/carbonio-configd/internal/cli/proxy"
)

// ProxyCmd handles the "configd proxy" subcommand.
type ProxyCmd struct {
	Conf    ProxyConfCmd    `cmd:"" help:"Print assembled nginx configuration (zmproxyconf replacement)"`
	Gen     ProxyGenCmd     `cmd:"" help:"Generate nginx proxy configuration files (zmproxyconfgen replacement)"`
	Enable  ProxyEnableCmd  `cmd:"" help:"Enable a proxy protocol"`
	Disable ProxyDisableCmd `cmd:"" help:"Disable a proxy protocol"`
	Status  ProxyStatusCmd  `cmd:"" help:"Show all protocol statuses"`
	Rewrite ProxyRewriteCmd `cmd:"" default:"withargs" hidden:""`
}

// runProxy executes a proxy action with the process's stdio.
func runProxy(opts *proxy.Options) error {
	opts.Standalone = standaloneRewrite(false)

	return exitWith(proxy.Run(context.Background(), os.Stdout, os.Stderr, opts))
}

// ProxyConfCmd prints the assembled nginx configuration by following all include
// directives. Go replacement for the legacy zmproxyconf Perl script.
type ProxyConfCmd struct {
	Markers    bool   `short:"m" help:"Print file inclusion markers"`
	Indent     bool   `short:"i" help:"Indent included files"`
	NoComments bool   `short:"n" help:"Do not print comment lines (beginning with #)"`
	NoEmpty    bool   `short:"e" help:"Do not print empty lines"`
	ConfigFile string `arg:"" optional:"" help:"Path to nginx.conf"`
}

// Run prints the assembled nginx configuration.
func (c *ProxyConfCmd) Run() error {
	return runProxy(&proxy.Options{
		Action:     proxy.ActionConf,
		ConfigFile: c.ConfigFile,
		Markers:    c.Markers,
		Indent:     c.Indent,
		NoComments: c.NoComments,
		NoEmpty:    c.NoEmpty,
	})
}

// ProxyGenCmd triggers proxy config file generation via the running configd daemon.
// Go replacement for the legacy zmproxyconfgen Java wrapper.
type ProxyGenCmd struct {
	ExtraConfigs []string `arg:"" optional:"" help:"Additional config names to regenerate"`
}

// Run generates the proxy configs.
func (c *ProxyGenCmd) Run() error {
	return runProxy(&proxy.Options{Action: proxy.ActionGen, ExtraConfigs: c.ExtraConfigs})
}

// ProxyEnableCmd enables a proxy protocol.
type ProxyEnableCmd struct {
	Protocol string `arg:"" help:"Protocol (http, https, mail, imap, imaps, pop3, pop3s)"`
}

// Run enables the protocol.
func (c *ProxyEnableCmd) Run() error {
	return runProxy(&proxy.Options{Action: proxy.ActionEnable, Protocol: c.Protocol})
}

// ProxyDisableCmd disables a proxy protocol.
type ProxyDisableCmd struct {
	Protocol string `arg:"" help:"Protocol (http, https, mail, imap, imaps, pop3, pop3s)"`
}

// Run disables the protocol.
func (c *ProxyDisableCmd) Run() error {
	return runProxy(&proxy.Options{Action: proxy.ActionDisable, Protocol: c.Protocol})
}

// ProxyStatusCmd shows proxy protocol statuses.
type ProxyStatusCmd struct{}

// Run shows the protocol statuses.
func (c *ProxyStatusCmd) Run() error {
	return runProxy(&proxy.Options{Action: proxy.ActionStatus})
}

// ProxyRewriteCmd handles the fallthrough case: "configd proxy" without
// enable/disable/status triggers a forced config rewrite for proxy.
type ProxyRewriteCmd struct {
	ExtraConfigs []string `arg:"" optional:"" help:"Additional config names"`
}

// Run rewrites the proxy configs.
func (c *ProxyRewriteCmd) Run() error {
	return runProxy(&proxy.Options{Action: proxy.ActionRewrite, ExtraConfigs: c.ExtraConfigs})
}
