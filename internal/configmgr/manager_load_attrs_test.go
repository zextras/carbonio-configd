// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attrs builds the LDAP attribute map the native client returns from
// "key: value" fixture lines; repeated keys join with newlines, as in
// ldap.entryToMap.
func attrs(text string) map[string]string {
	m := make(map[string]string)

	for line := range strings.SplitSeq(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		value = strings.TrimPrefix(value, " ")
		if prev, seen := m[key]; seen {
			value = prev + "\n" + value
		}

		m[key] = value
	}

	return m
}

func readFixtureMap(t *testing.T, name string) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "roundtrip", name))
	require.NoError(t, err)

	var m map[string]string

	require.NoError(t, json.Unmarshal(raw, &m))

	return m
}

// TestNormalizeLDAPAttrs_MatchesFormerTextRoundTrip pins the typed path to the
// ConfigMap the removed ldap.FormatAsZmprovOutput -> parseLDAPCommandOutput
// text round trip produced. Fixtures: "_in" is the attribute map of
// `zmprov -l gs`/`gacf` captured from a Carbonio 4.6.7 server (secrets
// replaced by REDACTED-* placeholders, plus synthetic edge cases for colon
// values, empty and padded values, blank and "key: value"-looking lines);
// "_exp" is what the old round trip returned for exactly that input.
func TestNormalizeLDAPAttrs_MatchesFormerTextRoundTrip(t *testing.T) {
	for _, name := range []string{"gs", "gacf"} {
		t.Run(name, func(t *testing.T) {
			in := readFixtureMap(t, name+"_in.json")
			want := readFixtureMap(t, name+"_exp.json")

			assert.Equal(t, want, normalizeLDAPAttrs(in))
		})
	}
}

func TestNormalizeLDAPAttrs_DoesNotAliasInput(t *testing.T) {
	in := map[string]string{"zimbraFoo": " a \n b "}
	got := normalizeLDAPAttrs(in)

	got["zimbraFoo"] = "changed"

	assert.Equal(t, " a \n b ", in["zimbraFoo"])
	assert.Equal(t, map[string]string{"zimbraFoo": "a\nb"}, normalizeLDAPAttrs(in))
}

func TestFetchAttrs_UnregisteredAndEmpty(t *testing.T) {
	cm := newTestConfigManager(t)

	_, err := cm.fetchServerAttrs(context.Background())
	assert.ErrorContains(t, err, "gs command not available")

	_, err = cm.fetchGlobalAttrs(context.Background())
	assert.ErrorContains(t, err, "gacf command not available")

	cm.CommandRegistry.ServerAttrs = func(context.Context, string) (map[string]string, error) {
		return map[string]string{}, nil
	}
	cm.CommandRegistry.GlobalAttrs = func(context.Context) (map[string]string, error) {
		return map[string]string(nil), nil
	}

	_, err = cm.fetchServerAttrs(context.Background())
	assert.ErrorContains(t, err, "no data returned from gs")

	_, err = cm.fetchGlobalAttrs(context.Background())
	assert.ErrorContains(t, err, "no data returned from gacf")
}
