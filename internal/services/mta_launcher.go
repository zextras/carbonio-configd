// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// Postfix binary and config paths. All entries point at upstream postfix —
// no legacy shell wrappers (/opt/zextras/bin/postfix, /opt/zextras/libexec/
// zmmtastatus, /opt/zextras/libexec/zmmtainit) are invoked from this
// package. Their logic has been reimplemented natively below.
var sudoBin = "/usr/bin/sudo"

var (
	postfixBin   = commonPath + "/sbin/postfix"
	postconfBin  = commonPath + "/sbin/postconf"
	postaliasBin = commonPath + "/sbin/postalias"
	mainCfPath   = commonPath + "/conf/main.cf"
	aliasesPath  = "/etc/aliases"
)

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
func mtaCustomStart(ctx context.Context, _ *ServiceDef) error {
	lc, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load localconfig: %w", err)
	}

	if mtaIsRunning(ctx) {
		logger.InfoContext(ctx, "postfix already running; skipping start")

		return nil
	}

	if err := bootstrapPostfixMainCf(ctx, lc); err != nil {
		return fmt.Errorf("bootstrap main.cf: %w", err)
	}

	if err := writePostfixLDAPConfig(ctx, lc); err != nil {
		return fmt.Errorf("render ldap-*.cf: %w", err)
	}

	runPostalias(ctx)

	return startPostfixDaemon(ctx)
}

// mtaCustomStop runs "sudo postfix stop" to gracefully shut down postfix.
// zextras has NOPASSWD sudo rights for postfix binary.
func mtaCustomStop(ctx context.Context, _ *ServiceDef) error {
	// #nosec G204 — fixed binary path from internal registry
	cmd := exec.CommandContext(ctx, sudoBin, postfixBin, "stop")

	out, err := cmd.CombinedOutput()
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
func mtaIsRunning(ctx context.Context) bool {
	// #nosec G204 — fixed binary path from internal registry
	cmd := exec.CommandContext(ctx, sudoBin, postfixBin, "status")

	return cmd.Run() == nil
}

// bootstrapPostfixMainCf ensures /opt/zextras/common/conf/main.cf exists
// with mail_owner and setgid_group set. Skips silently when the file is
// already present — configrewrite owns the rest of the file's contents.
func bootstrapPostfixMainCf(ctx context.Context, lc map[string]string) error {
	if _, err := os.Stat(mainCfPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", mainCfPath, err)
	}

	// sudo touch — main.cf lives under /opt/zextras/common which is
	// root-owned; zextras cannot create files there directly.
	if err := sudoRun(ctx, "/usr/bin/touch", mainCfPath); err != nil {
		return fmt.Errorf("touch %s: %w", mainCfPath, err)
	}

	owner := strings.TrimSpace(lc["postfix_mail_owner"])
	group := strings.TrimSpace(lc["postfix_setgid_group"])

	if owner == "" || group == "" {
		logger.WarnContext(ctx,
			"postfix_mail_owner/postfix_setgid_group missing from localconfig; skipping postconf seed",
			"owner", owner, "group", group)

		return nil
	}

	if err := sudoRun(ctx, postconfBin, "-e",
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
// Files are written atomically via os.WriteFile with 0640 so only root and
// the postfix group can read bind_pw. The files are then chgrp'd to the
// postfix group (matches legacy `chgrp postfix`); chgrp failures are
// logged and non-fatal — on fresh installs the postfix group may not
// exist yet.
func writePostfixLDAPConfig(ctx context.Context, lc map[string]string) error {
	ldapURL := strings.TrimSpace(lc["ldap_url"])
	ldapPort := strings.TrimSpace(lc["ldap_port"])
	bindPW := lc["ldap_postfix_password"]
	startTLS := "no"

	if isTruthy(lc["ldap_starttls_supported"]) {
		startTLS = "yes"
	}

	if ldapURL == "" || ldapPort == "" {
		return fmt.Errorf("ldap_url or ldap_port missing from localconfig")
	}

	if err := os.MkdirAll(confPath, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", confPath, err)
	}

	// Ordering: mirror legacy mtainit.sh so diffs between legacy and
	// native outputs can be compared byte-for-byte during migration.
	tables := []struct {
		name     string
		body     string
		extraTLS string
	}{
		{"ldap-vmm.cf", ldapQueryVMM, ""},
		{"ldap-vmd.cf", ldapQueryVMD, ""},
		{"ldap-vam.cf", ldapQueryVAM, "special_result_attribute = member\n"},
		{"ldap-vad.cf", ldapQueryVAD, ""},
		{"ldap-canonical.cf", ldapQueryCanonical, ""},
		{"ldap-transport.cf", ldapQueryTransport, ""},
		{"ldap-slm.cf", ldapQuerySLM, ""},
		{"ldap-splitdomain.cf", ldapQuerySplitdomain, ""},
	}

	for _, t := range tables {
		path := confPath + "/" + t.name
		content := renderLDAPTable(ldapURL, ldapPort, startTLS, bindPW, t.body, t.extraTLS)

		// 0640: bind_pw must stay off world-read while remaining group-readable
		// by postfix (chgrp'd below). Owner 0600 would lock postfix out.
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil { //nolint:gosec
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	chgrpPostfixLDAPFiles(ctx)

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
func chgrpPostfixLDAPFiles(ctx context.Context) {
	grp, err := user.LookupGroup("postfix")
	if err != nil {
		logger.WarnContext(ctx, "postfix group not found; leaving ldap-*.cf at default group",
			"error", err)

		return
	}

	gid, err := strconv.Atoi(grp.Gid)
	if err != nil {
		logger.WarnContext(ctx, "postfix group has non-numeric gid", "gid", grp.Gid, "error", err)

		return
	}

	for _, name := range []string{
		"ldap-vmm.cf", "ldap-vmd.cf", "ldap-vam.cf", "ldap-vad.cf",
		"ldap-canonical.cf", "ldap-transport.cf", "ldap-slm.cf", "ldap-splitdomain.cf",
	} {
		path := confPath + "/" + name
		if err := os.Chown(path, -1, gid); err != nil {
			logger.WarnContext(ctx, "chgrp postfix failed",
				"path", path, "error", err)
		}
	}
}

// runPostalias rebuilds /etc/aliases.db from /etc/aliases when the source
// file exists. Best-effort — a missing aliases file or a postalias failure
// is logged and non-fatal, matching legacy postfix wrapper behavior.
func runPostalias(ctx context.Context) {
	if _, err := os.Stat(aliasesPath); err != nil {
		return
	}

	if err := sudoRun(ctx, postaliasBin, aliasesPath); err != nil {
		logger.WarnContext(ctx, "postalias failed; continuing",
			"aliases", aliasesPath, "error", err)
	}
}

func startPostfixDaemon(ctx context.Context) error {
	// #nosec G204 — fixed binary path from internal registry
	cmd := exec.CommandContext(ctx, sudoBin, postfixBin, "start")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("postfix start: %s: %w",
			strings.TrimSpace(string(out)), err)
	}

	return nil
}

func sudoRun(ctx context.Context, binary string, args ...string) error {
	full := append([]string{binary}, args...)
	// #nosec G204 — binary path is a registry constant; args are localconfig scalars or literals
	cmd := exec.CommandContext(ctx, sudoBin, full...)

	out, err := cmd.CombinedOutput()
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
