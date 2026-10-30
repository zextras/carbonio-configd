// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jylibs/mtaconfig.py registers the lookup behind a POSTCONF (VAR|LOCAL|FILE|MAPLOCAL)
// or POSTCONFD (VAR|LOCAL|FILE) value via section.requiredvars(fields[3], fields[2]).
func TestPostconfRequiredVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		value         string
		allowMapLocal bool
		wantName      string
		wantType      string
		wantOK        bool
	}{
		{"VAR", "VAR:zimbraMtaMaxUse", true, "zimbraMtaMaxUse", "VAR", true},
		{"LOCAL", "LOCAL:zimbra_server_hostname", true, "zimbra_server_hostname", "LOCAL", true},
		{"FILE keeps the path", "FILE zmconfigd/a.cf", true, "zmconfigd/a.cf", "FILE", true},
		{"MAPLOCAL allowed for postconf", "MAPLOCAL:zimbraSSLDHParam", true, "zimbraSSLDHParam", "MAPLOCAL", true},
		{"MAPLOCAL refused for postconfd", "MAPLOCAL:zimbraSSLDHParam", false, "", "", false},
		{"only the first token decides", "VAR:key trailing words", true, "key", "VAR", true},
		{"literal", "permit_mynetworks,permit_sasl_authenticated", true, "", "", false},
		{"literal number", "0", true, "", "", false},
		{"empty", "", true, "", "", false},
		{"FILE without path", "FILE ", true, "", "", false},
		{"unknown type", "MAPFILE:key", true, "", "", false},
		{"missing key", "VAR:", true, "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name, cfgType, ok := PostconfRequiredVar(tt.value, tt.allowMapLocal)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantType, cfgType)
		})
	}
}

func TestParser_PostconfRegistersRequiredVars(t *testing.T) {
	t.Parallel()

	input := `SECTION mta
	POSTCONF max_use VAR zimbraMtaMaxUse
	POSTCONF relay FILE zmconfigd/relay.cf
	POSTCONF literal 0
	POSTCONFD gone VAR zimbraGone
	POSTCONFD plain
`

	mtaConfig, err := NewParser(NewLexer(context.Background(), strings.NewReader(""))).
		ParseString(context.Background(), input)
	require.NoError(t, err)

	section := mtaConfig.Sections["mta"]
	assert.Equal(t, map[string]string{
		"zimbraMtaMaxUse":    "VAR",
		"zmconfigd/relay.cf": "FILE",
		"zimbraGone":         "VAR",
	}, section.RequiredVars)
	assert.Equal(t, "VAR:zimbraMtaMaxUse", section.Postconf["max_use"])
	assert.Equal(t, map[string]string{"gone": "", "plain": ""}, section.Postconfd)
}
