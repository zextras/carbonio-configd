// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plan

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fullActionPlan() Plan {
	drift := []Reason{{Kind: ReasonDrift, Detail: "d"}}

	return Plan{
		Mapfiles:  []Mapfile{{Key: "mf", Reasons: drift}},
		Heal:      []Mapfile{{Key: "healed", Reasons: []Reason{{Kind: ReasonMapfileSync, Section: "s"}}}},
		Rewrites:  []Rewrite{{Source: "a.in", Target: "a", Reasons: drift}},
		Postconf:  []Postconf{{Key: "pc", Value: "v", Reasons: drift}},
		Postconfd: []Postconfd{{Key: "pd", Value: "ldap_password_secret", Reasons: drift}},
		Ldap:      []Ldap{{Key: "zimbraLdapPassword", Value: "hunter2", Reasons: drift}},
		Proxygen:  &Proxygen{Reasons: drift},
		Restarts:  []Restart{{Service: "mta", Action: ActionStop, Reasons: drift}},
	}
}

func TestPlan_WriteTextCoversEveryActionKind(t *testing.T) {
	t.Parallel()

	p := fullActionPlan()

	var buf bytes.Buffer

	require.NoError(t, p.WriteText(&buf))

	out := buf.String()
	for _, want := range []string{
		"mapfile      mf\n",
		"mapfile-sync healed\n",
		"rewrite      a.in -> a\n",
		"postconf -e  pc=v\n",
		"postconf -X  pd\n",
		"ldap         zimbraLdapPassword=[REDACTED]\n",
		"proxygen     regenerate proxy configuration\n",
		"stop         mta\n    because: drift: d\n",
		"because: mapped file kept in sync with the configuration\n",
	} {
		assert.Contains(t, out, want)
	}

	assert.NotContains(t, out, "hunter2")
}

func TestPlan_WriteTextHealOnlyIsNotEmpty(t *testing.T) {
	t.Parallel()

	p := Plan{Heal: []Mapfile{{Key: "healed"}}}

	var buf bytes.Buffer

	require.NoError(t, p.WriteText(&buf))
	assert.Equal(t, "mapfile-sync healed\n", buf.String())
}

type failWriter struct{ calls int }

var errWrite = errors.New("pipe closed")

func (f *failWriter) Write([]byte) (int, error) {
	f.calls++

	return 0, errWrite
}

func TestPlan_WriteTextStopsAtTheFirstWriteError(t *testing.T) {
	t.Parallel()

	p := fullActionPlan()
	w := &failWriter{}

	require.ErrorIs(t, p.WriteText(w), errWrite)
	assert.Equal(t, 1, w.calls, "no write is attempted after the first failure")
}

func TestPlan_JSONRedactsSecretsAndEndsWithNewline(t *testing.T) {
	t.Parallel()

	p := fullActionPlan()

	data, err := p.JSON()
	require.NoError(t, err)

	assert.True(t, strings.HasSuffix(string(data), "}\n"))
	assert.NotContains(t, string(data), "hunter2")
	assert.Contains(t, string(data), "[REDACTED]")
	assert.Equal(t, "hunter2", p.Ldap[0].Value, "the plan itself is not modified")
}
