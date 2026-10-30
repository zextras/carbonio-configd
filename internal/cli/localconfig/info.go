// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf16"

	lcfg "github.com/zextras/carbonio-configd/internal/localconfig"
)

// docWrapWidth is the column limit of LocalConfig.fmt in the legacy tool.
const docWrapWidth = 80

// runInfo implements -i and --all: print the documentation of the listed keys
// (all known keys when none is listed). Only -q may accompany them.
func (o *localconfigOpts) runInfo(mainOption string, printUnsupported bool) error {
	if err := o.checkCompatible(mainOption, "q"); err != nil {
		return err
	}

	var doc strings.Builder

	printDoc(&doc, o.warnWriter(), o.keys, printUnsupported)

	_, err := io.WriteString(o.out(), doc.String())

	return err
}

// warnWriter returns where legacy Logging.warn output goes: stderr, or nowhere
// with -q.
func (o *localconfigOpts) warnWriter() io.Writer {
	if o.quiet {
		return io.Discard
	}

	return o.errOut()
}

// printDoc ports LocalConfig.printDoc. The "i > 0" blank-line rule counts
// skipped keys too, exactly like the legacy loop index.
func printDoc(w *strings.Builder, warn io.Writer, keys []string, printUnsupported bool) {
	if len(keys) == 0 {
		keys = lcfg.KnownKeys()
	}

	for i, key := range keys {
		if !lcfg.IsKnownKey(key) {
			_, _ = fmt.Fprintf(warn, "Warning: '%s' is not a known key\n", key)

			continue
		}

		doc, ok := lcfg.LookupDoc(key)
		if !ok || (!doc.Supported && !printUnsupported) {
			continue
		}

		if i > 0 {
			w.WriteByte('\n')
		}

		w.WriteString(key + ":\n")
		wrapDoc(w, doc.Doc, docWrapWidth)

		if !doc.Reloadable {
			w.WriteString("* Changes are in effect after server restart.\n")
		}
	}
}

// wrapDoc ports LocalConfig.fmt: greedy word wrap on single-whitespace
// separated tokens (empty tokens from repeated whitespace included), with
// lengths counted in UTF-16 code units like Java's String.length.
func wrapDoc(w *strings.Builder, text string, limit int) {
	cols := 0

	for _, tok := range splitJavaWhitespace(text) {
		n := utf16Len(tok)

		if n+cols > limit {
			cols = 0

			w.WriteByte('\n')
		}

		if n >= limit {
			cols = 0

			w.WriteString(tok + "\n")

			continue
		}

		if cols != 0 {
			w.WriteByte(' ')
		}

		w.WriteString(tok)

		cols += n + 1
	}

	if cols != 0 {
		w.WriteByte('\n')
	}
}

// splitJavaWhitespace is Java's str.split("\\s"): split at every single
// whitespace character ([ \t\n\x0B\f\r]) and drop trailing empty strings. A
// string without separator is returned whole.
func splitJavaWhitespace(s string) []string {
	var parts []string

	start := 0

	for i := range len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}

	if start == 0 && len(parts) == 0 {
		return []string{s}
	}

	parts = append(parts, s[start:])

	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}

	return parts
}

func utf16Len(s string) int {
	n := 0

	for _, r := range s {
		n += utf16.RuneLen(r)
	}

	return n
}
