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

// RunDHParam executes "carbonio-bootstrap dhparam": conf/dhparam.pem.crb
// with the RFC 7919 ffdhe2048 group, recorded in zimbraSSLDHParam.
func RunDHParam(ctx context.Context, env sys.System, stdout, stderr io.Writer, args []string) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintf(stderr, "Usage: dhparam\n")

		return 2
	}

	return runTool(ctx, env, stdout, stderr, "dhparam", installDHParam)
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
