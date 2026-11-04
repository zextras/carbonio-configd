// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchStrategies lists the launch strategies def declares; a registry entry
// declares at most one (see validate).
func (def *ServiceDef) launchStrategies() []string {
	var set []string

	if def.CustomStart != nil {
		set = append(set, "CustomStart")
	}

	if def.Java != nil {
		set = append(set, "Java")
	}

	if def.Mysqld != nil {
		set = append(set, "Mysqld")
	}

	if def.Commands != nil {
		set = append(set, "Commands")
	}

	if def.Collectors != nil {
		set = append(set, "Collectors")
	}

	return set
}

// validate reports a registry entry that contradicts itself: more than one
// launch strategy, or a spec missing what its executor needs. The default
// registry is checked by a test, so a bad declaration fails the build rather
// than a start on a production host.
func (def *ServiceDef) validate() error {
	if set := def.launchStrategies(); len(set) > 1 {
		return fmt.Errorf("%s declares several launch strategies: %v", def.Name, set)
	}

	if err := def.validateSpecs(); err != nil {
		return err
	}

	if def.Ready != ReadyNone && len(def.launchStrategies()) == 0 && def.BinaryPath == "" {
		return fmt.Errorf("%s: Ready %q needs a launch strategy", def.Name, def.Ready)
	}

	return nil
}

// validateSpecs checks each declared spec for what its executor needs.
func (def *ServiceDef) validateSpecs() error {
	switch {
	case def.Java != nil && (def.Java.Args == nil || def.Java.LogFile == ""):
		return fmt.Errorf("%s: Java launch needs Args and LogFile", def.Name)
	case def.Mysqld != nil && (def.PidFile == "" || def.ProcessName == "" || def.Mysqld.DefaultsKey == ""):
		return fmt.Errorf("%s: Mysqld launch needs PidFile, ProcessName and DefaultsKey", def.Name)
	case def.Commands != nil && (def.BinaryPath == "" || def.Commands.Start == nil):
		return fmt.Errorf("%s: Commands launch needs BinaryPath and a Start verb", def.Name)
	case def.Collectors != nil && (len(def.Collectors.Always) == 0 || def.ProcessName == ""):
		return fmt.Errorf("%s: Collectors launch needs Always and ProcessName", def.Name)
	}

	return nil
}

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
