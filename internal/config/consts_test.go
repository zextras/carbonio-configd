// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsFalseValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		val  string
		want bool
	}{
		{"", true},
		{"no", true},
		{"NO", true},
		{"No", true},
		{"nO", true},
		{"false", true},
		{"FALSE", true},
		{"False", true},
		{"0", true},
		{"00", true},
		// Legacy re.match(r"no|false|0+", val, re.I) is a case-insensitive prefix match.
		{"none", true},
		{"None", true},
		{"nobody", true},
		{"Normal", true},
		{"falsey", true},
		{"FALSEHOOD", true},
		{"0.5", true},
		{"00:30", true},
		{"0abc", true},
		{"yes", false},
		{"true", false},
		{"TRUE", false},
		{"1", false},
		{"10", false},
		{"enabled", false},
		{"anything", false},
		{"n", false},
		{"fals", false},
		{"fal", false},
		{" no", false},
		{"ano", false},
	}

	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, IsFalseValue(tt.val))
			assert.Equal(t, !tt.want, IsTrueValue(tt.val))
		})
	}
}

func TestIsTrueValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"no", false},
		{"false", false},
		{"0", false},
		{"yes", true},
		{"YES", true},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"enabled", true},
		{"anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, IsTrueValue(tt.val))
		})
	}
}
