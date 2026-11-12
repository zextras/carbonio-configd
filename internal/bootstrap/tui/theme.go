// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/zextras/carbonio-configd/internal/bootstrap/brand"
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
	fg := func(s brand.Shade) lipgloss.Style {
		if noColor {
			return lipgloss.NewStyle()
		}

		return lipgloss.NewStyle().Foreground(s.Color(isDark))
	}

	t := theme{
		muted:  fg(brand.Secondary),
		accent: fg(brand.Primary),
		ok:     fg(brand.Success),
		warn:   fg(brand.Warning),
		bad:    fg(brand.Error),
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
