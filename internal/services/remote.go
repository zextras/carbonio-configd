// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// validateName checks that a service or action name contains only safe characters.
// This prevents injection via SSH remote commands.
var safeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validateName(name, kind string) error {
	if !safeNamePattern.MatchString(name) {
		return fmt.Errorf(
			"invalid %s name %q: must contain only alphanumeric chars, hyphens, underscores",
			kind, name,
		)
	}

	return nil
}

// RemoteHostStart executes a service start command on a remote host via SSH.
func RemoteHostStart(ctx context.Context, host, service string) error {
	if err := validateName(service, "service"); err != nil {
		return err
	}

	return remoteExec(ctx, host, "start", service, "Remote service started")
}

// RemoteHostStop executes a service stop command on a remote host via SSH.
func RemoteHostStop(ctx context.Context, host, service string) error {
	if err := validateName(service, "service"); err != nil {
		return err
	}

	return remoteExec(ctx, host, "stop", service, "Remote service stopped")
}

// RemoteHostStatus queries service status on a remote host via SSH.
func RemoteHostStatus(ctx context.Context, host, service string) (bool, error) {
	if err := validateName(service, "service"); err != nil {
		return false, err
	}

	client, err := sshConnect(host)
	if err != nil {
		return false, fmt.Errorf("failed to connect to %s: %w", host, err)
	}

	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		return false, fmt.Errorf("failed to create SSH session: %w", err)
	}

	defer func() { _ = session.Close() }()

	cmd := fmt.Sprintf("/usr/bin/sudo -u zextras %s control status %s", binPath+"/configd", service)

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return false, fmt.Errorf("remote status failed on %s: %s: %w", host, strings.TrimSpace(string(output)), err)
	}

	status := strings.TrimSpace(string(output))

	return strings.Contains(status, "Running"), nil
}

// remoteExec is a helper that executes a control command on a remote host via SSH.
func remoteExec(ctx context.Context, host, action, service, successMsg string) error {
	if err := validateName(action, "action"); err != nil {
		return err
	}

	client, err := sshConnect(host)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", host, err)
	}

	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}

	defer func() { _ = session.Close() }()

	cmd := fmt.Sprintf("/usr/bin/sudo -u zextras %s control %s %s", binPath+"/configd", action, service)

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return fmt.Errorf("remote %s failed on %s: %s: %w", action, host, strings.TrimSpace(string(output)), err)
	}

	logger.InfoContext(ctx, successMsg, "host", host, "service", service)

	return nil
}

// sshConnect establishes an SSH connection to the remote host using zextras user's SSH key.
func sshConnect(host string) (*ssh.Client, error) {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = basePath
	}

	keyPath := filepath.Join(homeDir, ".ssh", "id_rsa")

	//nolint:gosec // SSH key path is constructed from user's home directory
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key %s: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH key: %w", err)
	}

	// Load known_hosts for host key verification — fail closed if missing.
	knownHostsPath := filepath.Join(homeDir, ".ssh", "known_hosts")

	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("SSH host key verification failed: known_hosts not readable at %s. "+
			"Run: ssh-keyscan -H %s >> %s  (or ensure known_hosts is provisioned): %w",
			knownHostsPath, host, knownHostsPath, err)
	}

	config := &ssh.ClientConfig{
		User: "zextras",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	// Add default SSH port if not specified
	if !strings.Contains(host, ":") {
		host = net.JoinHostPort(host, "22")
	}

	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return nil, fmt.Errorf("SSH dial failed: %w", err)
	}

	return client, nil
}
