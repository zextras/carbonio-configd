// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// The commands the legacy setup.pl calls instead of zmgsautil, zmsshkeygen
// and zmupdateauthkeys. They are the steps' own functions, replaced by tests.
var (
	geteuid              = os.Geteuid
	createGalSyncAccount = setup.CreateGalSyncAccount
	generateSSHKey       = setup.GenerateSSHKey
	updateAuthKeys       = setup.UpdateAuthKeys
)

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

// RunSSHKeygen executes "carbonio-bootstrap ssh-keygen" (zmsshkeygen).
func RunSSHKeygen(ctx context.Context, env sys.System, stdout, stderr io.Writer) int {
	return runTool(ctx, env, stdout, stderr, "ssh-keygen", generateSSHKey)
}

// RunUpdateAuthKeys executes "carbonio-bootstrap update-authkeys"
// (zmupdateauthkeys without -a).
func RunUpdateAuthKeys(ctx context.Context, env sys.System, stdout, stderr io.Writer) int {
	return runTool(ctx, env, stdout, stderr, "update-authkeys", updateAuthKeys)
}

// runTool runs fn as root against the node, with no bootstrap configuration:
// fn reads what it needs from localconfig. Progress goes to stdout, the
// details to stderr; a failure is printed and the status is 1.
func runTool(ctx context.Context, env sys.System, stdout, stderr io.Writer, name string,
	fn func(*setup.Env) error,
) int {
	if geteuid() != 0 {
		_, _ = fmt.Fprintf(stderr, "Error: carbonio-bootstrap %s must run as root\n", name)

		return 1
	}

	e := &setup.Env{Ctx: ctx, Sys: env, Log: setup.NewLog(stdout, stderr), Paths: setup.DefaultPaths()}

	if err := fn(e); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}
