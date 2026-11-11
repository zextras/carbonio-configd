// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Command carbonio-postfix-conf is the only writer of the root-owned Postfix
// configuration under /opt/zextras/common/conf (CO-4621). It must run as root:
//
//	carbonio-postfix-conf set KEY=VALUE...   validate, then postconf -e
//	carbonio-postfix-conf unset KEY...       validate, then postconf -X
//	carbonio-postfix-conf apply              consume configd's staging directory
//
// Exit status: 0 success, 2 validation error (nothing applied), 1 other failure.
package main

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/zextras/carbonio-configd/internal/postfixconf"
)

const stagingUser = "zextras"

func main() {
	os.Exit(run())
}

func run() int {
	// Only apply reads a staging directory, so only apply needs the zextras
	// uid; set and unset must work without that user. Without it apply accepts
	// root-owned staging only.
	var uid uint32

	if len(os.Args) > 1 && os.Args[1] == "apply" {
		if u, err := user.Lookup(stagingUser); err == nil {
			n, perr := strconv.ParseUint(u.Uid, 10, 32)
			if perr != nil {
				fmt.Fprintf(os.Stderr, "carbonio-postfix-conf: bad uid %q of %s\n", u.Uid, stagingUser)

				return postfixconf.ExitFailure
			}

			uid = uint32(n)
		}
	}

	opts := postfixconf.DefaultOptions(uid)

	return postfixconf.RunCLI(context.Background(), os.Args[1:], os.Geteuid(), &opts, os.Stderr)
}
