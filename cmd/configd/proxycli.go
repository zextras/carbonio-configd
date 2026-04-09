// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
)

// proxyProtocols maps protocol names to their LDAP attributes.
var proxyProtocols = map[string]string{
	"http":  "zimbraReverseProxyHttpEnabled",
	"https": "zimbraReverseProxySSLToUpstreamEnabled",
	"mail":  "zimbraReverseProxyMailEnabled",
	"imap":  "zimbraReverseProxyMailImapEnabled",
	"imaps": "zimbraReverseProxyMailImapsEnabled",
	"pop3":  "zimbraReverseProxyMailPop3Enabled",
	"pop3s": "zimbraReverseProxyMailPop3sEnabled",
}

var (
	nginxWorkingDirRe = regexp.MustCompile(`^\s*working_directory\s+([^\s;]+)\s*;`)
	nginxIncludeRe    = regexp.MustCompile(`^\s*include\s+([^\s;]+)\s*;`)
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

// ProxyConfCmd prints the assembled nginx configuration by following all include
// directives. Go replacement for the legacy zmproxyconf Perl script.
type ProxyConfCmd struct {
	Markers    bool   `short:"m" help:"Print file inclusion markers"`
	Indent     bool   `short:"i" help:"Indent included files"`
	NoComments bool   `short:"n" help:"Do not print comment lines (beginning with #)"`
	NoEmpty    bool   `short:"e" help:"Do not print empty lines"`
	ConfigFile string `arg:"" optional:"" default:"/opt/zextras/conf/nginx.conf" help:"Path to nginx.conf"`
}

// Run executes the proxy conf command.
//
//nolint:unparam // Kong interface requires error return
func (c *ProxyConfCmd) Run() error {
	initCLILogging()

	if _, err := os.Stat("/opt/zextras/common/sbin/nginx"); err != nil {
		fmt.Println("Nginx not installed, exiting")
		return nil //nolint:nilerr // intentional: nginx absence is not an error condition
	}

	conf := c.ConfigFile
	if conf == "" {
		conf = "/opt/zextras/conf/nginx.conf"
	}

	opts := &nginxConfOpts{
		markers:    c.Markers,
		indent:     c.Indent,
		noComments: c.NoComments,
		noEmpty:    c.NoEmpty,
	}

	printNginxConf(os.Stdout, conf, 0, opts)

	return nil
}

// nginxConfOpts holds options for printNginxConf.
type nginxConfOpts struct {
	markers    bool
	indent     bool
	noComments bool
	noEmpty    bool
	workingDir string
}

// printNginxConf prints the nginx config at filename, expanding glob patterns and
// recursively following include directives — mirroring zmproxyconf behaviour.
func printNginxConf(w io.Writer, filename string, depth int, opts *nginxConfOpts) {
	matches, err := filepath.Glob(filename)
	if err != nil || len(matches) == 0 {
		matches = []string{filename}
	}

	for _, f := range matches {
		printNginxConfFile(w, f, depth, opts)
	}
}

func printNginxConfFile(w io.Writer, filename string, depth int, opts *nginxConfOpts) {
	prefix := ""
	if opts.indent {
		prefix = strings.Repeat("  ", depth)
	}

	if opts.markers {
		_, _ = fmt.Fprintf(w, "%s# begin:%s\n", prefix, filename)
	}

	f, err := os.Open(filename) //nolint:gosec // caller-supplied path
	if err != nil {
		_, _ = fmt.Fprintf(w, "%s# cannot open %s: %v\n", prefix, filename, err)

		if opts.markers {
			_, _ = fmt.Fprintf(w, "%s# end:%s\n", prefix, filename)
		}

		return
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()

		// Track working_directory — nginx uses it as base for relative include paths.
		if m := nginxWorkingDirRe.FindStringSubmatch(line); len(m) > 1 {
			opts.workingDir = m[1]
		}

		// Recursively follow include directives.
		if m := nginxIncludeRe.FindStringSubmatch(line); len(m) > 1 {
			printNginxInclude(w, m[1], prefix, depth, opts)
			continue
		}

		printNginxLine(w, line, prefix, opts)
	}

	if opts.markers {
		_, _ = fmt.Fprintf(w, "%s# end:%s\n", prefix, filename)
	}
}

// printNginxInclude handles a single nginx include directive, resolving the path
// relative to workingDir when needed and recursing into the included file.
func printNginxInclude(w io.Writer, includePath, prefix string, depth int, opts *nginxConfOpts) {
	if opts.workingDir == "" {
		_, _ = fmt.Fprintf(w, "%s# working directory not defined while including %s\n",
			prefix, includePath)

		return
	}

	if !filepath.IsAbs(includePath) {
		includePath = filepath.Join(opts.workingDir, includePath)
	}

	printNginxConf(w, includePath, depth+1, opts)
}

// printNginxLine writes a single config line to w, applying comment/empty filters.
func printNginxLine(w io.Writer, line, prefix string, opts *nginxConfOpts) {
	trimmed := strings.TrimSpace(line)

	if opts.noComments && strings.HasPrefix(trimmed, "#") {
		return
	}

	if opts.noEmpty && trimmed == "" {
		return
	}

	_, _ = fmt.Fprintf(w, "%s%s\n", prefix, line)
}

// ProxyGenCmd triggers proxy config file generation via the running configd daemon.
// Go replacement for the legacy zmproxyconfgen Java wrapper.
type ProxyGenCmd struct {
	ExtraConfigs []string `arg:"" optional:"" help:"Additional config names to regenerate"`
}

// Run executes the proxy gen command.
//
//nolint:unparam // Kong interface requires error return
func (c *ProxyGenCmd) Run() error {
	requireZextras()
	proxyContactDaemon(append([]string{"proxy"}, c.ExtraConfigs...))

	return nil
}

// ProxyEnableCmd enables a proxy protocol.
type ProxyEnableCmd struct {
	Protocol string `arg:"" help:"Protocol (http, https, mail, imap, imaps, pop3, pop3s)"`
}

// Run executes the proxy enable command.
//
//nolint:unparam // Kong interface requires error return
func (c *ProxyEnableCmd) Run() error {
	requireZextras()
	setProxyProtocol(c.Protocol, "TRUE")

	return nil
}

// ProxyDisableCmd disables a proxy protocol.
type ProxyDisableCmd struct {
	Protocol string `arg:"" help:"Protocol (http, https, mail, imap, imaps, pop3, pop3s)"`
}

// Run executes the proxy disable command.
//
//nolint:unparam // Kong interface requires error return
func (c *ProxyDisableCmd) Run() error {
	requireZextras()
	setProxyProtocol(c.Protocol, "FALSE")

	return nil
}

// ProxyStatusCmd shows proxy protocol statuses.
type ProxyStatusCmd struct{}

// Run executes the proxy status command.
//
//nolint:unparam // Kong interface requires error return
func (c *ProxyStatusCmd) Run() error {
	showProxyStatus()

	return nil
}

// ProxyRewriteCmd handles the fallthrough case: "configd proxy" without
// enable/disable/status triggers a forced config rewrite for proxy.
type ProxyRewriteCmd struct {
	ExtraConfigs []string `arg:"" optional:"" help:"Additional config names"`
}

// Run executes the proxy rewrite fallthrough.
//
//nolint:unparam // Kong interface requires error return
func (c *ProxyRewriteCmd) Run() error {
	requireZextras()
	proxyContactDaemon(append([]string{"proxy"}, c.ExtraConfigs...))

	return nil
}

// proxyContactDaemon sends a REWRITE request for the given configs to the running
// configd daemon. Shared by ProxyGenCmd and ProxyRewriteCmd.
func proxyContactDaemon(configs []string) {
	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading localconfig: %v\n", err)
		os.Exit(1)
	}

	listenPort, _ := strconv.Atoi(lc["zmconfigd_listen_port"])
	if listenPort == 0 {
		listenPort = 7171
	}

	ipMode := lc["zimbraIPMode"]
	if ipMode == "" {
		ipMode = ipModeIPv4
	}

	if ContactService("REWRITE", configs, listenPort, ipMode) {
		fmt.Fprintln(os.Stderr,
			"Error: could not contact configd service. Is carbonio-configd.service running?")
		os.Exit(1)
	}
}

func setProxyProtocol(protocol, value string) {
	attr, ok := proxyProtocols[strings.ToLower(protocol)]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown protocol: %s\n", protocol)
		fmt.Fprintln(os.Stderr, "Valid protocols: http, https, imap, imaps, pop3, pop3s")
		os.Exit(1)
	}

	client, hostname := connectLDAP()

	serverDN := fmt.Sprintf("cn=%s,cn=servers,cn=zimbra", hostname)

	if err := client.ModifyAttribute(serverDN, attr, value); err != nil {
		_ = client.Close()

		fmt.Fprintf(os.Stderr, "Error: failed to set %s=%s: %v\n", attr, value, err)
		os.Exit(1)
	}

	_ = client.Close()

	action := "Enabled"
	if value == "FALSE" {
		action = "Disabled"
	}

	fmt.Printf("%s %s proxy\n", action, protocol)
}

func showProxyStatus() {
	client, hostname := connectLDAP()

	serverDN := fmt.Sprintf("cn=%s,cn=servers,cn=zimbra", hostname)

	attrs := make([]string, 0, len(proxyProtocols))
	for _, attr := range proxyProtocols {
		attrs = append(attrs, attr)
	}

	entry, err := client.GetEntry(serverDN, attrs)

	_ = client.Close()

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Proxy Protocol Status:")

	for protocol, attr := range proxyProtocols {
		val := entry.GetAttributeValue(attr)
		proxyStatus := "disabled"

		if strings.EqualFold(val, "TRUE") {
			proxyStatus = "enabled"
		}

		fmt.Printf("  %-8s %s\n", protocol, proxyStatus)
	}
}

func connectLDAP() (client *carboldap.Client, hostname string) {
	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading localconfig: %v\n", err)
		os.Exit(1)
	}

	ldapURL := lc["ldap_master_url"]
	if ldapURL == "" {
		fmt.Fprintln(os.Stderr, "Error: LDAP not configured (ldap_master_url is empty)")
		fmt.Fprintln(os.Stderr, "Directory server may not be running or localconfig is incomplete")
		os.Exit(1)
	}

	client, err = carboldap.NewClient(&carboldap.ClientConfig{
		URL:      ldapURL,
		BindDN:   lc["zimbra_ldap_userdn"],
		Password: lc["zimbra_ldap_password"],
		StartTLS: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to LDAP: %v\n", err)
		os.Exit(1)
	}

	return client, lc["zimbra_server_hostname"]
}
