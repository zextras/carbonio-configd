// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDependencyClosure(t *testing.T) {
	t.Parallel()

	deps := map[string][]string{
		"mta":       {"Amavis", "antivirus"},
		"antivirus": {"amavis", "clamd"},
		"clamd":     {"mta"},
		"proxy":     {"memcached"},
	}

	assert.Equal(t, []string{"amavis", "antivirus", "clamd"}, DependencyClosure(deps, []string{"mta"}),
		"transitive dependents, lower-cased, without the root even through a cycle")
	assert.Equal(t, []string{"memcached"}, DependencyClosure(deps, []string{"proxy"}))
	assert.Equal(t, []string{"amavis", "antivirus", "memcached", "mta"},
		DependencyClosure(deps, []string{"proxy", "clamd"}), "every root is left out of the result")
	assert.Empty(t, DependencyClosure(deps, []string{"memcached"}), "a service nothing depends on")
	assert.Empty(t, DependencyClosure(deps, nil))
}
