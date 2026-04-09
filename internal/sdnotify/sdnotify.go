// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package sdnotify implements the systemd sd_notify protocol for service readiness,
// status reporting, and watchdog keep-alive notifications.
//
// It communicates with systemd via the NOTIFY_SOCKET Unix datagram socket.
// This is a pure-Go implementation with zero external dependencies and no CGo,
// so it works correctly with CGO_ENABLED=0 static builds.
//
// When NOTIFY_SOCKET is not set (e.g., running outside systemd or with Type=simple),
// all operations are no-ops and return nil errors.
package sdnotify

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Well-known sd_notify state strings.
const (
	// Ready tells systemd that service startup is complete.
	// For Type=notify services, systemd waits for this before considering
	// the service as "active".
	Ready = "READY=1"

	// Stopping tells systemd the service is beginning its shutdown sequence.
	Stopping = "STOPPING=1"

	// Reloading tells systemd the service is reloading its configuration.
	Reloading = "RELOADING=1"

	// Watchdog is the keep-alive ping for the systemd watchdog.
	// Must be sent at least every WatchdogSec/2 to prevent systemd
	// from killing the service.
	Watchdog = "WATCHDOG=1"
)

// ErrNoSocket is returned when NOTIFY_SOCKET is not set.
// It is not treated as an error in normal operation -- callers should
// check with errors.Is and handle it as a no-op.
var ErrNoSocket = errors.New("NOTIFY_SOCKET not set")

// Notifier manages the connection to the systemd notification socket.
type Notifier struct {
	socketAddr *net.UnixAddr
}

// New creates a Notifier from the NOTIFY_SOCKET environment variable.
// Returns (nil, nil) if NOTIFY_SOCKET is not set, indicating sd_notify
// should be a no-op.
func New() (*Notifier, error) {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	if socketPath == "" {
		return nil, nil //nolint:nilnil // nil notifier is a deliberate no-op sentinel
	}

	// systemd supports abstract sockets (prefixed with @) and filesystem sockets
	socketPath = strings.TrimSpace(socketPath)
	if socketPath[0] == '@' {
		// Abstract socket: replace @ with null byte for net.UnixAddr
		socketPath = "\x00" + socketPath[1:]
	}

	addr := &net.UnixAddr{
		Name: socketPath,
		Net:  "unixgram",
	}

	return &Notifier{socketAddr: addr}, nil
}

// Notify sends a raw state string to systemd.
// If the notifier is nil (NOTIFY_SOCKET was not set), this is a no-op.
func (n *Notifier) Notify(state string) error {
	if n == nil {
		return nil
	}

	conn, err := net.DialUnix("unixgram", nil, n.socketAddr)
	if err != nil {
		return fmt.Errorf("sdnotify: dial %s: %w", n.socketAddr.Name, err)
	}
	defer conn.Close() //nolint:errcheck // best-effort close on datagram socket

	_, err = conn.Write([]byte(state))
	if err != nil {
		return fmt.Errorf("sdnotify: write: %w", err)
	}

	return nil
}

// Ready tells systemd that the service is ready.
func (n *Notifier) Ready() error {
	return n.Notify(Ready)
}

// Stopping tells systemd the service is stopping.
func (n *Notifier) Stopping() error {
	return n.Notify(Stopping)
}

// Reloading tells systemd the service is reloading.
func (n *Notifier) Reloading() error {
	return n.Notify(Reloading)
}

// WatchdogPing sends a watchdog keep-alive to systemd.
func (n *Notifier) WatchdogPing() error {
	return n.Notify(Watchdog)
}

// Status sends a free-form status string to systemd, displayed in
// "systemctl status" output.
func (n *Notifier) Status(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return n.Notify("STATUS=" + msg)
}

// Enabled returns true if sd_notify is available (NOTIFY_SOCKET is set).
func (n *Notifier) Enabled() bool {
	return n != nil
}

// WatchdogEnabled returns the watchdog interval if WATCHDOG_USEC is set.
// Returns (0, false) if watchdog is not enabled.
// The recommended ping interval is half the returned duration.
func WatchdogEnabled() (time.Duration, bool) {
	usecStr := os.Getenv("WATCHDOG_USEC")
	if usecStr == "" {
		return 0, false
	}

	var usec int64

	_, err := fmt.Sscanf(usecStr, "%d", &usec)
	if err != nil || usec <= 0 {
		return 0, false
	}

	return time.Duration(usec) * time.Microsecond, true
}
