// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package tls implements "configd tls": the Go port of legacy zmtlsctl. It
// validates the requested mail mode against the cluster's proxies, applies
// zimbraMailMode on the local server entry and rewrites the affected configs.
package tls

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
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
	"sasl", "webxml", "mailbox", "service", "zextras", "zextrasAdmin", "zimlet",
}

// Options are the parsed command-line flags of "configd tls".
type Options struct {
	// Mode is the target zimbraMailMode (both|http|https|mixed|redirect).
	// If empty, only the config rewrite is performed.
	Mode string
	// Force skips the proxy cross-constraint validation.
	Force bool
	// Host overrides the detected hostname (zimbra_server_hostname).
	Host string
	// Standalone is the in-process rewrite used when no daemon answers.
	Standalone rewrite.Standalone
}

// Run executes the tls command and returns the process exit status: validate
// the requested mode against the cluster's proxies, apply zimbraMailMode to
// the local server entry, then rewrite the affected configs.
func Run(_ context.Context, stdout, stderr io.Writer, opts *Options) int {
	if !cliutil.RequireZextras(stderr) {
		return 1
	}

	ctx := cliutil.InitLogging(stderr)
	ctx = logger.ContextWithComponent(ctx, "tls")

	lc, err := localconfig.LoadResolvedConfig()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error loading localconfig: %v\n", err)

		return 1
	}

	return run(ctx, stdout, stderr, opts, lc)
}

func run(ctx context.Context, stdout, stderr io.Writer, opts *Options, lc map[string]string) int {
	// Step 1: resolve hostname.
	hostname := opts.Host
	if hostname == "" {
		hostname = lc[cliutil.LCKeyZimbraServerHostname]
	}

	if hostname == "" {
		_, _ = fmt.Fprintln(stderr, "Error: could not determine hostname (zimbra_server_hostname is empty, pass --host)")

		return 1
	}

	// Step 2: if a target mode was provided, apply it via LDAP.
	if opts.Mode != "" {
		mode, parseErr := configtls.ParseMode(opts.Mode)
		if parseErr != nil {
			_, _ = fmt.Fprintf(stderr, errFmt, parseErr)

			return 1
		}

		if code := applyMailMode(stdout, stderr, lc, hostname, mode, opts.Force); code != 0 {
			return code
		}
	}

	// Step 3: rewrite affected configs (same path as `configd rewrite`).
	// Legacy tlsctl.sh prints this fixed text (no newline) before "done.".
	_, _ = fmt.Fprint(stdout,
		"Rewriting config files for cyrus-sasl, webxml, mailboxd, service, zimbraUI, and zimbraAdmin...")

	if code := rewrite.Forced(ctx, rewriteConfigs, opts.Standalone); code != 0 {
		return code
	}

	_, _ = fmt.Fprintln(stdout, "done.")

	return 0
}

// applyMailMode performs the LDAP-side of the mail-mode change:
//  1. Connect to LDAP (master URL for writes).
//  2. If the host participates in reverse proxying and proxies exist,
//     validate the requested mode against each proxy's constraints,
//     unless --force was passed.
//  3. Write zimbraMailMode on the local server entry.
//
// It returns the exit status (0 on success); failures are reported on stderr.
func applyMailMode(stdout, stderr io.Writer, lc map[string]string, hostname string, mode configtls.Mode,
	force bool,
) int {
	client, err := cliutil.OpenWriteLDAPClient(lc)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, errFmt, err)

		return 1
	}

	defer func() { _ = client.Close() }()

	if !force {
		if err := validateAgainstProxies(stdout, stderr, client, hostname, mode); err != nil {
			_, _ = fmt.Fprintf(stderr, errFmt, err)
			_, _ = fmt.Fprintln(stderr, "Re-run with --force to bypass proxy cross-constraint validation.")
			_, _ = fmt.Fprintf(stderr, errFmt, fmt.Errorf("proxy validation failed"))

			return 1
		}
	}

	_, _ = fmt.Fprintf(stdout, "Attempting to set ldap config zimbraMailMode %s on host %s...", mode, hostname)

	if err := configtls.SetMailMode(client, hostname, mode); err != nil {
		_, _ = fmt.Fprintln(stdout, "failed.")
		_, _ = fmt.Fprintf(stderr, errFmt, err)
		_, _ = fmt.Fprintf(stderr, errFmt, fmt.Errorf("set mail mode: %w", err))

		return 1
	}

	_, _ = fmt.Fprintln(stdout, "done.")

	return 0
}

// validateAgainstProxies returns nil if validation can be skipped (host not
// a reverse-proxy backend or no proxies present) OR if every designated proxy
// accepts the requested mode. Returns a descriptive error on the first
// violation it encounters.
func validateAgainstProxies(
	stdout, stderr io.Writer, client *carboldap.Client, hostname string, mode configtls.Mode,
) error {
	isBackend, err := configtls.IsReverseProxyBackend(client, hostname)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Warning: %v — skipping proxy validation\n", err)

		return nil
	}

	if !isBackend {
		_, _ = fmt.Fprintf(stderr,
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
		_, _ = fmt.Fprintln(stderr, "Warning: no proxy servers detected. No validation performed.")

		return nil
	}

	// Validate against the first proxy whose settings can be read — mirrors the
	// legacy script, which broke out of its loop on the first proxy that
	// produced both attributes. Unreadable proxies are skipped; the first
	// readable one decides: a constraint violation there is final and later
	// proxies are not consulted.
	var lastErr error

	for _, proxy := range proxies {
		_, _ = fmt.Fprintf(stdout, "Attempting to query proxy (%s) settings for zimbraReverseProxyMailMode "+
			"and zimbraReverseProxySSLToUpstreamEnabled.\n", proxy)

		settings, err := configtls.ReadProxySettings(client, proxy)
		if err != nil {
			lastErr = err

			continue
		}

		_, _ = fmt.Fprintf(stdout,
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
