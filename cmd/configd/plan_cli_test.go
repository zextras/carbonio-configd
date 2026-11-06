// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parsePlanCLI(t *testing.T, argv ...string) (*CLI, string) {
	t.Helper()

	cli, cmd, err := parseArgv(t, argv...)
	require.NoError(t, err)

	return cli, cmd
}

func TestCLI_Plan(t *testing.T) {
	cli, cmd := parsePlanCLI(t, "plan")

	assert.Equal(t, "plan", cmd)
	assert.Empty(t, cli.Plan.Sections)
	assert.False(t, cli.Plan.JSON)
}

func TestCLI_PlanSectionsAndJSON(t *testing.T) {
	cli, cmd := parsePlanCLI(t, "plan", "mta", "proxy", "--json")

	assert.Equal(t, "plan", cmd)
	assert.Equal(t, []string{"mta", "proxy"}, cli.Plan.Sections)
	assert.True(t, cli.Plan.JSON)
}

func TestCLI_PlanIsNotDefaultCommand(t *testing.T) {
	_, cmd := parsePlanCLI(t)

	assert.Equal(t, "daemon", cmd)
}
