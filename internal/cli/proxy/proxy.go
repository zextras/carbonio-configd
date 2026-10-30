// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy implements "configd proxy": nginx configuration printing
// (zmproxyconf), proxy config generation (zmproxyconfgen) and protocol
// enable/disable/status (zmproxyctl). The cmd/configd subcommands only map
// parsed flags onto Options.
package proxy

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	"github.com/zextras/carbonio-configd/internal/localconfig"
)

const (
	componentProxy = "proxy"
	schemeHTTPS    = "https"
)

// proxyProtocols maps protocol names to their LDAP attributes.
var proxyProtocols = map[string]string{
	"http":      "zimbraReverseProxyHttpEnabled",
	schemeHTTPS: "zimbraReverseProxySSLToUpstreamEnabled",
	"mail":      "zimbraReverseProxyMailEnabled",
	"imap":      "zimbraReverseProxyMailImapEnabled",
	"imaps":     "zimbraReverseProxyMailImapsEnabled",
	"pop3":      "zimbraReverseProxyMailPop3Enabled",
	"pop3s":     "zimbraReverseProxyMailPop3sEnabled",
}

// Action selects which "configd proxy" subcommand to run.
type Action string

// The proxy subcommands.
const (
	ActionConf    Action = "conf"
	ActionGen     Action = "gen"
	ActionEnable  Action = "enable"
	ActionDisable Action = "disable"
	ActionStatus  Action = "status"
	ActionRewrite Action = "rewrite"
)

// Options are the parsed command-line flags of "configd proxy <action>".
type Options struct {
	Action Action

	// conf: path to nginx.conf (default <base>/conf/nginx.conf) and filters.
	ConfigFile string
	Markers    bool
	Indent     bool
	NoComments bool
	NoEmpty    bool

	// enable/disable: protocol name.
	Protocol string

	// gen/rewrite: additional config names to regenerate.
	ExtraConfigs []string

	// Standalone is the in-process rewrite used when no daemon answers.
	Standalone rewrite.Standalone
}

// ldapConn is the subset of the LDAP client the enable/disable/status
// actions use.
type ldapConn interface {
	ModifyAttribute(dn, attribute, value string) error
	GetEntry(dn string, attributes []string) (*ldap.Entry, error)
	Close() error
}

// Run executes the proxy action and returns the process exit status.
// conf and status do not require the zextras user, as in the legacy tools.
func Run(_ context.Context, stdout, stderr io.Writer, opts *Options) int {
	switch opts.Action {
	case ActionConf:
		return runConf(stdout, stderr, opts)
	case ActionStatus:
		return withLDAP(stdout, stderr, showProxyStatus)
	case ActionGen, ActionRewrite:
		if !cliutil.RequireZextras(stderr) {
			return 1
		}

		return contactDaemon(stderr, opts)
	case ActionEnable, ActionDisable:
		if !cliutil.RequireZextras(stderr) {
			return 1
		}

		value := "TRUE"
		if opts.Action == ActionDisable {
			value = "FALSE"
		}

		return setProxyProtocol(stdout, stderr, opts.Protocol, value)
	}

	_, _ = fmt.Fprintf(stderr, "Unknown proxy action: %s\n", opts.Action)

	return 1
}

func runConf(stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	conf := opts.ConfigFile
	if conf == "" {
		conf = cliutil.BasePath("conf", "nginx.conf")
	}

	if _, err := os.Stat(cliutil.BasePath("common", "sbin", "nginx")); err != nil {
		_, _ = fmt.Fprintln(stdout, "Nginx not installed, exiting")

		return 0
	}

	printNginxConf(stdout, conf, 0, &nginxConfOpts{
		markers:    opts.Markers,
		indent:     opts.Indent,
		noComments: opts.NoComments,
		noEmpty:    opts.NoEmpty,
	})

	return 0
}

// contactDaemon sends a REWRITE request for the proxy configs to the running
// configd daemon, rewriting in-process when it is not running (legacy
// configrewrite). Shared by gen and rewrite.
func contactDaemon(stderr io.Writer, opts *Options) int {
	ctx := cliutil.InitLogging(stderr)

	return rewrite.Forced(ctx, append([]string{componentProxy}, opts.ExtraConfigs...), opts.Standalone)
}

// withLDAP loads localconfig, dials LDAP and runs fn against the connection.
// Failures are reported on stderr and yield exit status 1.
func withLDAP(stdout, stderr io.Writer, fn func(stdout, stderr io.Writer, conn ldapConn, serverDN string) int) int {
	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error loading localconfig: %v\n", err)

		return 1
	}

	client, err := cliutil.OpenWriteLDAPClient(lc)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return fn(stdout, stderr, client, serverDN(lc[cliutil.LCKeyZimbraServerHostname]))
}

func serverDN(hostname string) string {
	return fmt.Sprintf("cn=%s,cn=servers,cn=zimbra", hostname)
}

func setProxyProtocol(stdout, stderr io.Writer, protocol, value string) int {
	attr, ok := proxyProtocols[strings.ToLower(protocol)]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "Unknown protocol: %s\n", protocol)
		_, _ = fmt.Fprintln(stderr, "Valid protocols: http, https, imap, imaps, pop3, pop3s")

		return 1
	}

	return withLDAP(stdout, stderr, func(stdout, stderr io.Writer, conn ldapConn, dn string) int {
		return modifyProtocol(stdout, stderr, conn, dn, protocol, attr, value)
	})
}

func modifyProtocol(stdout, stderr io.Writer, conn ldapConn, dn, protocol, attr, value string) int {
	err := conn.ModifyAttribute(dn, attr, value)

	_ = conn.Close()

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: failed to set %s=%s: %v\n", attr, value, err)

		return 1
	}

	action := "Enabled"
	if value == "FALSE" {
		action = "Disabled"
	}

	_, _ = fmt.Fprintf(stdout, "%s %s proxy\n", action, protocol)

	return 0
}

func showProxyStatus(stdout, stderr io.Writer, conn ldapConn, dn string) int {
	attrs := make([]string, 0, len(proxyProtocols))
	for _, attr := range proxyProtocols {
		attrs = append(attrs, attr)
	}

	entry, err := conn.GetEntry(dn, attrs)

	_ = conn.Close()

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	_, _ = fmt.Fprintln(stdout, "Proxy Protocol Status:")

	for protocol, attr := range proxyProtocols {
		proxyStatus := "disabled"

		if strings.EqualFold(entry.GetAttributeValue(attr), "TRUE") {
			proxyStatus = "enabled"
		}

		_, _ = fmt.Fprintf(stdout, "  %-8s %s\n", protocol, proxyStatus)
	}

	return 0
}
