// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseServiceCLI(t *testing.T, argv ...string) (*CLI, string, error) {
	t.Helper()

	return parseArgv(t, append([]string{"service"}, argv...)...)
}

// The core wrappers exec `configd service "${1:-}" <name> "${@:2}"`: every verb
// must take the service and any trailing legacy argument without a parse
// error (C-08).
func TestServiceVerbs_AcceptTrailingLegacyArgs(t *testing.T) {
	verbs := []string{
		"start", "stop", "kill", "restart", "reload", "status", "refresh", "rotate",
		"start-systemd", "stop-systemd", "reload-systemd", "restart-systemd",
	}

	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			_, cmd, err := parseServiceCLI(t, verb, "mta", "norewrite", "-x", "--whatever", "foo")

			require.NoError(t, err)
			assert.Contains(t, cmd, "service "+verb)
		})
	}
}

func TestServiceStart_NoRewriteForms(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want bool
	}{
		{"flag", []string{"start", "proxy", "--no-rewrite"}, true},
		{"legacy positional", []string{"start", "proxy", "norewrite"}, true},
		{"unrelated extras", []string{"start", "proxy", "other", "args"}, false},
		{"plain", []string{"start", "proxy"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli, _, err := parseServiceCLI(t, tt.argv...)
			require.NoError(t, err)

			opts := cli.Service.Start.options("start")
			opts.NoRewrite = opts.NoRewrite || cli.Service.Start.NoRewrite

			assert.Equal(t, tt.want, opts.NoRewrite)
			assert.Equal(t, "proxy", opts.Name)
		})
	}
}

func TestServiceVerb_ParsedNameAndExtra(t *testing.T) {
	cli, _, err := parseServiceCLI(t, "stop", "mailbox", "foo", "-y")

	require.NoError(t, err)
	assert.Equal(t, "mailbox", cli.Service.Stop.Name)
	assert.Equal(t, []string{"foo", "-y"}, cli.Service.Stop.Extra)
}

// An empty or unknown verb ends in the usage line, not in a parse error.
func TestService_EmptyOrUnknownVerbFallsBackToUsage(t *testing.T) {
	for _, argv := range [][]string{{}, {""}, {"update", "mta"}, {"help"}} {
		_, cmd, err := parseServiceCLI(t, argv...)

		require.NoError(t, err, "argv %q", argv)
		assert.Contains(t, cmd, "service", "argv %q", argv)
	}
}

func TestService_ListStillParses(t *testing.T) {
	_, cmd, err := parseServiceCLI(t, "list")

	require.NoError(t, err)
	assert.Equal(t, "service list", cmd)
}
