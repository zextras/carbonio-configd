// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeAttrs_TrimsEveryValueLineAndName(t *testing.T) {
	t.Parallel()

	got := NormalizeAttrs(map[string]string{
		" zimbraMailMode ":     "  https ",
		"zimbraServiceEnabled": " mailbox \n\tmta  \nproxy",
		"empty":                "",
	})

	assert.Equal(t, map[string]string{
		"zimbraMailMode":       "https",
		"zimbraServiceEnabled": "mailbox\nmta\nproxy",
		"empty":                "",
	}, got)
}

func TestNormalizeAttrs_ReturnsAFreshMap(t *testing.T) {
	t.Parallel()

	in := map[string]string{"a": " 1 "}
	got := NormalizeAttrs(in)
	got["a"] = "changed"

	assert.Equal(t, " 1 ", in["a"], "the input is left alone")
}
