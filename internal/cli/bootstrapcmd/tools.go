// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// The commands the legacy setup.pl calls instead of zmgsautil, zmsshkeygen
// and zmupdateauthkeys. They are the steps' own functions, replaced by tests.
var (
	geteuid              = os.Geteuid
	createGalSyncAccount = setup.CreateGalSyncAccount
	generateSSHKey       = setup.GenerateSSHKey
	updateAuthKeys       = setup.UpdateAuthKeys
	setupSyslog          = setup.ConfigureSyslog
	zextrasUID           = lookupZextrasUID
	updateAuthKeysAll    = setup.UpdateAuthKeysEverywhere
	installDHParam       = setup.InstallDHParam
	runProxyConfig       = setup.RunProxyConfig
	runDHParam           = setup.RunDHParam
	runLDAPPasswd        = setup.RunLDAPPasswd
	runLDAPSchema        = setup.RunLDAPSchema
	runLDAPUpdateLDIF    = setup.RunLDAPUpdateLDIF
	runLDAPEnableReplica = setup.RunLDAPEnableReplica
	runLDAPEnableMMR     = setup.RunLDAPEnableMMR
	runIPTool            = setup.RunIPTool
	runIniUtil           = setup.RunIniUtil
	runCertMgr           = setup.RunCertMgr
)

// lookupZextrasUID is the uid of zextras, -1 when there is no such user.
func lookupZextrasUID() int {
	u, err := user.Lookup(config.ZextrasUser)
	if err != nil {
		return -1
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return -1
	}

	return uid
}

// RunGalSyncAccount executes "carbonio-bootstrap gal-sync-account": it
// creates galsync.<random>@domain on server and prints "<name>\t<id>" as
// "zmgsautil createAccount" did. It returns the exit status.
func RunGalSyncAccount(ctx context.Context, env sys.System, stdout, stderr io.Writer, domain, server string) int {
	return runTool(ctx, env, stdout, stderr, "gal-sync-account", func(e *setup.Env) error {
		acct, err := createGalSyncAccount(e, domain, server)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(stdout, "%s\t%s\n", acct.Name, acct.ID)

		return err
	})
}

// RunSSHKeygen executes "carbonio-bootstrap ssh-keygen [rsa]" (zmsshkeygen,
// whose only key type is rsa).
func RunSSHKeygen(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	if len(args) > 1 || len(args) == 1 && args[0] != "rsa" {
		_, _ = fmt.Fprintf(stderr, "Bad keytype: %s\n", strings.Join(args, " "))

		return 1
	}

	return runTool(ctx, env, stdout, stderr, "ssh-keygen", generateSSHKey)
}

// RunUpdateAuthKeys executes "carbonio-bootstrap update-authkeys [-a]"
// (zmupdateauthkeys): -a first has every server update its own keys.
func RunUpdateAuthKeys(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	switch {
	case len(args) == 0:
		return runTool(ctx, env, stdout, stderr, "update-authkeys", updateAuthKeys)
	case len(args) == 1 && args[0] == "-a":
		return runTool(ctx, env, stdout, stderr, "update-authkeys", updateAuthKeysAll)
	default:
		_, _ = fmt.Fprintf(stderr, "Usage: update-authkeys [-a]\n")

		return 2
	}
}

// RunDHParam executes "setup dhparam". Without arguments it is the step of
// setup.pl: conf/dhparam.pem.crb with the RFC 7919 ffdhe2048 group, recorded
// in zimbraSSLDHParam. With arguments it is zmdhparam ("get" and "set", see
// setup.RunDHParam), whose exit status and output it keeps; stdin feeds
// "-in -".
func RunDHParam(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		return runTool(ctx, env, stdout, stderr, "dhparam", installDHParam)
	}

	e, ok := toolEnv(ctx, env, stdout, stderr, "dhparam")
	if !ok {
		return 1
	}

	return runDHParam(e, args, stdin, stdout, stderr)
}

// RunLDAPPasswd executes "setup ldap-passwd": the port of zmldappasswd with
// its options and exit statuses (see setup.RunLDAPPasswd). The password is the
// argument after the options or, without one, the first line of stdin. Like
// the script, it runs as root or zextras.
func RunLDAPPasswd(ctx context.Context, env sys.System, stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	e, ok := toolEnv(ctx, env, stdout, stderr, "ldap-passwd")
	if !ok {
		return 1
	}

	return runLDAPPasswd(e, args, stdin, stdout, stderr)
}

// RunSyslog executes "setup syslog" (zmsyslogsetup): it sets rsyslog up for
// Carbonio and prints what it changed, as the script did. The script took no
// arguments (and ignored any); they are rejected here. It rewrites /etc, so
// unlike the other tools it is for root only.
func RunSyslog(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintf(stderr, "Usage: syslog\n")

		return 2
	}

	if geteuid() != 0 {
		_, _ = fmt.Fprintf(stderr, "Error: carbonio-bootstrap syslog must run as root\n")

		return 1
	}

	return runTool(ctx, env, stdout, stderr, "syslog", setupSyslog)
}

// runTool runs fn as root or zextras (the scripts it replaces ran as
// zextras, zmrc runs them remotely) against the node, with no bootstrap
// configuration: fn reads what it needs from localconfig. Progress goes to
// stdout, the details to stderr; a failure is printed and the status is 1.
func runTool(ctx context.Context, env sys.System, stdout, stderr io.Writer, name string,
	fn func(*setup.Env) error,
) int {
	e, ok := toolEnv(ctx, env, stdout, stderr, name)
	if !ok {
		return 1
	}

	if err := fn(e); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}

// toolEnv is the Env of a tool, false after printing why it must not run: the
// caller is neither root nor zextras.
func toolEnv(ctx context.Context, env sys.System, stdout, stderr io.Writer, name string) (*setup.Env, bool) {
	uid := geteuid()
	if uid != 0 && uid != zextrasUID() {
		_, _ = fmt.Fprintf(stderr, "Error: carbonio-bootstrap %s must run as root or %s\n", name, config.ZextrasUser)

		return nil, false
	}

	return &setup.Env{
		Ctx: ctx, Sys: env, Log: setup.NewLog(stdout, stderr), Paths: setup.DefaultPaths(),
		IsZextras: uid != 0,
	}, true
}

// RunCertMgr executes "setup certmgr": the port of bin/zmcertmgr, which
// carbonio-core's wrapper calls with the command and options of the script.
// args are what follows zmcertmgr (see setup.RunCertMgr); the exit status and
// the output are the script's. Like it, it runs as zextras, and as root too,
// where it gives what it creates to zextras.
func RunCertMgr(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	e, ok := toolEnv(ctx, env, stdout, stderr, "certmgr")
	if !ok {
		return 1
	}

	// The script prints no command lines: the log has no file (-debug adds one).
	e.Log = setup.NewLog(stdout, nil)

	return runCertMgr(e, args, stdout, stderr)
}

// RunProxyConfig executes "setup proxyconfig": the port of libexec/zmproxyconfig,
// which carbonio-core's wrapper calls with the options of the script. args are
// its options (see setup.ParseProxyConfigArgs); the exit status and the
// output are the script's. Like it, it runs as root or zextras.
func RunProxyConfig(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	e, ok := toolEnv(ctx, env, stdout, stderr, "proxyconfig")
	if !ok {
		return 1
	}

	return runProxyConfig(e, args, stdout, stderr)
}

// RunLDAPSchema executes "setup ldap-schema": zmldapschema, which installs the
// slapd schemas (see setup.RunLDAPSchema). The script ran as zextras; it runs
// as root or zextras here.
func RunLDAPSchema(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	e, ok := toolEnv(ctx, env, stdout, stderr, "ldap-schema")
	if !ok {
		return 1
	}

	return runLDAPSchema(e, args, stdout, stderr)
}

// RunLDAPUpdateLDIF executes "setup ldap-update-ldif": zmldapupdateldif (see
// setup.RunLDAPUpdateLDIF). Like the script it is for root only.
func RunLDAPUpdateLDIF(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	e, ok := rootToolEnv(ctx, env, stdout, stderr, "ldap-update-ldif")
	if !ok {
		return 1
	}

	return runLDAPUpdateLDIF(e, args, stdout, stderr)
}

// RunLDAPEnableReplica executes "setup ldap-enable-replica":
// zmldapenablereplica (see setup.RunLDAPEnableReplica). Like the script it is
// for root only.
func RunLDAPEnableReplica(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	e, ok := rootToolEnv(ctx, env, stdout, stderr, "ldap-enable-replica")
	if !ok {
		return 1
	}

	return runLDAPEnableReplica(e, args, stdout, stderr)
}

// RunLDAPEnableMMR executes "setup ldap-enable-mmr": zmldapenable-mmr (see
// setup.RunLDAPEnableMMR). The script ran as zextras; it runs as root or
// zextras here.
func RunLDAPEnableMMR(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	e, ok := toolEnv(ctx, env, stdout, stderr, "ldap-enable-mmr")
	if !ok {
		return 1
	}

	return runLDAPEnableMMR(e, args, stdout, stderr)
}

// RunIPTool executes "setup iptool": zmiptool (see setup.RunIPTool). The
// script ran as zextras; it runs as root or zextras here.
func RunIPTool(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	e, ok := toolEnv(ctx, env, stdout, stderr, "iptool")
	if !ok {
		return 1
	}

	return runIPTool(e, args, stdout, stderr)
}

// RunIniUtil executes "setup iniutil": zminiutil (see setup.RunIniUtil). The
// script checked no user, and neither does this.
func RunIniUtil(_ context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	return runIniUtil(env.FS, args, stdout, stderr)
}

// rootToolEnv is toolEnv for a script that refused any user but root, with the
// message it printed.
func rootToolEnv(ctx context.Context, env sys.System, stdout, stderr io.Writer, name string) (*setup.Env, bool) {
	if geteuid() != 0 {
		_, _ = fmt.Fprintln(stderr, "Error: must be run as root user")

		return nil, false
	}

	return toolEnv(ctx, env, stdout, stderr, name)
}
