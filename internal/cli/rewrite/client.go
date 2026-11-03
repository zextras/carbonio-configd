// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package rewrite

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

const (
	// dialTimeout bounds one connection attempt; the daemon listens on the
	// loopback, so anything slower than this is not a listening daemon.
	dialTimeout = 5 * time.Second

	// maxReplyBytes is the size of the single read that carries the reply,
	// like the legacy recv(2048).
	maxReplyBytes = 2048

	networkTCP4 = "tcp4"
	networkTCP6 = "tcp6"
)

// ErrUnreachable reports that nothing listens on the daemon port, on either
// loopback family. It is the only error after which a caller may assume that
// no daemon is running.
var ErrUnreachable = errors.New("configd service not reachable")

// ReplyError is an "ERROR ..." reply of a running daemon.
type ReplyError struct {
	// Reply is the reply line, e.g. "ERROR REWRITE TIMEOUT".
	Reply string
}

func (e *ReplyError) Error() string { return "configd service returned " + e.Reply }

// Endpoint is where the daemon listens and how long a request may take.
type Endpoint struct {
	// Port is the TCP port of the listener.
	Port int
	// IPMode is the zimbraIPMode hint: "ipv6" tries the IPv6 loopback first,
	// anything else the IPv4 one first. The other family is always tried next.
	IPMode string
	// Timeout bounds the wait for a reply, which a REWRITE only sends once the
	// main loop has processed the sections. Zero means defaultReplyTimeout.
	Timeout time.Duration
}

// networks returns the dial order for the loopback families. The daemon binds
// the IPv6 loopback when zimbraIPMode is ipv6 and the IPv4 one otherwise
// (legacy binds ::1 for every mode but ipv4); the mode lives in LDAP, which a
// CLI cannot rely on, so the family that does not match the hint is tried too
// rather than concluding that no daemon is running.
func (e Endpoint) networks() []string {
	if e.IPMode == IPModeIPv6 {
		return []string{networkTCP6, networkTCP4}
	}

	return []string{networkTCP4, networkTCP6}
}

// Request sends command to the daemon and returns its reply line. The error is
// ErrUnreachable when no daemon listens on the port, a *ReplyError when the
// daemon answered "ERROR ...", and another error when a daemon was reached but
// the exchange failed.
func Request(ctx context.Context, command string, args []string, ep Endpoint) (string, error) {
	ctx = logger.ContextWithComponent(ctx, "client")

	conn, err := dialDaemon(ctx, ep)
	if err != nil {
		logger.WarnContext(ctx, "Service unavailable", "error", err)

		return "", err
	}

	defer func() {
		if err := conn.Close(); err != nil {
			logger.ErrorContext(ctx, "Error closing connection", "error", err)
		}
	}()

	timeout := ep.Timeout
	if timeout <= 0 {
		timeout = defaultReplyTimeout
	}

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", fmt.Errorf("set deadline: %w", err)
	}

	message := command
	if len(args) > 0 {
		message += " " + strings.Join(args, " ")
	}

	logger.DebugContext(ctx, "Requesting command", "message", message)

	if _, err := conn.Write([]byte(message + "\n")); err != nil {
		return "", fmt.Errorf("send %s: %w", command, err)
	}

	buffer := make([]byte, maxReplyBytes)

	n, err := conn.Read(buffer)
	if err != nil {
		return "", fmt.Errorf("read reply to %s: %w", command, err)
	}

	reply := strings.TrimSpace(string(buffer[:n]))

	if strings.HasPrefix(reply, "ERROR") {
		logger.DebugContext(ctx, "Service returned error", "response", reply)

		return reply, &ReplyError{Reply: reply}
	}

	logger.InfoContext(ctx, "Service returned response", "response", reply)

	return reply, nil
}

// dialDaemon connects to the first loopback family the daemon answers on.
func dialDaemon(ctx context.Context, ep Endpoint) (net.Conn, error) {
	port := strconv.Itoa(ep.Port)
	dialer := &net.Dialer{Timeout: dialTimeout}

	var errs []error

	for _, network := range ep.networks() {
		host := "127.0.0.1"
		if network == networkTCP6 {
			host = "::1"
		}

		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		if err == nil {
			return conn, nil
		}

		errs = append(errs, err)
	}

	return nil, fmt.Errorf("%w on port %d: %w", ErrUnreachable, ep.Port, errors.Join(errs...))
}
