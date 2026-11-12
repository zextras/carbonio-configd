// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"strings"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// NoticeWidth is the width the licence notice is wrapped to in plain output
// (the old carbonio-bootstrap ran it through `fold -w 80 -s`).
const NoticeWidth = 80

// Notice returns the licence notice installed at paths.LicenseNotice, or ""
// when there is none.
func Notice(fsys sys.FS, paths Paths) string {
	data, err := fsys.ReadFile(paths.LicenseNotice)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

// WriteNotice prints the start of a plain run: the title, then the licence
// notice wrapped at NoticeWidth between two rules (nothing when there is no
// notice).
func WriteNotice(w io.Writer, notice string) {
	_, _ = fmt.Fprintln(w, "Starting Carbonio Bootstrap...")

	if notice == "" {
		return
	}

	rule := strings.Repeat("-", NoticeWidth)

	_, _ = fmt.Fprintf(w, "%s\n%s\n%s\n\n", rule, strings.Join(Wrap(notice, NoticeWidth), "\n"), rule)
}

// Wrap breaks text into lines of at most width columns, at spaces when it
// can (fold -s); a word longer than width is split. Existing line breaks stay.
func Wrap(text string, width int) []string {
	var out []string

	for para := range strings.SplitSeq(text, "\n") {
		out = append(out, wrapLine(strings.TrimRight(para, " \r"), width)...)
	}

	return out
}

func wrapLine(line string, width int) []string {
	r := []rune(line)

	var out []string

	for width > 0 && len(r) > width {
		cut := width

		for i := width; i > 0; i-- {
			if r[i] == ' ' {
				cut = i

				break
			}
		}

		out = append(out, strings.TrimRight(string(r[:cut]), " "))
		r = []rune(strings.TrimLeft(string(r[cut:]), " "))
	}

	return append(out, string(r))
}
