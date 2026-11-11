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
	"github.com/zextras/carbonio-configd/internal/postfixconf"
	pfclient "github.com/zextras/carbonio-configd/internal/postfixconf/client"
)

// startTLSYes is the Postfix boolean literal for "yes", used in LDAP table rendering.
const startTLSYes = "yes"

// The MTA is the CommandSpec registry entry: `postfix start|stop|status|
// reload` through sudo, with the three Prepare steps below run before a start
// of a stopped postfix. The legacy /opt/zextras/bin/postfix start
// wrapper composed the same steps; each is reimplemented natively so
// carbonio-configd carries no runtime dependency on the shell-script MTA
// control path:
//
//  1. Idempotence probe (the generic launcher): `postfix status` exits 0 when
//     already running; then the start is skipped — otherwise postfix's own
//     startup emits "postfix/postlog: fatal: the Postfix mail system is
//     already running" and the caller sees a failed start for a healthy
//     daemon.
//  2. Hook prepareMainCf: postfix refuses to start without main.cf. When absent,
//     seed mail_owner / setgid_group from localconfig through the root helper
//     carbonio-postfix-conf (common/conf is root-owned), which creates it.
//  3. Hook prepareLDAPTables: write the 8 ldap-*.cf files under /opt/zextras/conf
//     that postfix's main.cf references (ldap:/opt/zextras/conf/ldap-vmm.cf
//     etc.). This replaces legacy mtainit.sh, and like the legacy wrapper it
//     also runs before every reload (CommandSpec.ReloadPrepare), so changed
//     ldap_url / ldap_postfix_password reach postfix without a cold start.
//  4. Run step: `sudo postalias /etc/aliases` rebuilds the aliases database
//     when /etc/aliases exists (best effort: a failure is logged).
//  5. Spawn the daemon (the generic launcher): sudo postfix start. Postfix
//     self-daemonizes, so the command returns once the master has forked.
//
// ConfigRewrite on the registry entry still triggers
// /opt/zextras/libexec/configrewrite before the start — that regenerates
// main.cf itself and is intentionally kept outside of these steps so it runs
// for every start path (including systemd).

// prepareMainCf is the main.cf bootstrap step of the MTA start.
func (c *Controller) prepareMainCf(ctx context.Context) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("load localconfig: %w", err)
	}

	if err := c.bootstrapPostfixMainCf(ctx, lc); err != nil {
		return fmt.Errorf("bootstrap main.cf: %w", err)
	}

	return nil
}

// prepareLDAPTables is the ldap-*.cf rendering step of the MTA start.
func (c *Controller) prepareLDAPTables(ctx context.Context) error {
	lc, err := c.loadConfig()
	if err != nil {
		return fmt.Errorf("load localconfig: %w", err)
	}

	if err := c.writePostfixLDAPConfig(ctx, lc); err != nil {
		return fmt.Errorf("render ldap-*.cf: %w", err)
	}

	return nil
}

// postfixAccountNameRe is the shape of a POSIX user/group name accepted for
// postconf's mail_owner/setgid_group. It rules out option-like (leading "-"),
// whitespace and "=" values read from localconfig before they reach sudo.
var postfixAccountNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

var errInvalidPostfixAccount = errors.New("invalid postfix owner/group name")

// bootstrapPostfixMainCf ensures /opt/zextras/common/conf/main.cf exists
// with mail_owner and setgid_group set. The directory is root-owned (CO-4621),
// so the seed is applied by the root helper carbonio-postfix-conf. Skips silently when the file is
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

	if owner == "" || group == "" {
		logger.WarnContext(ctx,
			"postfix_mail_owner/postfix_setgid_group missing from localconfig; skipping postconf seed",
			"owner", owner, "group", group)

		return nil
	}

	// common/conf is root-owned and zextras has no sudo right for touch or
	// postconf: the seed goes through the root helper carbonio-postfix-conf,
	// which creates the missing main.cf and runs postconf -e.
	change := postfixconf.Change{Set: []postfixconf.KV{
		{Key: "mail_owner", Value: owner},
		{Key: "setgid_group", Value: group},
	}}

	if err := pfclient.NewClient(c.p.base, c.env, c.IsSystemdMode).Apply(ctx, change); err != nil {
		return fmt.Errorf("seed %s with mail_owner/setgid_group: %w", c.p.mainCf, err)
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
