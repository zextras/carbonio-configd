// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"fmt"
	"strings"
)

// applyQuoteChar processes a quote rune for splitCommandArgs.
// Returns the updated inQuote/quoteChar state; appends an empty arg when an
// empty quoted segment is closed.
func applyQuoteChar(
	r, quoteChar rune,
	inQuote bool,
	current *strings.Builder,
	args *[]string,
) (newInQuote bool, newQuoteChar rune) {
	switch {
	case !inQuote:
		return true, r
	case r == quoteChar:
		if current.Len() == 0 {
			*args = append(*args, "")
		}

		return false, 0
	default:
		current.WriteRune(r)

		return inQuote, quoteChar
	}
}

// splitCommandArgs splits a command string into argv preserving quoted and
// escaped segments.
func splitCommandArgs(cmdStr string) ([]string, error) {
	var (
		args      []string
		current   strings.Builder
		inQuote   bool
		quoteChar rune
		escaped   bool
	)

	for _, r := range cmdStr {
		switch {
		case escaped:
			current.WriteRune(r)

			escaped = false
		case r == '\\':
			escaped = true
		case r == '"' || r == '\'':
			inQuote, quoteChar = applyQuoteChar(r, quoteChar, inQuote, &current, &args)
		case !inQuote && (r == ' ' || r == '\t'):
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if inQuote {
		return nil, fmt.Errorf("unterminated quote in command: %s", cmdStr)
	}

	if escaped {
		return nil, fmt.Errorf("trailing escape character in command: %s", cmdStr)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args, nil
}
