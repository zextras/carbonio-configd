// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package brand is the colour palette of the bootstrap, shared by the
// full-screen interface and the coloured plain output. The "regular" shades
// are those of the Carbonio Design System (src/theme/theme.ts); where a
// shade is hard to read on a light terminal, Light holds a darker one.
package brand

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// Shade is a colour for light and for dark terminals.
type Shade struct {
	Light, Dark string
}

// Color picks the shade for the terminal background.
func (s Shade) Color(isDark bool) color.Color {
	return lipgloss.LightDark(isDark)(lipgloss.Color(s.Light), lipgloss.Color(s.Dark))
}

// The palette.
var (
	Primary   = Shade{Light: "#2b73d2", Dark: "#2b73d2"}
	Secondary = Shade{Light: "#828282", Dark: "#828282"}
	Success   = Shade{Light: "#5a8f2a", Dark: "#8bc34a"}
	Warning   = Shade{Light: "#b38600", Dark: "#ffc107"}
	Error     = Shade{Light: "#d74942", Dark: "#d74942"}
	Info      = Shade{Light: "#2196d3", Dark: "#2196d3"}
)

// Wordmark follows the title of the bootstrap screens.
const Wordmark = "by Zextras"

// DarkBackground guesses whether the terminal is dark from COLORFGBG
// ("fg;bg": a background of 7 or 15 is light); dark when unknown.
func DarkBackground(environ []string) bool {
	for _, kv := range environ {
		v, ok := strings.CutPrefix(kv, "COLORFGBG=")
		if !ok {
			continue
		}

		if _, bg, ok := strings.CutLast(v, ";"); ok {
			if n, err := strconv.Atoi(bg); err == nil {
				return n != 7 && n != 15
			}
		}
	}

	return true
}
