// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCarry_IsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		carry Carry
		want  bool
	}{
		{"zero", Carry{}, true},
		{"rewrite", Carry{Rewrites: []Rewrite{{}}}, false},
		{"postconf", Carry{Postconf: []Postconf{{}}}, false},
		{"postconfd", Carry{Postconfd: []Postconfd{{}}}, false},
		{"ldap", Carry{Ldap: []Ldap{{}}}, false},
		{"proxygen", Carry{Proxygen: &Proxygen{}}, false},
		{"restart", Carry{Restarts: []Restart{{}}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.carry.IsEmpty())
		})
	}
}
