// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Key names as Bubble Tea spells them, and hint labels used more than once.
const (
	keyEnter = "enter"
	keyCtrlC = "ctrl+c"
	keyTab   = "tab"
	keyDown  = "down"
	keyLeft  = "left"
	keyRight = "right"
	keySpace = "space"
	keyEsc   = "esc"

	hintQuit = "quit"
	hintSave = "save"
)

// Terminal sizes below this show a message instead of the screen.
const (
	minWidth  = 80
	minHeight = 24
)

// fit returns s cut or padded to exactly w columns.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}

	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}

	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// box draws lines in a rounded border of w x h cells (borders included), the
// title in the top edge; the border is styled with st. Lines are cut to the
// inner width and the box is padded or cut to h.
func box(title string, lines []string, w, h int, st, titleSt *lipgloss.Style) string {
	inner := max(0, w-2)

	top := st.Render("╭" + strings.Repeat("─", inner) + "╮")

	if title != "" {
		dashes := max(0, w-lipgloss.Width(title)-5)
		top = st.Render("╭─ ") + titleSt.Render(title) + st.Render(" "+strings.Repeat("─", dashes)+"╮")
	}

	out := []string{top}

	for i := range max(0, h-2) {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}

		out = append(out, st.Render("│")+fit(line, inner)+st.Render("│"))
	}

	out = append(out, st.Render("╰"+strings.Repeat("─", inner)+"╯"))

	return strings.Join(out, "\n")
}

// wrapText breaks s into lines of at most w columns at spaces.
func wrapText(s string, w int) []string {
	var out []string

	for para := range strings.SplitSeq(s, "\n") {
		out = append(out, strings.Split(ansi.Wrap(para, max(1, w), ""), "\n")...)
	}

	return out
}

// pad indents every line by two columns.
func pad(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = " " + l
	}

	return out
}

// tooSmall is the screen shown below the minimum size.
func tooSmall(w, h int) string {
	return fmt.Sprintf("Terminal too small: carbonio-bootstrap needs at least %dx%d (now %dx%d).\n"+
		"Resize the window or use --plain.", minWidth, minHeight, w, h)
}

func joinWith(parts []string, sep string) string { return strings.Join(parts, sep) }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return fmt.Sprintf("%d %s", n, many)
}
