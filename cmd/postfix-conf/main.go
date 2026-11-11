// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command postfix-conf is the only writer of the root-owned Postfix
// configuration under /opt/zextras/common/conf (CO-4621). It must run as root:
//
//	postfix-conf set KEY=VALUE...   validate, then postconf -e
//	postfix-conf unset KEY...       validate, then postconf -X
//	postfix-conf apply              consume configd's staging directory
//
// Exit status: 0 success, 2 validation error (nothing applied), 1 other failure.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"

	"github.com/zextras/carbonio-configd/internal/postfixconf"
)

const stagingUser = "zextras"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Geteuid(), os.Stderr, user.Lookup, postfixconf.DefaultOptions))
}

// run is main with its inputs injected: the arguments, the effective uid, the
// error stream, the user lookup and the Options builder.
func run(
	ctx context.Context,
	args []string,
	euid int,
	stderr io.Writer,
	lookup func(name string) (*user.User, error),
	newOptions func(stagingUID uint32) postfixconf.Options,
) int {
	// Only apply reads a staging directory, so only apply needs the zextras
	// uid; set and unset must work without that user. Without it apply accepts
	// root-owned staging only.
	var uid uint32

	if len(args) > 0 && args[0] == "apply" {
		if u, err := lookup(stagingUser); err == nil {
			n, perr := strconv.ParseUint(u.Uid, 10, 32)
			if perr != nil {
				_, _ = fmt.Fprintf(stderr, "postfix-conf: bad uid %q of %s\n", u.Uid, stagingUser)

				return postfixconf.ExitFailure
			}

			uid = uint32(n)
		}
	}

	opts := newOptions(uid)

	return postfixconf.RunCLI(ctx, args, euid, &opts, stderr)
}
