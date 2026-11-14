// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/cli/rewrite"
	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// Readiness windows of the legacy launchers.
const (
	// mailboxPortAttempts x mailboxPortInterval: zmmailboxdctl asked zmprov for
	// zimbraMailPort up to 6 times, 5s apart; the port is now read from LDAP,
	// with the same attempts.
	mailboxPortAttempts = 6
	mailboxPortInterval = 5 * time.Second
	// mailboxDialAttempts x mailboxDialInterval: then it waits up to 180s for
	// the port to accept connections — mailboxd's own DB-connect retry window,
	// so the JVM has headroom under boot-time contention (CO-4008). Legacy
	// zmmailboxdctl polled every 5s; with mailboxd up in ~6s that added up to
	// 5s to every start, so poll every 250ms over the same window.
	mailboxDialAttempts = 720
	mailboxDialInterval = 250 * time.Millisecond
	mailboxDialTimeout  = 2 * time.Second

	// The daemon answers STATUS only after its first (synchronous) rewrite
	// pass, which takes well under a second; poll often so start returns as
	// soon as it is ready, for as long as the unit's TimeoutStartSec (300s).
	// (Legacy zmconfigdctl slept 3s between probes, so the very first probe,
	// sent before the daemon was up, always cost a full 3s.)
	configdReadyTimeout  = 300 * time.Second
	configdReadyInterval = 100 * time.Millisecond
	configdProbeTimeout  = 3 * time.Second

	// attrMailPort is the attribute the mailbox port is read from.
	attrMailPort = "zimbraMailPort"

	defaultConfigdPort = 7171
	configdStatusReply = "SUCCESS ACTIVE"
)

// pause waits d on the injected clock, or until ctx is done (false).
func (c *Controller) pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-c.env.Clock.After(d):
		return true
	}
}

// waitForMailboxPort resolves the mailbox's zimbraMailPort from LDAP and
// waits until localhost accepts connections on it, like the tail of legacy
// zmmailboxdctl start. It is the default Controller.mailboxReady.
func (c *Controller) waitForMailboxPort(ctx context.Context, lc map[string]string) error {
	port, err := c.resolveMailboxPort(ctx, lc)
	if err != nil {
		return fmt.Errorf("mailboxd started but zimbraMailPort could not be resolved from LDAP: %w", err)
	}

	addr := net.JoinHostPort("localhost", port)
	dialer := &net.Dialer{Timeout: mailboxDialTimeout}

	for i := range mailboxDialAttempts {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()

			return nil
		}

		if i < mailboxDialAttempts-1 && !c.pause(ctx, mailboxDialInterval) {
			return fmt.Errorf("waiting for mailboxd on %s: %w", addr, ctx.Err())
		}
	}

	return fmt.Errorf("mailboxd did not open %s within %s",
		addr, time.Duration(mailboxDialAttempts)*mailboxDialInterval)
}

// resolveMailboxPort returns the zimbraMailPort of the local server, trying
// up to mailboxPortAttempts times; the error is the last failure (also when
// the context ended while it waited).
func (c *Controller) resolveMailboxPort(ctx context.Context, lc map[string]string) (string, error) {
	var last error

	for i := range mailboxPortAttempts {
		port, err := c.mailPortOf(ctx, lc, lc["zimbra_server_hostname"])
		if err == nil {
			return port, nil
		}

		last = err

		if i < mailboxPortAttempts-1 && !c.pause(ctx, mailboxPortInterval) {
			return "", fmt.Errorf("%w (%w)", last, ctx.Err())
		}
	}

	return "", last
}

// errNoMailPort is a server entry that has no usable zimbraMailPort.
var errNoMailPort = errors.New("the server has no valid zimbraMailPort")

// readMailPort is "zmprov -l gs <host> zimbraMailPort" over LDAP: the server's
// own zimbraMailPort, else the global config one it inherits
// (ldap.Client.GetServerConfig). It is the default Controller.mailPortOf.
func (c *Controller) readMailPort(ctx context.Context, lc map[string]string, host string) (string, error) {
	params, err := carboldap.ParamsFrom(ctx, lc, carboldap.PurposeRead, c.env.FS)
	if err != nil {
		return "", err
	}

	client, err := carboldap.NewClient(params)
	if err != nil {
		return "", err
	}

	defer func() { _ = client.Close() }()

	attrs, err := client.GetServerConfig(host)
	if err != nil {
		return "", err
	}

	for name, value := range attrs {
		if !strings.EqualFold(name, attrMailPort) {
			continue
		}

		if _, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return strings.TrimSpace(value), nil
		}
	}

	return "", errNoMailPort
}

// configdEndpoint is where the configd daemon listens, from localconfig
// (zmconfigd_listen_port, default 7171).
func (c *Controller) configdEndpoint() rewrite.Endpoint {
	lc, _ := c.loadConfig()

	return rewrite.ListenerAddress(lc)
}

// configdStatus asks the running configd daemon for STATUS and returns its
// one-line answer.
func (c *Controller) configdStatus(ctx context.Context) (string, error) {
	ep := c.configdEndpoint()
	ep.Timeout = configdProbeTimeout

	return rewrite.Request(ctx, "STATUS", nil, ep)
}

// waitForConfigd polls the configd daemon's STATUS until it answers
// "SUCCESS ACTIVE", like legacy zmconfigdctl start (zmconfigd_request STATUS),
// every configdReadyInterval for up to configdReadyTimeout. It is the default
// Controller.configdReady.
func (c *Controller) waitForConfigd(ctx context.Context) error {
	var last error

	deadline := c.env.Clock.Now().Add(configdReadyTimeout)

	for {
		reply, err := c.configdStatus(ctx)

		switch {
		case err != nil:
			last = err
		case reply == configdStatusReply:
			return nil
		default:
			last = fmt.Errorf("unexpected STATUS reply %q", reply)
		}

		if !c.env.Clock.Now().Before(deadline) {
			break
		}

		if !c.pause(ctx, configdReadyInterval) {
			return fmt.Errorf("waiting for configd: %w", ctx.Err())
		}
	}

	logger.WarnContext(ctx, "configd did not become ready", "error", last)

	return fmt.Errorf("configd did not become ready within %s: %w", configdReadyTimeout, last)
}

// awaitReady runs the readiness probe def.Ready names, after the service has
// been launched. exited delivers the launched child's exit for probes that
// must fail fast when it dies (ReadyLDAP); other probes ignore it.
func (c *Controller) awaitReady(ctx context.Context, def *ServiceDef, exited <-chan error) error {
	switch def.Ready {
	case ReadyNone:
		return nil
	case ReadyConfigd:
		return c.configdReady(ctx)
	case ReadyLDAP, ReadyMailboxPort:
		lc, err := c.loadConfig()
		if err != nil {
			return fmt.Errorf("failed to load localconfig: %w", err)
		}

		if def.Ready == ReadyLDAP {
			return c.awaitLDAPReady(ctx, lc, exited)
		}

		return c.mailboxReady(ctx, lc)
	default:
		return fmt.Errorf("%s: unknown readiness probe %q", def.Name, def.Ready)
	}
}
