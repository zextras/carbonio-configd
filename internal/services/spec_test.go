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
	case def.Java != nil && def.Java.Prepare != nil && def.Java.FallbackArgs == nil:
		return fmt.Errorf("%s: Java Prepare needs FallbackArgs", def.Name)
	case def.Mysqld != nil && (def.PidFile == "" || def.ProcessName == "" || def.Mysqld.DefaultsKey == ""):
		return fmt.Errorf("%s: Mysqld launch needs PidFile, ProcessName and DefaultsKey", def.Name)
	case def.Commands != nil && (def.BinaryPath == "" || def.Commands.Start == nil):
		return fmt.Errorf("%s: Commands launch needs BinaryPath and a Start verb", def.Name)
	case def.Collectors != nil && (len(def.Collectors.Always) == 0 || def.ProcessName == ""):
		return fmt.Errorf("%s: Collectors launch needs Always and ProcessName", def.Name)
	case def.Direct != nil && (def.BinaryPath == "" || def.ProcessName == "" || len(def.launchStrategies()) > 0):
		return fmt.Errorf("%s: Direct refines a BinaryPath launch and needs BinaryPath and ProcessName", def.Name)
	}

	for _, companion := range def.Companions {
		if companion.Identity == "" || (companion.Binary != "" && def.Direct == nil) {
			return fmt.Errorf("%s: a Companion needs an Identity, and a Binary needs Direct", def.Name)
		}
	}

	return validateSteps(def.Name, def.Setup, def.Commands, def.Direct)
}

// validateSteps checks that every Step declares exactly one action.
func validateSteps(name string, setup []Step, commands *CommandSpec, direct *DirectSpec) error {
	all := append([]Step(nil), setup...)

	if commands != nil {
		all = append(all, commands.Prepare...)
	}

	if direct != nil {
		all = append(all, direct.Prepare...)
	}

	for i := range all {
		step := &all[i]
		actions := 0

		for _, set := range []bool{len(step.Mkdir) > 0, step.RemoveStale != nil, step.Run != nil, step.Hook != nil} {
			if set {
				actions++
			}
		}

		if actions != 1 {
			return fmt.Errorf("%s: step %d declares %d actions, want exactly one", name, i, actions)
		}

		if step.Run != nil && step.Run.Path == "" {
			return fmt.Errorf("%s: step %d runs a command without a Path", name, i)
		}
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
			def:  ServiceDef{Name: "x", BinaryPath: "/b", Commands: &CommandSpec{Prepare: []Step{{Hook: noop}}}},
			want: "Commands launch needs",
		},
		{
			name: "collectors without identity",
			def:  ServiceDef{Name: "x", Collectors: &CollectorSpec{Always: []string{"c"}}},
			want: "Collectors launch needs",
		},
		{
			name: "direct without a binary",
			def:  ServiceDef{Name: "x", ProcessName: "p", Direct: &DirectSpec{}},
			want: "Direct refines",
		},
		{
			name: "direct next to another strategy",
			def: ServiceDef{
				Name: "x", BinaryPath: "/b", ProcessName: "p", Direct: &DirectSpec{},
				Java: &JavaSpec{Args: javaArgs, LogFile: "/l"},
			},
			want: "Direct refines",
		},
		{
			name: "companion binary without direct",
			def: ServiceDef{
				Name: "x", BinaryPath: "/b",
				Companions: []Companion{{Identity: "helper", Binary: "/helper"}},
			},
			want: "a Binary needs Direct",
		},
		{
			name: "step without an action",
			def:  ServiceDef{Name: "x", Setup: []Step{{UnlessExists: "/f"}}},
			want: "declares 0 actions",
		},
		{
			name: "step with two actions",
			def:  ServiceDef{Name: "x", Setup: []Step{{Mkdir: []string{"/d"}, Hook: noop}}},
			want: "declares 2 actions",
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

// TestDefaultRegistry_ProceduralExceptions pins every piece of procedural code
// the registry still wires in: the Hook steps, the PreStart/PostStart/PreStop
// hooks and the CustomStart/CustomStop launchers. Each is an exception
// documented, with its reason, in docs/service-registry.md; growing this list
// is a decision to take there, not a side effect of a new service.
func TestDefaultRegistry_ProceduralExceptions(t *testing.T) {
	t.Parallel()

	ctl := newRealController(t)

	got := map[string]int{}

	for name, def := range ctl.registry {
		steps := append([]Step(nil), def.Setup...)
		if def.Commands != nil {
			steps = append(steps, def.Commands.Prepare...)
		}

		if def.Direct != nil {
			steps = append(steps, def.Direct.Prepare...)
		}

		for i := range steps {
			if steps[i].Hook != nil {
				got[name+" Hook step"]++
			}
		}

		got[name+" PreStart"] = len(def.PreStart)
		got[name+" PostStart"] = len(def.PostStart)
		got[name+" PreStop"] = len(def.PreStop)

		if def.CustomStart != nil || def.CustomStop != nil {
			got[name+" CustomStart/CustomStop"] = 1
		}
	}

	want := map[string]int{
		serviceMTA + " Hook step":   2, // main.cf bootstrap, ldap-*.cf rendering
		svcMailbox + " PostStart":   1, // advanced status
		svcAppserverDB + " PreStop": 1, // InnoDB flush
	}

	for key, n := range got {
		if n != 0 {
			assert.Equal(t, want[key], n, key)
		}
	}

	for key, n := range want {
		assert.Equal(t, n, got[key], key)
	}
}
