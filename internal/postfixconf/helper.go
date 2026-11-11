// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfixconf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Exit codes of the helper.
const (
	ExitOK         = 0
	ExitFailure    = 1
	ExitValidation = 2
)

const usage = "usage: carbonio-postfix-conf set KEY=VALUE... | unset KEY... | apply"

// RunCLI is the helper command line. euid is the caller's effective uid; the
// helper refuses anything but root. It returns the process exit code.
func RunCLI(ctx context.Context, args []string, euid int, o *Options, stderr io.Writer) int {
	if euid != 0 {
		fprintln(stderr, "carbonio-postfix-conf: must run as root")

		return ExitFailure
	}

	if len(args) == 0 {
		fprintln(stderr, usage)

		return ExitValidation
	}

	var err error

	switch args[0] {
	case "apply":
		if len(args) != 1 {
			fprintln(stderr, usage)

			return ExitValidation
		}

		err = o.Apply(ctx)
	case "set", "unset":
		err = o.runDirect(ctx, args[0], args[1:])
	default:
		fprintln(stderr, usage)

		return ExitValidation
	}

	if err == nil {
		return ExitOK
	}

	fprintln(stderr, "carbonio-postfix-conf: "+err.Error())

	if errors.Is(err, ErrValidation) {
		return ExitValidation
	}

	return ExitFailure
}

func (o *Options) runDirect(ctx context.Context, verb string, rest []string) error {
	if len(rest) == 0 {
		return validationf("%s needs at least one argument", verb)
	}

	change := &Change{}

	for _, a := range rest {
		if verb == "unset" {
			change.Unset = append(change.Unset, a)

			continue
		}

		key, value, ok := strings.Cut(a, "=")
		if !ok {
			return validationf("%q is not KEY=VALUE", a)
		}

		change.Set = append(change.Set, KV{Key: key, Value: value})
	}

	unlock, err := o.lock()
	if err != nil {
		return err
	}
	defer unlock()

	return o.Commit(ctx, change)
}

func fprintln(w io.Writer, s string) { _, _ = fmt.Fprintln(w, s) }
