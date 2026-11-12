// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// welcomeView shows the licence notice: informational, not an accept gate.
func welcomeView(th *theme, notice string, w, h int) string {
	width := min(76, w-8)

	lines := []string{"", " " + th.title.Render("Carbonio Bootstrap"), ""}
	for _, l := range wrapText(notice, width-4) {
		lines = append(lines, " "+l)
	}

	lines = append(lines, "", " "+hintLine(th, [][2]string{{"⏎", "continue"}, {"q", hintQuit}}))

	boxed := box("", lines, width, len(lines)+2, &th.borderFocus, &th.title)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, strings.TrimRight(boxed, "\n"))
}
