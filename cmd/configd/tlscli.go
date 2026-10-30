// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	configtls "github.com/zextras/carbonio-configd/internal/tls"
)

// errFmt is the "Error: <msg>" stderr line of the legacy CLI tools.
const errFmt = "Error: %v\n"

// rewriteConfigs is the set of Carbonio configs rewritten after a successful
// mail-mode change (matches the legacy zmtlsctl script).
var rewriteConfigs = []string{
	"sasl", "webxml", componentMailbox, "service", "zextras", "zextrasAdmin", "zimlet",
}

// TLSCmd handles "configd tls" — the Go port of legacy zmtlsctl.
type TLSCmd struct {
	// Target zimbraMailMode (both|http|https|mixed|redirect).
	// If omitted, only the config rewrite is performed.
	Mode  string `arg:"" optional:"" help:"Target zimbraMailMode"`
	Force bool   `name:"force" help:"Skip proxy cross-constraint validation"`
	Host  string `name:"host" help:"Override detected hostname (defaults to zimbra_server_hostname)"`
}

// Run executes the tls command: validate requested mode against the cluster's
// proxies, apply zimbraMailMode to the local server entry, then trigger a
// configd in-process rewrite of the affected configs.
func (c *TLSCmd) Run(cli *CLI) error {
	requireZextras()

	ctx := initializeLogging()
	ctx = logger.ContextWithComponent(ctx, "tls")

	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading localconfig: %v\n", err)
		os.Exit(1)
	}

	// Step 1: resolve hostname.
	hostname := c.Host
	if hostname == "" {
		hostname = lc["zimbra_server_hostname"]
	}

	if hostname == "" {
		fmt.Fprintln(os.Stderr, "Error: could not determine hostname (zimbra_server_hostname is empty, pass --host)")
		os.Exit(1)
	}

	// Step 2: if a target mode was provided, apply it via LDAP.
	if c.Mode != "" {
		mode, parseErr := configtls.ParseMode(c.Mode)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, errFmt, parseErr)
			os.Exit(1)
		}

		if err := applyMailMode(lc, hostname, mode, c.Force); err != nil {
			return err
		}
	}

	// Step 3: rewrite affected configs (same path as `configd rewrite`).
	args := &Args{
		DisableRestarts: cli.DisableRestarts,
		ForcedConfigs:   rewriteConfigs,
	}

	// Legacy tlsctl.sh prints this fixed text (no newline) before "done.".
	fmt.Print("Rewriting config files for cyrus-sasl, webxml, mailboxd, service, zimbraUI, and zimbraAdmin...")

	if code := forcedRewrite(ctx, args, runStandaloneRewrite); code != 0 {
		os.Exit(code)
	}

	fmt.Println("done.")

	return nil
}

// applyMailMode performs the LDAP-side of the mail-mode change:
//  1. Connect to LDAP (master URL for writes).
//  2. If the host participates in reverse proxying and proxies exist,
//     validate the requested mode against each proxy's constraints,
//     unless --force was passed.
//  3. Write zimbraMailMode on the local server entry.
func applyMailMode(lc map[string]string, hostname string, mode configtls.Mode, force bool) error {
	client, err := openWriteLDAPClient(lc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	defer func() { _ = client.Close() }()

	if !force {
		if err := validateAgainstProxies(client, hostname, mode); err != nil {
			fmt.Fprintf(os.Stderr, errFmt, err)
			fmt.Fprintln(os.Stderr, "Re-run with --force to bypass proxy cross-constraint validation.")

			return fmt.Errorf("proxy validation failed")
		}
	}

	fmt.Printf("Attempting to set ldap config zimbraMailMode %s on host %s...", mode, hostname)

	if err := configtls.SetMailMode(client, hostname, mode); err != nil {
		fmt.Println("failed.")
		fmt.Fprintf(os.Stderr, errFmt, err)

		return fmt.Errorf("set mail mode: %w", err)
	}

	fmt.Println("done.")

	return nil
}

// validateAgainstProxies returns nil if validation can be skipped (host not
// a reverse-proxy backend or no proxies present) OR if every designated proxy
// accepts the requested mode. Returns a descriptive error on the first
// violation it encounters.
func validateAgainstProxies(client *carboldap.Client, hostname string, mode configtls.Mode) error {
	isBackend, err := configtls.IsReverseProxyBackend(client, hostname)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v — skipping proxy validation\n", err)

		return nil
	}

	if !isBackend {
		fmt.Fprintf(os.Stderr,
			"Warning: %s is not a reverse-proxy backend (zimbraReverseProxyLookupTarget!=TRUE). No validation performed.\n",
			hostname,
		)

		return nil
	}

	proxies, err := configtls.GetProxiesForHost(client, hostname)
	if err != nil {
		return fmt.Errorf("enumerate proxies: %w", err)
	}

	if len(proxies) == 0 {
		fmt.Fprintln(os.Stderr, "Warning: no proxy servers detected. No validation performed.")

		return nil
	}

	// Validate against the first proxy whose settings can be read — mirrors the
	// legacy script, which broke out of its loop on the first proxy that
	// produced both attributes. Unreadable proxies are skipped; the first
	// readable one decides: a constraint violation there is final and later
	// proxies are not consulted.
	var lastErr error

	for _, proxy := range proxies {
		fmt.Printf("Attempting to query proxy (%s) settings for zimbraReverseProxyMailMode "+
			"and zimbraReverseProxySSLToUpstreamEnabled.\n", proxy)

		settings, err := configtls.ReadProxySettings(client, proxy)
		if err != nil {
			lastErr = err

			continue
		}

		fmt.Printf(
			"On proxy (%s): zimbraReverseProxyMailMode='%s', zimbraReverseProxySSLToUpstreamEnabled='%s'\n",
			proxy, settings.MailMode, strings.ToUpper(strconv.FormatBool(settings.SSLToUpstreamTrue)),
		)

		if err := configtls.ValidateMode(mode, settings); err != nil {
			return err
		}

		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("could not read settings from any proxy: %w", lastErr)
	}

	return nil
}
