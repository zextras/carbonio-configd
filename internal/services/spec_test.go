// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultRegistry_EntriesAreValid keeps the registry honest: every entry
// is keyed by its own name and declares a consistent launch strategy. A bad
// declaration must fail here, not on a production start.
func TestDefaultRegistry_EntriesAreValid(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	for name, def := range ctl.registry {
		assert.Equal(t, name, def.Name)
		assert.NoError(t, def.validate(), name)
	}
}

// TestDefaultRegistry_DeclaresTheLaunchStrategies pins the strategy each
// service that needs more than a binary path declares, so moving one to
// another executor is a visible decision.
func TestDefaultRegistry_DeclaresTheLaunchStrategies(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	want := map[string]string{
		svcMailbox:     "Java",
		svcMilter:      "Java",
		svcAppserverDB: "Mysqld",
		svcAntispamDB:  "Mysqld",
		serviceMTA:     "Commands",
		svcStats:       "Collectors",
		svcAmavis:      "CustomStart",
	}

	for name, def := range ctl.registry {
		got := def.launchStrategies()

		if strategy, ok := want[name]; ok {
			assert.Equal(t, []string{strategy}, got, name)
		} else {
			assert.Empty(t, got, "%s launches from BinaryPath or its members", name)
		}
	}
}

func TestServiceDefValidate_RejectsContradictions(t *testing.T) {
	t.Parallel()

	noop := func(context.Context) error { return nil }
	hook := func(context.Context, *ServiceDef) error { return nil }
	javaArgs := func(map[string]string) []string { return nil }

	tests := []struct {
		name string
		def  ServiceDef
		want string
	}{
		{
			name: "two strategies",
			def: ServiceDef{
				Name:        "x",
				CustomStart: hook,
				Java:        &JavaSpec{Args: javaArgs, LogFile: "/l"},
			},
			want: "several launch strategies",
		},
		{
			name: "java without args",
			def:  ServiceDef{Name: "x", Java: &JavaSpec{LogFile: "/l"}},
			want: "Java launch needs",
		},
		{
			name: "mysqld without pidfile",
			def:  ServiceDef{Name: "x", ProcessName: "p", Mysqld: &MysqldSpec{DefaultsKey: "k"}},
			want: "Mysqld launch needs",
		},
		{
			name: "commands without a start verb",
			def:  ServiceDef{Name: "x", BinaryPath: "/b", Commands: &CommandSpec{Prepare: []func(context.Context) error{noop}}},
			want: "Commands launch needs",
		},
		{
			name: "collectors without identity",
			def:  ServiceDef{Name: "x", Collectors: &CollectorSpec{Always: []string{"c"}}},
			want: "Collectors launch needs",
		},
		{
			name: "readiness without a launcher",
			def:  ServiceDef{Name: "x", Ready: ReadyLDAP},
			want: "needs a launch strategy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.def.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
