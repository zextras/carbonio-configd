// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"net"
	"strconv"
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
