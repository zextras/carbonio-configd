// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package brand

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func hex(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()

	return string([]byte{'#'}) + h(r>>8) + h(g>>8) + h(b>>8)
}

func h(v uint32) string {
	const digits = "0123456789abcdef"

	return string([]byte{digits[v>>4], digits[v&0xf]})
}

func TestPalette_IsTheCarbonioDesignSystem(t *testing.T) {
	assert.Equal(t, "#2b73d2", hex(Primary.Color(true)))
	assert.Equal(t, "#2b73d2", hex(Primary.Color(false)))
	assert.Equal(t, "#828282", hex(Secondary.Color(true)))
	assert.Equal(t, "#d74942", hex(Error.Color(true)))
	assert.Equal(t, "#2196d3", hex(Info.Color(true)))
}

func TestPalette_LightTerminalsGetReadableWarningAndSuccess(t *testing.T) {
	assert.Equal(t, "#ffc107", hex(Warning.Color(true)))
	assert.Equal(t, "#b38600", hex(Warning.Color(false)))
	assert.Equal(t, "#8bc34a", hex(Success.Color(true)))
	assert.Equal(t, "#5a8f2a", hex(Success.Color(false)))
}

func TestDarkBackground(t *testing.T) {
	assert.True(t, DarkBackground(nil), "unknown: dark")
	assert.True(t, DarkBackground([]string{"COLORFGBG=15;0"}))
	assert.False(t, DarkBackground([]string{"COLORFGBG=0;15"}))
	assert.False(t, DarkBackground([]string{"COLORFGBG=0;default;7"}))
	assert.True(t, DarkBackground([]string{"COLORFGBG=junk"}))
}
