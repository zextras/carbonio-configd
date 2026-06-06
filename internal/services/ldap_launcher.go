// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
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

	// slapd is a long-lived detached daemon: its lifetime must NOT be bound to
	// the start request's context. With exec.CommandContext(ctx, ...) a later
	// cancellation of ctx (e.g. a future cancellable ServiceStart caller) would
	// SIGKILL slapd out from under the running system. Use a background context
	// so only an explicit stop terminates it.
	cmd := exec.CommandContext(context.Background(), commonPath+"/libexec/slapd", args...)
	cmd.Stdout = logFd
	cmd.Stderr = logFd
	cmd.SysProcAttr = detachedSysProcAttr()

	logger.InfoContext(ctx, "Starting LDAP server", "bind_url", bindURL, "log", logFile)

	// The bundled slapd is built WITHOUT libsystemd, so it never emits
	// sd_notify READY=1. Waiting on a notify datagram therefore always burns
	// the full timeout (the daemon is up but silent), which made the first
	// `zmcontrol start` report the directory server as Failed while a second
	// start — seeing slapd already listening — succeeded. Instead we start
	// slapd and actively probe the LDAP endpoint until it answers, so readiness
	// reflects "actually serving" and returns in ~1s.
	return startThenProbe(ctx, cmd, bindURL)
}

// ldapReadyTimeout bounds how long startThenProbe waits for slapd to begin
// answering LDAP queries before declaring the start failed.
const ldapReadyTimeout = 30 * time.Second

// ldapProbeInterval is the poll cadence for the readiness probe.
const ldapProbeInterval = 250 * time.Millisecond

// ldapStopTimeout bounds the graceful SIGTERM wait before escalating to SIGKILL.
// slapd must flush and cleanly close its LMDB backing store within this window;
// an unclean kill can force LMDB recovery on the next start and stall readiness.
const ldapStopTimeout = 10 * time.Second

// ldapCustomStop stops slapd gracefully (SIGTERM, bounded wait, SIGKILL
// fallback) so LMDB closes cleanly. A hard SIGKILL skips the clean shutdown and
// can leave the next start doing recovery work that exceeds the readiness wait.
func ldapCustomStop(ctx context.Context, def *ServiceDef) error {
	return gracefulStopViaPidfile(ctx, pidDir+"/slapd.pid", "LDAP server", ldapStopTimeout)
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
		host = localhostName
	}

	return fmt.Sprintf("ldap://%s:%s", host, port)
}

// ldapProbeFn performs one readiness probe against the given LDAP URLs.
// Overridable in tests. Returns nil when slapd answers.
var ldapProbeFn = probeLDAPReady

// startThenProbe starts cmd and waits until the LDAP server actually answers a
// query (or the child exits, ctx is cancelled, or ldapReadyTimeout elapses).
// This replaces sd_notify readiness for slapd, which is built without
// libsystemd and never sends READY=1.
func startThenProbe(ctx context.Context, cmd *exec.Cmd, bindURL string) error {
	const service = "ldap"

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", service, err)
	}

	// Detect early child exit so we fail fast instead of polling for the full
	// timeout when slapd dies during startup (e.g. cannot bind its listener).
	exited := make(chan error, 1)

	go func() { exited <- cmd.Wait() }()

	// bindURL may contain multiple space-separated URLs (HA); probe all.
	urls := carboldap.ParseURLs(bindURL)

	deadline := time.Now().Add(ldapReadyTimeout)
	ticker := time.NewTicker(ldapProbeInterval)

	defer ticker.Stop()

	for {
		if err := ldapProbeFn(ctx, urls); err == nil {
			logger.InfoContext(ctx, "LDAP server is answering queries", "service", service, "urls", urls)

			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case waitErr := <-exited:
			return fmt.Errorf("%s exited during startup before becoming ready: %w", service, waitErr)
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("%s did not become ready within %s", service, ldapReadyTimeout)
			}
		}
	}
}

// probeLDAPReady dials the first reachable LDAP URL and performs an anonymous
// rootDSE base search. Success means slapd is accepting connections AND serving
// queries — a half-open listener that hasn't finished initializing fails the
// search, so this does not false-positive. No credentials are required.
func probeLDAPReady(ctx context.Context, urls []string) error {
	conn, _, err := carboldap.DialFirstReachable(ctx, urls)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	req := ldap.NewSearchRequest(
		"", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false,
		"(objectClass=*)", []string{"namingContexts"}, nil,
	)

	if _, err := conn.Search(req); err != nil {
		return fmt.Errorf("rootDSE probe search failed: %w", err)
	}

	return nil
}
