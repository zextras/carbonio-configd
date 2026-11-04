// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package systemd

import "testing"

type fixedMode bool

func (m fixedMode) IsSystemdMode() bool { return bool(m) }

func TestManaged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode Mode
		want bool
	}{
		{"no systemd host", nil, false},
		{"legacy host", fixedMode(false), false},
		{"systemd host", fixedMode(true), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Managed(tt.mode); got != tt.want {
				t.Errorf("Managed() = %v, want %v", got, tt.want)
			}
		})
	}
}
