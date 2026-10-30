// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/testutil"
)

// Legacy reference: carbonio-core 4.6.7 src/lib/jylibs/state.py. Every input
// below makes the Jython transform()/xformConfig()/xformLocalConfig() raise
// (int() of None/""/garbage, a failed `[(a, b)] = re.findall(...)` unpack,
// None.split(), an out-of-range fields[n]); rewriteConfig then abandons the
// file. The Go port must return ErrTransform instead of rendering a guess.
func newRaiseTransformer() *Transformer {
	return NewTransformer(testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"num":      "40",
			"empty":    "",
			"bad":      "abc",
			"decimal":  "50.5",
			"nodigits": "xyz",
			"items":    "a b",
		},
		"LOCAL": {
			"empty": "",
			"blank": "   ",
			"plain": "host.example",
		},
	}))
}

func TestTransform_LegacyRaisePointsAbortLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"range missing key", "x %%range VAR:missing 0 100%%"},
		{"range empty value", "x %%range VAR:empty 0 100%%"},
		{"range non-numeric value", "x %%range VAR:bad 0 100%%"},
		{"range decimal value", "x %%range VAR:decimal 0 100%%"},
		{"range invalid lo", "x %%range VAR:num lo 100%%"},
		{"range invalid hi", "x %%range VAR:num 0 1.5%%"},
		{"range missing operands", "x %%range VAR:num%%"},
		{"range inline missing operands", "a %%range VAR:num 0%% b"},
		{"freq missing key", "x %%freq VAR:missing 3600%%"},
		{"freq empty value", "x %%freq VAR:empty 3600%%"},
		{"freq value without digits", "x %%freq VAR:nodigits 3600%%"},
		{"freq invalid total", "x %%freq VAR:num total%%"},
		{"freq missing total", "x %%freq VAR:num%%"},
		{"SPLIT missing key", "x=@@SPLIT missing@@;"},
		{"PERDITION_LDAP_SPLIT missing key", "x=@@PERDITION_LDAP_SPLIT missing@@;"},
		{"PERDITION_LDAP_SPLIT empty value", "x=@@PERDITION_LDAP_SPLIT empty@@;"},
		{"PERDITION_LDAP_SPLIT blank value", "x=@@PERDITION_LDAP_SPLIT blank@@;"},
		{"binary without operand", "%%binary%%"},
		{"truefalse without operand", "%%truefalse%%"},
		{"comment without operand", "%%comment foo%%rest"},
		{"uncomment without operand", "%%uncomment foo%%rest"},
		{"explode without operand", "%%explode basestring%%"},
		{"list without operand", "%%list%%"},
		{"list operand without colon", "%%list invalidconfig |%%"},
		{"list operand with two colons", "%%list VAR:a:b |%%"},
		{"list value without separator", "%%list VAR:items%%"},
		{"contains without search string", "%%contains VAR:items%%"},
		{"contains operand without colon", "%%contains nokey x^y^z%%"},
		{"exact without search string", "%%exact VAR:items%%"},
		{"exact operand with two colons", "%%exact VAR:a:b x^y^z%%"},
	}

	tr := newRaiseTransformer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tr.Transform(context.Background(), tt.input)
			require.ErrorIs(t, err, ErrTransform)
			assert.Empty(t, got, "no partially rendered line may leak out")
		})
	}
}

// Inputs that look close to the raising ones but do not raise in legacy: a None
// operand is only fatal where the legacy code feeds it to int()/re.sub()/split().
func TestTransform_LegacyNonRaisingNeighbours(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain @@key@@ missing renders empty", "x=@@missing@@;", "x=;\n"},
		{"SPLIT of empty value", "x=@@SPLIT empty@@;", "x=;\n"},
		{"range ok", "%%range VAR:num 0 100%%", "40.0\n"},
		{"list missing key", "%%list VAR:missing |%%", "\n"},
		{"list empty value needs no separator", "%%list VAR:empty%%", "\n"},
		{"exact missing key needs no search string", "%%exact VAR:missing%%", "\n"},
		{"explode missing key", "%%explode base VAR:missing%%", "\n"},
	}

	tr := newRaiseTransformer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tr.Transform(context.Background(), tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
