// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systemd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsBooted_MatchesHostStateAndCaches(t *testing.T) {
	st, err := os.Stat("/run/systemd/system")
	want := err == nil && st.IsDir()

	assert.Equal(t, want, IsBooted(), "must follow sd_booted(3) semantics")
	assert.Equal(t, IsBooted(), IsBooted(), "cached result must be stable")
}
