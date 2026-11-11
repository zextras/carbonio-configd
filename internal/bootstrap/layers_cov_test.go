// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyExisting_RanksAboveDefaultsBelowOperatorLayers(t *testing.T) {
	r := NewResolved(readFiles(nil))

	require.NoError(t, r.ApplyExisting(map[string]string{"ldap.port": "636", "domain.name": "old.example.com"}))
	require.NoError(t, r.ApplySets([]string{"ldap.port=4389"}))
	require.NoError(t, r.ApplyDefaults(singleNode))

	assert.Equal(t, "old.example.com", r.Config.Domain.Name)
	assert.Equal(t, SourceExisting, r.Source("domain.name"))
	assert.Equal(t, 4389, r.Config.LDAP.Port, "the operator's flag wins over the existing install")
	assert.Equal(t, SourceFlag, r.Source("ldap.port"))
}

func TestApplyExisting_ReportsEveryBadKeyInOrder(t *testing.T) {
	r := NewResolved(readFiles(nil))

	err := r.ApplyExisting(map[string]string{"ldap.port": "notanumber", "nosuch.key": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "existing install: ")
	assert.Contains(t, err.Error(), "ldap.port")
	assert.Contains(t, err.Error(), `unknown key "nosuch.key"`)
	assert.Less(t, indexOf(err.Error(), "ldap.port"), indexOf(err.Error(), "nosuch.key"), "sorted by key")
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}

	return -1
}

func TestApplyYAML_StructureErrorsNameTheLine(t *testing.T) {
	for name, tc := range map[string]struct{ doc, want string }{
		"root is not a mapping":     {"- a\n- b\n", "line 1: document: expected a mapping"},
		"section is not a mapping":  {"ldap: 5\n", "line 1: ldap: expected a mapping"},
		"unknown scalar key":        {"nosuch: 1\n", `line 1: unknown key "nosuch"`},
		"unknown list key":          {"nosuch:\n  - a\n", `line 1: unknown key "nosuch"`},
		"nested list item":          {"roles:\n  - [a]\n", "roles: list items must be plain values"},
		"mapping for a plain value": {"ldap:\n  port:\n    a: 1\n", "ldap.port: expected a value"},
		"mapping for unknown key":   {"ldap:\n  nosuch:\n    a: 1\n", `unknown key "ldap.nosuch"`},
		"bad list value":            {"roles:\n  - nosuchrole\n", "roles"},
	} {
		t.Run(name, func(t *testing.T) {
			err := NewResolved(readFiles(nil)).ApplyYAML([]byte(tc.doc))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestApplyYAML_EmptyDocumentAndNullsAreIgnored(t *testing.T) {
	r := NewResolved(readFiles(nil))
	require.NoError(t, r.ApplyYAML(nil))
	require.NoError(t, r.ApplyYAML([]byte("ldap:\n  port:\n")))
	assert.Equal(t, SourceUnset, r.Source("ldap.port"))
}

func TestApplyYAML_ListsAreRecordedAsFileValues(t *testing.T) {
	r := NewResolved(readFiles(nil))
	require.NoError(t, r.ApplyYAML([]byte("roles:\n  - mta\n  - proxy\n")))
	assert.Equal(t, []string{"mta", "proxy"}, r.Config.Roles)
	assert.Equal(t, SourceFile, r.Source("roles"))
}

func TestApplySets_ErrorsAreReportedTogether(t *testing.T) {
	r := NewResolved(readFiles(nil))

	err := r.ApplySets([]string{"ldap.port", "nosuch=1", " ldap.port = 1389"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--set "ldap.port": expected key=value`)
	assert.Contains(t, err.Error(), "--set nosuch")
	assert.Equal(t, 1389, r.Config.LDAP.Port, "a valid assignment still applies")
}

func TestSetSecretFile_Errors(t *testing.T) {
	r := NewResolved(readFiles(nil))
	err := r.ApplySets([]string{"ldap.root_password_file=/missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the secret file")

	err = r.ApplySets([]string{"ldap.port_file=/x"})
	require.ErrorContains(t, err, `unknown key "ldap.port_file"`)
}
