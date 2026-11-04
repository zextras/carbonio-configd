// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"sync"

	"github.com/zextras/carbonio-configd/internal/proxy/tmpl"
)

// defaultWorkDir is the working directory of a Carbonio installation.
const defaultWorkDir = "/opt/zextras"

// variableKinds maps every variable the generator declares to its kind, built
// once: the variables are declared in code (vars_*.go), so this needs neither
// LDAP nor a configuration.
var variableKinds = sync.OnceValue(func() map[string]tmpl.Kind {
	g := newBareGenerator(defaultWorkDir)

	kinds := make(map[string]tmpl.Kind, len(g.Variables))
	for name, v := range g.Variables {
		kinds[name] = kindOf(v.ValueType)
	}

	return kinds
})

func kindOf(t ValueType) tmpl.Kind {
	switch t {
	case ValueTypeEnabler:
		return tmpl.KindEnabler
	case ValueTypeInteger, ValueTypeLong, ValueTypeBoolean, ValueTypeTime, ValueTypeTimeInSec:
		return tmpl.KindScalar
	}

	return tmpl.KindText
}

// newBareGenerator is a Generator with every variable registered at its
// default value and nothing else: no LDAP, no configuration, no host access.
func newBareGenerator(workDir string) *Generator {
	g := &Generator{}
	g.setLayout(workDir)
	g.applySettings(nil)
	g.RegisterVariables(context.Background())

	return g
}

// TemplateRegistry is the tmpl.Registry of the variables the generator
// declares, for the template linter.
type TemplateRegistry struct{}

// Kind implements tmpl.Registry.
func (TemplateRegistry) Kind(name string) (tmpl.Kind, bool) {
	k, ok := variableKinds()[name]

	return k, ok
}

// IsEnabler reports whether name is a declared enabler variable, for the
// template formatter.
func IsEnabler(name string) bool {
	return variableKinds()[name] == tmpl.KindEnabler
}
