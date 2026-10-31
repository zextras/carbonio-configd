// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReason_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    Reason
		want string
	}{
		{"first run", Reason{Kind: ReasonFirstRun, Section: "mta"}, "first run: section mta rendered for the first time"},
		{"forced", Reason{Kind: ReasonForced, Section: "mta"}, "section mta forced by REWRITE request"},
		{"requested", Reason{Kind: ReasonRequested, Section: "mta"}, "section mta requested by REWRITE command"},
		{"evaluated", Reason{Kind: ReasonEvaluated, Section: "mta"}, "section mta evaluated against the host state"},
		{
			"var changed", Reason{Kind: ReasonVarChanged, Section: "mta", Type: "VAR", Key: "k", Old: "a"},
			"section mta changed because VAR k changed 'a'-><unset>",
		},
		{"service enabled", Reason{Kind: ReasonServiceEnabled, Service: "proxy"}, "service proxy was enabled"},
		{"service disabled", Reason{Kind: ReasonServiceDisabled, Service: "proxy"}, "service proxy was disabled"},
		{"dependency", Reason{Kind: ReasonDependency, Service: "amavis"}, "dependency of amavis"},
		{"retry", Reason{Kind: ReasonRetry}, "retry: previous attempt did not complete"},
		{"drift", Reason{Kind: ReasonDrift, Detail: "file differs"}, "drift: file differs"},
		{"mapfile sync", Reason{Kind: ReasonMapfileSync}, "mapped file kept in sync with the configuration"},
		{"unknown kind falls back to the kind", Reason{Kind: "custom"}, "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.r.String())
		})
	}
}

func TestStrings_RendersEveryReasonInOrder(t *testing.T) {
	t.Parallel()

	got := Strings([]Reason{{Kind: ReasonRetry}, {Kind: ReasonDrift, Detail: "x"}})

	assert.Equal(t, []string{"retry: previous attempt did not complete", "drift: x"}, got)
	assert.Empty(t, Strings(nil))
}

func TestVarChanged_TruncatesLongValuesAndRedactsSecrets(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", maxReasonValue)

	r := varChanged("mta", "VAR", "zimbraCert", "", long)

	assert.True(t, strings.HasSuffix(r.New, "..."))
	assert.LessOrEqual(t, len(r.New), maxReasonValue+len("..."))
	assert.Equal(t, strings.ToValidUTF8(r.New, "\uFFFD"), r.New, "truncation never splits a rune")

	secret := varChanged("mta", "VAR", "ldap_password", "old-secret", "new-secret")
	assert.NotContains(t, secret.Old+secret.New, "secret")
}
