// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapcmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Defaults of "configd ldap wait".
const (
	// DefaultWaitTimeout bounds the wait, as the systemd unit always did.
	DefaultWaitTimeout = 120 * time.Second
	// defaultWaitInterval separates two connection rounds.
	defaultWaitInterval = 2 * time.Second
	// waitAttemptTimeout bounds one connection attempt to one URL.
	waitAttemptTimeout = 5 * time.Second
)

// WaitOptions are the arguments of "configd ldap wait".
type WaitOptions struct {
	// URLs override ldap_url (repeat for failover).
	URLs []string
	// ConfigPath is the localconfig.xml to read (default: the install's).
	ConfigPath string
	// FS is the filesystem to read from (default: the real one).
	FS sys.FS
	// Timeout bounds the whole wait (default DefaultWaitTimeout).
	Timeout time.Duration
	// Interval separates two connection rounds (default 2s).
	Interval time.Duration
}

// Wait blocks until one of the LDAP URLs configd reads from (ldap_url, with the
// same transport security) accepts a connection, and returns the exit status:
// 0 when one does, 1 on timeout or an unreadable localconfig.
//
// With no LDAP URL configured it returns 0 at once, with a warning: the daemon
// itself starts in that state (its LDAP queries fail), so waiting would only
// delay it by the whole timeout.
func Wait(ctx context.Context, stderr io.Writer, opts *WaitOptions) int {
	cliutil.InitCLILogging(stderr)

	if err := waitReachable(ctx, stderr, opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}

func waitReachable(ctx context.Context, stderr io.Writer, opts *WaitOptions) error {
	fsys := opts.FS
	if fsys == nil {
		fsys = sys.RealFS{}
	}

	lc, err := effectiveConfig(fsys, &Options{URLs: opts.URLs, ConfigPath: opts.ConfigPath})
	if err != nil {
		return err
	}

	urls := carboldap.ParseURLs(lc[lcKeyURL])
	if len(urls) == 0 {
		_, _ = fmt.Fprintln(stderr, "Warning: no LDAP URL configured (ldap_url is empty): not waiting for LDAP")

		return nil
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}

	interval := opts.Interval
	if interval <= 0 {
		interval = defaultWaitInterval
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	deadline, _ := ctx.Deadline()
	dialOpts := []goldap.DialOpt{goldap.DialWithDialer(&net.Dialer{Timeout: waitAttemptTimeout, Deadline: deadline})}

	if tlsSetup := carboldap.ResolveTLS(ctx, lc, fsys); tlsSetup.Config != nil {
		dialOpts = append(dialOpts, goldap.DialWithTLSConfig(tlsSetup.Config))
	}

	var lastErr error

	for {
		conn, url, err := carboldap.DialFirstReachable(ctx, urls, dialOpts...)
		if err == nil {
			_ = conn.Close()

			_, _ = fmt.Fprintf(stderr, "LDAP is reachable at %s\n", url)

			return nil
		}

		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("LDAP (%s) not reachable after %s: %w", strings.Join(urls, " "), timeout, lastErr)
		case <-time.After(interval):
		}
	}
}
