// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
)

// rewriteViaConfigdWithPort is a test helper that mirrors rewriteViaConfigd
// but accepts an explicit port so we can inject a test server.
func rewriteViaConfigdWithPort(ctx context.Context, configs []string, port int) error {
	addr := net.JoinHostPort("localhost", strconv.Itoa(port))
	dialer := &net.Dialer{}

	conn, err := dialer.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	msg := "REWRITE "
	for i, c := range configs {
		if i > 0 {
			msg += " "
		}
		msg += c
	}
	msg += "\n"

	if _, err := conn.Write([]byte(msg)); err != nil {
		return err
	}

	buf := make([]byte, 2048)

	n, err := conn.Read(buf)
	if err != nil {
		return err
	}

	resp := string(buf[:n])
	if len(resp) >= 5 && resp[:5] == "ERROR" {
		return errors.New("configd returned error: " + resp)
	}

	return nil
}

// withMode forces IsSystemdMode() to return the given value for the duration
// of a test, then restores the production detector. The two orchestration
// modes are mutually exclusive; this helper lets each test pin the one it
// means to exercise without depending on the host's target enablement.
func withMode(t *testing.T, strict bool) {
	t.Helper()

	orig := isSystemdModeFn
	isSystemdModeFn = func() bool { return strict }

	t.Cleanup(func() { isSystemdModeFn = orig })
}
