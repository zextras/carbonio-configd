// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// Readiness windows of the legacy launchers.
const (
	// mailboxPortAttempts x mailboxPortInterval: zmmailboxdctl asks zmprov for
	// zimbraMailPort up to 6 times, 5s apart.
	mailboxPortAttempts = 6
	mailboxPortInterval = 5 * time.Second
	// mailboxDialAttempts x mailboxDialInterval: then it waits 36 x 5s = 180s
	// for the port to accept connections — mailboxd's own DB-connect retry
	// window, so the JVM has headroom under boot-time contention (CO-4008).
	mailboxDialAttempts = 36
	mailboxDialInterval = 5 * time.Second
	mailboxDialTimeout  = 2 * time.Second

	// configdReadyAttempts x configdReadyInterval: zmconfigdctl start waits up
	// to 10 x 3s for the daemon to answer STATUS.
	configdReadyAttempts = 10
	configdReadyInterval = 3 * time.Second
	configdProbeTimeout  = 3 * time.Second

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

// waitForMailboxPort resolves the mailbox's zimbraMailPort through zmprov and
// waits until localhost accepts connections on it, like the tail of legacy
// zmmailboxdctl start. It is the default Controller.mailboxReady.
func (c *Controller) waitForMailboxPort(ctx context.Context, lc map[string]string) error {
	port := c.resolveMailboxPort(ctx, lc["zimbra_server_hostname"])
	if port == "" {
		return errors.New("mailboxd started but zimbraMailPort could not be resolved with zmprov")
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

// resolveMailboxPort returns the zimbraMailPort of host ("" when zmprov never
// answered within the attempts).
func (c *Controller) resolveMailboxPort(ctx context.Context, host string) string {
	for i := range mailboxPortAttempts {
		out, err := sys.Output(ctx, c.env.Exec, c.p.bin+"/zmprov", "-l", "gs", host, "zimbraMailPort")
		if err == nil {
			if port := parseMailPort(string(out)); port != "" {
				return port
			}
		}

		if i < mailboxPortAttempts-1 && !c.pause(ctx, mailboxPortInterval) {
			return ""
		}
	}

	return ""
}

// parseMailPort extracts N from the "zimbraMailPort: N" line of zmprov output.
func parseMailPort(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key != "zimbraMailPort" {
			continue
		}

		if _, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

// configdPort is the TCP port the configd daemon listens on
// (zmconfigd_listen_port, default 7171).
func (c *Controller) configdPort() int {
	if lc, err := c.loadConfig(); err == nil {
		if p, convErr := strconv.Atoi(lc["zmconfigd_listen_port"]); convErr == nil && p > 0 {
			return p
		}
	}

	return defaultConfigdPort
}

// configdStatus asks the running configd daemon for STATUS and returns its
// one-line answer.
func (c *Controller) configdStatus(ctx context.Context) (string, error) {
	addr := net.JoinHostPort("localhost", strconv.Itoa(c.configdPort()))
	dialer := &net.Dialer{Timeout: configdProbeTimeout}

	conn, err := dialer.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return "", fmt.Errorf("configd not reachable at %s: %w", addr, err)
	}

	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(configdProbeTimeout))

	if _, err := conn.Write([]byte("STATUS\n")); err != nil {
		return "", fmt.Errorf("send STATUS: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read STATUS reply: %w", err)
	}

	return strings.TrimSpace(line), nil
}

// waitForConfigd polls the configd daemon's STATUS until it answers
// "SUCCESS ACTIVE", like legacy zmconfigdctl start (zmconfigd_request STATUS,
// 10 x 3s). It is the default Controller.configdReady.
func (c *Controller) waitForConfigd(ctx context.Context) error {
	var last error

	for i := range configdReadyAttempts {
		reply, err := c.configdStatus(ctx)

		switch {
		case err != nil:
			last = err
		case reply == configdStatusReply:
			return nil
		default:
			last = fmt.Errorf("unexpected STATUS reply %q", reply)
		}

		if i < configdReadyAttempts-1 && !c.pause(ctx, configdReadyInterval) {
			return fmt.Errorf("waiting for configd: %w", ctx.Err())
		}
	}

	logger.WarnContext(ctx, "configd did not become ready", "error", last)

	return fmt.Errorf("configd did not become ready within %s: %w",
		time.Duration(configdReadyAttempts)*configdReadyInterval, last)
}

// configdCustomStart spawns the detached configd daemon and waits until it
// answers STATUS.
func (c *Controller) configdCustomStart(ctx context.Context, def *ServiceDef) error {
	if err := c.startDirect(ctx, def.Name, def); err != nil {
		return err
	}

	return c.configdReady(ctx)
}
