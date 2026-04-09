// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// ldapCustomStart builds and executes the slapd command with bind_url from localconfig.
func ldapCustomStart(ctx context.Context, def *ServiceDef) error {
	lc, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load localconfig: %w", err)
	}

	// Build bind_url from localconfig (matches systemd-envscript logic)
	bindURL := buildLDAPBindURL(lc)

	args := []string{
		"-d", "0",
		"-l", "LOCAL0",
		"-h", bindURL + " ldapi:///",
		"-F", dataPath + "/ldap/config",
	}

	logFile := logPath + "/slapd.out"

	logFd, err := openLogFile(logFile)
	if err != nil {
		return err
	}

	defer func() { _ = logFd.Close() }()

	cmd := exec.CommandContext(ctx, commonPath+"/libexec/slapd", args...)
	cmd.Stdout = logFd
	cmd.Stderr = logFd
	cmd.SysProcAttr = detachedSysProcAttr()

	logger.InfoContext(ctx, "Starting LDAP server", "bind_url", bindURL, "log", logFile)

	// startWithSDNotify sets NOTIFY_SOCKET, starts slapd, and blocks until
	// READY=1 is received — so DiscoverEnabledServices never runs before slapd
	// is ready. slapd writes its own pidfile via olcPidFile in cn=config.
	return startWithSDNotify(ctx, cmd, "ldap")
}

// ldapCustomStop kills the slapd process via its pidfile.
func ldapCustomStop(ctx context.Context, def *ServiceDef) error {
	return signalViaPidfile(ctx, pidDir+"/slapd.pid", "LDAP server", syscall.SIGKILL)
}

// buildLDAPBindURL constructs the LDAP bind URL from localconfig.
// Matches the old shell script logic: prefer ldap_bind_url, fall back to
// the first URL in ldap_url, then reconstruct from zimbra_server_hostname/ldap_port.
func buildLDAPBindURL(lc map[string]string) string {
	// Prefer explicit bind URL (may contain multiple space-separated URLs)
	if bindURL := lc["ldap_bind_url"]; bindURL != "" {
		return bindURL
	}

	// Fall back to first URL in ldap_url (matches ldap.sh lines 57-60)
	if urls := strings.Fields(lc["ldap_url"]); len(urls) > 0 {
		return urls[0]
	}

	// Last resort: reconstruct from individual keys
	port := lc["ldap_port"]
	if port == "" {
		port = "389"
	}

	host := lc["zimbra_server_hostname"]
	if host == "" {
		host = "localhost"
	}

	return fmt.Sprintf("ldap://%s:%s", host, port)
}
