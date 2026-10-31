// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// startTLSYes is the Postfix boolean literal for "yes", used in LDAP table rendering.
const startTLSYes = "yes"

// mtaCustomStart replaces legacy /opt/zextras/bin/postfix start. The legacy
// wrapper composed five steps; each is reimplemented natively here so
// carbonio-configd carries no runtime dependency on the shell-script MTA
// control path:
//
//  1. Idempotence probe: `postfix status` exits 0 when already running; if
//     it does, skip — otherwise postfix's own startup emits
//     "postfix/postlog: fatal: the Postfix mail system is already running"
//     and the caller sees a failed start for a healthy daemon.
//  2. Bootstrap main.cf: postfix refuses to start without it. When absent,
//     create an empty file and seed mail_owner / setgid_group from
//     localconfig via postconf -e.
//  3. Render LDAP tables: write the 8 ldap-*.cf files under /opt/zextras/
//     conf that postfix's main.cf references (ldap:/opt/zextras/conf/
//     ldap-vmm.cf etc.). This replaces legacy mtainit.sh.
//  4. Rebuild aliases database when /etc/aliases exists.
//  5. Spawn the daemon: sudo postfix start. Postfix self-daemonizes, so
//     CombinedOutput returns once the master has forked.
//
// ConfigRewrite on the registry entry still triggers
// /opt/zextras/libexec/configrewrite before this hook runs — that regenerates
// main.cf itself and is intentionally kept outside of this function so it
// runs for every start path (including systemd).
func (c *Controller) mtaCustomStart(ctx context.Context, _ *ServiceDef) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("load localconfig: %w", err)
	}

	if c.mtaIsRunning(ctx) {
		logger.InfoContext(ctx, "postfix already running; skipping start")

		return nil
	}

	if err := c.bootstrapPostfixMainCf(ctx, lc); err != nil {
		return fmt.Errorf("bootstrap main.cf: %w", err)
	}

	if err := c.writePostfixLDAPConfig(ctx, lc); err != nil {
		return fmt.Errorf("render ldap-*.cf: %w", err)
	}

	c.runPostalias(ctx)

	return c.startPostfixDaemon(ctx)
}

// mtaCustomStop runs "sudo postfix stop" to gracefully shut down postfix.
// zextras has NOPASSWD sudo rights for postfix binary.
func (c *Controller) mtaCustomStop(ctx context.Context, _ *ServiceDef) error {
	// #nosec G204 — fixed binary path from internal registry
	out, err := sys.CombinedOutput(ctx, c.env.Exec, sudoBin, c.p.postfixBin, "stop")
	if err != nil {
		// "not running" means postfix is already stopped — treat as success.
		if strings.Contains(string(out), "is not running") {
			return nil
		}

		return fmt.Errorf("postfix stop: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// mtaIsRunning is the idempotence gate before start. Replaces zmmtastatus.
func (c *Controller) mtaIsRunning(ctx context.Context) bool {
	// #nosec G204 — fixed binary path from internal registry
	_, err := c.env.Exec.Run(ctx, &sys.Cmd{Path: sudoBin, Args: []string{c.p.postfixBin, "status"}})

	return err == nil
}

// postfixAccountNameRe is the shape of a POSIX user/group name accepted for
// postconf's mail_owner/setgid_group. It rules out option-like (leading "-"),
// whitespace and "=" values read from localconfig before they reach sudo.
var postfixAccountNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

var errInvalidPostfixAccount = errors.New("invalid postfix owner/group name")

// bootstrapPostfixMainCf ensures /opt/zextras/common/conf/main.cf exists
// with mail_owner and setgid_group set. Skips silently when the file is
// already present — configrewrite owns the rest of the file's contents.
func (c *Controller) bootstrapPostfixMainCf(ctx context.Context, lc map[string]string) error {
	if _, err := c.env.FS.Stat(c.p.mainCf); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", c.p.mainCf, err)
	}

	owner := strings.TrimSpace(lc["postfix_mail_owner"])
	group := strings.TrimSpace(lc["postfix_setgid_group"])

	if (owner != "" || group != "") &&
		(!postfixAccountNameRe.MatchString(owner) || !postfixAccountNameRe.MatchString(group)) {
		return fmt.Errorf("%w: postfix_mail_owner=%q postfix_setgid_group=%q",
			errInvalidPostfixAccount, owner, group)
	}

	// sudo touch — main.cf lives under /opt/zextras/common which is
	// root-owned; zextras cannot create files there directly.
	if err := c.sudoRun(ctx, "/usr/bin/touch", c.p.mainCf); err != nil {
		return fmt.Errorf("touch %s: %w", c.p.mainCf, err)
	}

	if owner == "" || group == "" {
		logger.WarnContext(ctx,
			"postfix_mail_owner/postfix_setgid_group missing from localconfig; skipping postconf seed",
			"owner", owner, "group", group)

		return nil
	}

	if err := c.sudoRun(ctx, c.p.postconfBin, "-e",
		"mail_owner="+owner,
		"setgid_group="+group,
	); err != nil {
		return fmt.Errorf("postconf -e mail_owner/setgid_group: %w", err)
	}

	return nil
}

// writePostfixLDAPConfig writes the 8 ldap-*.cf files that postfix's
// main.cf references. Replaces legacy mtainit.sh.
//
// Files are written atomically (temp file + rename via FS.WriteFileAtomic)
// with an explicit 0640, so a pre-existing file never keeps a looser mode and
// readers never see a partial bind_pw. Only the owner and the postfix group
// can read them. The files are then chgrp'd to the postfix group (matches
// legacy `chgrp postfix`); chgrp failures are logged and non-fatal — on fresh
// installs the postfix group may not exist yet.
func (c *Controller) writePostfixLDAPConfig(ctx context.Context, lc map[string]string) error {
	ldapURL := strings.TrimSpace(lc["ldap_url"])
	ldapPort := strings.TrimSpace(lc["ldap_port"])
	bindPW := lc["ldap_postfix_password"]
	startTLS := "no"

	if config.IsTruthy(lc["ldap_starttls_supported"]) {
		startTLS = startTLSYes
	}

	if ldapURL == "" || ldapPort == "" {
		return fmt.Errorf("ldap_url or ldap_port missing from localconfig")
	}

	if err := c.env.FS.MkdirAll(c.p.conf, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", c.p.conf, err)
	}

	// Ordering: mirror legacy mtainit.sh so diffs between legacy and
	// native outputs can be compared byte-for-byte during migration.
	tables := []struct {
		name     string
		body     string
		extraTLS string
	}{
		{ldapVmm, ldapQueryVMM, ""},
		{ldapVmd, ldapQueryVMD, ""},
		{ldapVam, ldapQueryVAM, "special_result_attribute = member\n"},
		{ldapVad, ldapQueryVAD, ""},
		{ldapCanonical, ldapQueryCanonical, ""},
		{ldapTransport, ldapQueryTransport, ""},
		{ldapSlm, ldapQuerySLM, ""},
		{ldapSplitdomain, ldapQuerySplitdomain, ""},
	}

	for _, t := range tables {
		path := c.p.conf + "/" + t.name
		content := renderLDAPTable(ldapURL, ldapPort, startTLS, bindPW, t.body, t.extraTLS)

		// 0640: bind_pw must stay off world-read while remaining group-readable
		// by postfix (chgrp'd below). Owner 0600 would lock postfix out.
		if err := c.env.FS.WriteFileAtomic(path, []byte(content), 0o640); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	c.chgrpPostfixLDAPFiles(ctx)

	return nil
}

func renderLDAPTable(ldapURL, ldapPort, startTLS, bindPW, query, extra string) string {
	return fmt.Sprintf(`server_host = %s
server_port = %s
search_base =
%sversion = 3
start_tls = %s
tls_ca_cert_dir = /opt/zextras/conf/ca
bind = yes
bind_dn = uid=zmpostfix,cn=appaccts,cn=zimbra
bind_pw = %s
timeout = 30
%s`, ldapURL, ldapPort, query, startTLS, bindPW, extra)
}

// chgrpPostfixLDAPFiles best-effort chgrp of the 8 ldap-*.cf files to the
// postfix group. Mirrors legacy `chgrp postfix /opt/zextras/conf/ldap-*.cf`.
// When the group doesn't resolve (early-install race) we log and continue;
// file mode 0640 still keeps bind_pw off world-read.
func (c *Controller) chgrpPostfixLDAPFiles(ctx context.Context) {
	gid, err := c.env.Proc.LookupGID("postfix")
	if err != nil {
		logger.WarnContext(ctx, "postfix group not found; leaving ldap-*.cf at default group",
			"error", err)

		return
	}

	for _, name := range []string{
		ldapVmm, ldapVmd, ldapVam, ldapVad,
		ldapCanonical, ldapTransport, ldapSlm, ldapSplitdomain,
	} {
		path := c.p.conf + "/" + name
		if err := c.env.FS.Chown(path, -1, gid); err != nil {
			logger.WarnContext(ctx, "chgrp postfix failed",
				"path", path, "error", err)
		}
	}
}

// runPostalias rebuilds /etc/aliases.db from /etc/aliases when the source
// file exists. Best-effort — a missing aliases file or a postalias failure
// is logged and non-fatal, matching legacy postfix wrapper behavior.
func (c *Controller) runPostalias(ctx context.Context) {
	if _, err := c.env.FS.Stat(c.p.aliases); err != nil {
		return
	}

	if err := c.sudoRun(ctx, c.p.postaliasBin, c.p.aliases); err != nil {
		logger.WarnContext(ctx, "postalias failed; continuing",
			"aliases", c.p.aliases, "error", err)
	}
}

func (c *Controller) startPostfixDaemon(ctx context.Context) error {
	// #nosec G204 — fixed binary path from internal registry
	out, err := sys.CombinedOutput(ctx, c.env.Exec, sudoBin, c.p.postfixBin, "start")
	if err != nil {
		return fmt.Errorf("postfix start: %s: %w",
			strings.TrimSpace(string(out)), err)
	}

	return nil
}

func (c *Controller) sudoRun(ctx context.Context, binary string, args ...string) error {
	full := append([]string{binary}, args...)
	// #nosec G204 — binary path is a registry constant; args are localconfig scalars or literals
	out, err := sys.CombinedOutput(ctx, c.env.Exec, sudoBin, full...)
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w",
			binary, strings.Join(args, " "),
			strings.TrimSpace(string(out)), err)
	}

	return nil
}

// LDAP query bodies. Formatted so each table's query_filter and
// result_attribute live together; lines end with \n so they concatenate
// cleanly into the frame produced by renderLDAPTable. Long lines are by
// design — postfix reads these as literal config content, splitting would
// change what postfix sees.
//
//nolint:lll
const (
	ldapQueryVMM = `query_filter = (&(zimbraMailDeliveryAddress=%s)(zimbraMailStatus=enabled))
result_attribute = zimbraMailDeliveryAddress
`
	ldapQueryVMD = `query_filter = (&(zimbraDomainName=%s)(zimbraDomainType=local)(zimbraMailStatus=enabled))
result_attribute = zimbraDomainName
`
	ldapQueryVAM = `query_filter = (&(|(zimbraMailDeliveryAddress=%s)(zimbraMailAlias=%s)(zimbraOldMailAddress=%s)(zimbraMailCatchAllAddress=%s))(zimbraMailStatus=enabled))
result_attribute = zimbraMailDeliveryAddress,zimbraMailForwardingAddress,zimbraPrefMailForwardingAddress,zimbraMailCatchAllForwardingAddress
`
	ldapQueryVAD = `query_filter = (&(zimbraDomainName=%s)(zimbraDomainType=alias)(zimbraMailStatus=enabled))
result_attribute = zimbraDomainName
`
	ldapQueryCanonical = `query_filter = (&(|(zimbraMailDeliveryAddress=%s)(zimbraMailAlias=%s)(zimbraMailCatchAllAddress=%s))(zimbraMailStatus=enabled))
result_attribute = zimbraMailCanonicalAddress,zimbraMailCatchAllCanonicalAddress
`
	ldapQueryTransport = `query_filter = (&(|(zimbraMailDeliveryAddress=%s)(zimbraDomainName=%s))(zimbraMailStatus=enabled))
result_attribute = zimbraMailTransport
`
	ldapQuerySLM = `query_filter = (&(|(uid=%s)(zimbraMailDeliveryAddress=%s)(zimbraMailAlias=%s)(zimbraMailCatchAllAddress=%s)(zimbraAllowFromAddress=%s))(zimbraMailStatus=enabled))
result_format = %u, %s
result_attribute = uid,zimbraMailDeliveryAddress,zimbraMailForwardingAddress,zimbraPrefMailForwardingAddress,zimbraMailCatchAllForwardingAddress,zimbraMailAlias,zimbraAllowFromAddress
`
	ldapQuerySplitdomain = `query_filter = (&(|(zimbraMailDeliveryAddress=%s)(zimbraMailAlias=%s)(zimbraMailCatchAllAddress=%s))(zimbraMailStatus=enabled))
result_attribute = zimbraMailDeliveryAddress,zimbraMailForwardingAddress,zimbraPrefMailForwardingAddress
result_filter = OK
`
)
