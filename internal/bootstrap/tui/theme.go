// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// theme holds the styles of both screens.
type theme struct {
	title, crumb, muted, accent, ok, warn, bad lipgloss.Style
	selected, selectedDim, locked, secret      lipgloss.Style
	border, borderFocus, key, label            lipgloss.Style
}

// newTheme returns the styles for a dark or light terminal. With noColor the
// styles keep only weight and reverse video, so the screen reads in a
// monochrome terminal (NO_COLOR).
func newTheme(isDark, noColor bool) theme {
	pick := func(light, dark string) color.Color {
		return lipgloss.LightDark(isDark)(lipgloss.Color(light), lipgloss.Color(dark))
	}

	fg := func(light, dark string) lipgloss.Style {
		if noColor {
			return lipgloss.NewStyle()
		}

		return lipgloss.NewStyle().Foreground(pick(light, dark))
	}

	t := theme{
		muted:  fg("#6b7280", "#8b93a1"),
		accent: fg("#0369a1", "#7dd3fc"),
		ok:     fg("#15803d", "#86efac"),
		warn:   fg("#b45309", "#fcd34d"),
		bad:    fg("#b91c1c", "#fca5a5"),
	}
	t.title = t.accent.Bold(true)
	t.crumb = t.muted
	t.locked = t.warn
	t.secret = t.muted
	t.key = t.accent.Bold(true)
	t.label = lipgloss.NewStyle()
	t.selected = lipgloss.NewStyle().Reverse(true).Bold(true)
	t.selectedDim = lipgloss.NewStyle().Bold(true).Underline(true)
	t.border = t.muted
	t.borderFocus = t.accent

	return t
}
