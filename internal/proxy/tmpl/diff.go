// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tmpl

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines around a change.
const diffContext = 3

// Diff returns the unified diff from old to formatted, which must be Format's
// output for old: Format changes lines in place and never adds or removes one,
// so the diff pairs the lines by position. It returns "" when they are equal.
func Diff(name, old, formatted string) string {
	if old == formatted {
		return ""
	}

	a, b := SplitLines(old), SplitLines(formatted)
	if len(a) != len(b) {
		return fmt.Sprintf("--- %s\n+++ %s\n@@ -1,%d +1,%d @@\n%s%s", name, name,
			len(a), len(b), prefixed("-", a), prefixed("+", b))
	}

	oldNoNL := old != "" && !strings.HasSuffix(old, "\n")
	// same reports whether line i is unchanged; without a final newline the
	// last line counts as changed.
	same := func(i int) bool { return a[i] == b[i] && (!oldNoNL || i != len(a)-1) }

	var out strings.Builder

	fmt.Fprintf(&out, "--- %s\n+++ %s\n", name, name)

	for i := 0; i < len(a); {
		if same(i) {
			i++

			continue
		}

		// A hunk: from diffContext lines before the first change to
		// diffContext lines after the last, merging changes that are close.
		start := max(i-diffContext, 0)
		end := i + 1

		for j := end; j < len(a) && j <= end+2*diffContext; j++ {
			if !same(j) {
				end = j + 1
			}
		}

		end = min(end+diffContext, len(a))

		writeHunk(&out, a, b, start, end, same, oldNoNL)

		i = end
	}

	return out.String()
}

// prefixed prefixes every line with p, for the whole-file fallback diff.
func prefixed(p string, lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(p + l + "\n")
	}

	return b.String()
}

// writeHunk writes the lines [start, end) as one hunk: unchanged lines as
// context, and each run of changed lines as the old ones, then the new ones.
func writeHunk(out *strings.Builder, a, b []string, start, end int, same func(int) bool, oldNoNL bool) {
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", start+1, end-start, start+1, end-start)

	for k := start; k < end; {
		if same(k) {
			out.WriteString(" " + a[k] + "\n")

			k++

			continue
		}

		run := k
		for run < end && !same(run) {
			run++
		}

		for _, l := range a[k:run] {
			out.WriteString("-" + l + "\n")
		}

		if oldNoNL && run == len(a) {
			out.WriteString("\\ No newline at end of file\n")
		}

		for _, l := range b[k:run] {
			out.WriteString("+" + l + "\n")
		}

		k = run
	}
}
