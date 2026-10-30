// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jylibs/mtaconfig.py registers the lookup behind a POSTCONF (VAR|LOCAL|FILE|MAPLOCAL)
// or POSTCONFD (VAR|LOCAL|FILE) value via section.requiredvars(fields[3], fields[2]);
// re.match is a prefix match on the type field.
func TestParser_PostconfLookups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		line         string
		wantPostconf map[string]string
		wantPostcfd  map[string]string
		wantReq      map[string]string
	}{
		{"VAR", "POSTCONF k VAR zimbraMtaMaxUse", map[string]string{"k": "VAR:zimbraMtaMaxUse"}, nil, map[string]string{"zimbraMtaMaxUse": "VAR"}},
		{"LOCAL", "POSTCONF k LOCAL zimbra_server_hostname", map[string]string{"k": "LOCAL:zimbra_server_hostname"}, nil, map[string]string{"zimbra_server_hostname": "LOCAL"}},
		{"FILE keeps the path", "POSTCONF k FILE zmconfigd/a.cf", map[string]string{"k": "FILE zmconfigd/a.cf"}, nil, map[string]string{"zmconfigd/a.cf": "FILE"}},
		{"MAPLOCAL allowed for postconf", "POSTCONF k MAPLOCAL zimbraSSLDHParam", map[string]string{"k": "MAPLOCAL:zimbraSSLDHParam"}, nil, map[string]string{"zimbraSSLDHParam": "MAPLOCAL"}},
		{"only the key counts", "POSTCONF k VAR key trailing words", map[string]string{"k": "VAR:key"}, nil, map[string]string{"key": "VAR"}},
		{"prefix type", "POSTCONF k VARIABLE_X name", map[string]string{"k": "VARIABLE_X:name"}, nil, map[string]string{"name": "VARIABLE_X"}},
		{"prefix type FILE", "POSTCONF k FILENAME name", map[string]string{"k": "FILENAME:name"}, nil, map[string]string{"name": "FILENAME"}},
		{"literal", "POSTCONF k permit_mynetworks,permit_sasl_authenticated", map[string]string{"k": "permit_mynetworks,permit_sasl_authenticated"}, nil, map[string]string{}},
		{"literal number", "POSTCONF k 0", map[string]string{"k": "0"}, nil, map[string]string{}},
		{"no value", "POSTCONF k", map[string]string{"k": ""}, nil, map[string]string{}},
		{"POSTCONFD value is consumed and registered", "POSTCONFD k VAR zimbraGone", nil, map[string]string{"k": ""}, map[string]string{"zimbraGone": "VAR"}},
		{"POSTCONFD FILE", "POSTCONFD k FILE zmconfigd/x.cf", nil, map[string]string{"k": ""}, map[string]string{"zmconfigd/x.cf": "FILE"}},
		{"POSTCONFD MAPLOCAL is no lookup", "POSTCONFD k MAPLOCAL zimbraX", nil, map[string]string{"k": ""}, map[string]string{}},
		{"POSTCONFD plain", "POSTCONFD k", nil, map[string]string{"k": ""}, map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := parseConfig(t, "SECTION s\n\t"+tt.line+"\n")
			require.NoError(t, err)

			section := cfg.Sections["s"]
			assert.Equal(t, orEmpty(tt.wantPostconf), section.Postconf)
			assert.Equal(t, orEmpty(tt.wantPostcfd), section.Postconfd)
			assert.Equal(t, tt.wantReq, section.RequiredVars)
		})
	}
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}

// Legacy fields[3] raises IndexError when a lookup has no key, aborting the load.
func TestParser_PostconfLookupWithoutKeyIsAnError(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"POSTCONF k LOCALHOST",
		"POSTCONF k FILE",
		"POSTCONF k VAR",
		"POSTCONFD k LOCAL",
	} {
		t.Run(line, func(t *testing.T) {
			t.Parallel()

			_, err := parseConfig(t, "SECTION s\n\t"+line+"\n")
			require.Error(t, err)
		})
	}
}

// Inside an if block the lookups are collected on the conditional, MAPLOCAL is
// a plain literal word and nothing touches the section.
func TestParser_ConditionalPostconfLookups(t *testing.T) {
	t.Parallel()

	input := `SECTION s
	if VAR x
		POSTCONF a VAR zimbraA
		POSTCONF b MAPLOCAL zimbraB
		POSTCONFD c FILE zmconfigd/c.cf
		POSTCONF d VARIABLE_X nm
	fi
`

	cfg, err := parseConfig(t, input)
	require.NoError(t, err)

	section := cfg.Sections["s"]
	assert.Empty(t, section.RequiredVars)

	cond := section.Conditionals[0]
	assert.Equal(t, map[string]string{
		"a": "VAR:zimbraA",
		"b": "MAPLOCAL zimbraB",
		"d": "VARIABLE_X:nm",
	}, cond.Postconf)
	assert.Equal(t, map[string]string{"c": ""}, cond.Postconfd)
	assert.Equal(t, map[string]string{
		"zimbraA":        "VAR",
		"zmconfigd/c.cf": "FILE",
		"nm":             "VARIABLE_X",
	}, cond.RequiredVars)
}
