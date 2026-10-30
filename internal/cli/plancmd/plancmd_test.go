// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package plancmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/plan"
	"github.com/zextras/carbonio-configd/internal/redact"
)

const secretValue = "s3cr3t-do-not-print"

func drift(section string) []plan.Reason {
	return []plan.Reason{{Kind: plan.ReasonDrift, Section: section}}
}

func changesPlan() *plan.Plan {
	return &plan.Plan{
		Rewrites: []plan.Rewrite{{
			Source: "conf/nginx.conf.in", Target: "conf/nginx.conf",
			Sections: []string{"proxy"}, Reasons: drift("proxy"),
		}},
		Postconf: []plan.Postconf{{
			Key: "zimbra_ldap_password", Value: secretValue,
			Sections: []string{"mta"}, Reasons: drift("mta"),
		}},
		Restarts: []plan.Restart{{
			Service: "proxy", Action: plan.ActionRestart, Reasons: drift("proxy"),
		}},
	}
}

func TestSecretKeyPrecondition(t *testing.T) {
	require.True(t, redact.IsSecretKey("zimbra_ldap_password"))
}

func TestExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pl         *plan.Plan
		err        error
		json       bool
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{name: "empty text", pl: &plan.Plan{}, wantExit: ExitNoChanges, wantStdout: "No changes."},
		{name: "empty json", pl: &plan.Plan{}, json: true, wantExit: ExitNoChanges},
		{name: "changes text", pl: changesPlan(), wantExit: ExitChanges, wantStdout: "nginx.conf"},
		{name: "changes json", pl: changesPlan(), json: true, wantExit: ExitChanges},
		{name: "error text", err: errors.New("boom"), wantExit: ExitError, wantStderr: "Error: boom"},
		{name: "error json", err: errors.New("boom"), json: true, wantExit: ExitError, wantStderr: "Error: boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			opts := &Options{
				Sections: []string{"mta"},
				JSON:     tt.json,
				Planner: func(_ context.Context, sections []string) (*plan.Plan, error) {
					assert.Equal(t, []string{"mta"}, sections)

					return tt.pl, tt.err
				},
			}

			assert.Equal(t, tt.wantExit, Execute(context.Background(), &stdout, &stderr, opts))
			assert.NotContains(t, stdout.String()+stderr.String(), secretValue)
			assert.Contains(t, stdout.String(), tt.wantStdout)
			assert.Contains(t, stderr.String(), tt.wantStderr)

			if tt.err != nil {
				assert.Empty(t, stdout.String(), "stdout must stay empty on error")

				return
			}

			assert.Empty(t, stderr.String())

			if tt.json {
				var decoded map[string]any
				require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))

				for _, key := range []string{
					"first_run", "sections", "heal", "mapfiles", "rewrites",
					"postconf", "postconfd", "ldap", "restarts",
				} {
					assert.Contains(t, decoded, key)
				}
			}
		})
	}
}

func TestExecute_RedactsSecrets(t *testing.T) {
	t.Parallel()

	for _, asJSON := range []bool{false, true} {
		var stdout, stderr bytes.Buffer

		opts := &Options{
			JSON:    asJSON,
			Planner: func(context.Context, []string) (*plan.Plan, error) { return changesPlan(), nil },
		}

		require.Equal(t, ExitChanges, Execute(context.Background(), &stdout, &stderr, opts))
		assert.NotContains(t, stdout.String(), secretValue)
		assert.True(t, strings.Contains(stdout.String(), "zimbra_ldap_password"), "key stays visible")
	}
}
