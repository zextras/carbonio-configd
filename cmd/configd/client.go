// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// ContactService attempts to connect to a running configd instance and send a command.
// It returns true if the service is unavailable or an error occurs, false otherwise.
func ContactService(command string, args []string, listenPort int, ipMode string) bool {
	listenerParams := net.JoinHostPort("localhost", fmt.Sprintf("%d", listenPort))

	var (
		conn net.Conn
		err  error
	)

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if ipMode == ipModeIPv4 {
		conn, err = dialer.DialContext(context.Background(), "tcp4", listenerParams)
	} else {
		conn, err = dialer.DialContext(context.Background(), "tcp6", listenerParams)
	}

	ctx := context.Background()
	ctx = logger.ContextWithComponent(ctx, "client")

	if err != nil {
		logger.WarnContext(ctx, "Service unavailable",
			"error", err)

		return true // Service unavailable
	}

	defer func() {
		if err := conn.Close(); err != nil {
			logger.ErrorContext(ctx, "Error closing connection",
				"error", err)
		}
	}()

	message := command
	if len(args) > 0 {
		message += " " + strings.Join(args, " ")
	}

	message += "\n"

	logger.DebugContext(ctx, "Requesting command",
		"message", strings.TrimSpace(message))

	_, err = conn.Write([]byte(message))
	if err != nil {
		logger.ErrorContext(ctx, "Error sending message to service",
			"error", err)

		return true
	}

	// Read response
	buffer := make([]byte, 2048)

	n, err := conn.Read(buffer)
	if err != nil {
		logger.ErrorContext(ctx, "Error reading response from service",
			"error", err)

		return true
	}

	response := strings.TrimSpace(string(buffer[:n]))

	if strings.HasPrefix(response, "ERROR") {
		logger.ErrorContext(ctx, "Service returned error",
			"response", response)

		return true
	}

	logger.InfoContext(ctx, "Service returned response",
		"response", response)

	return false
}
