// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/proxy/tmpl"
)

// defaultWorkDir is the working directory of a Carbonio installation.
const defaultWorkDir = "/opt/zextras"

// registryInfo is what the template tools learn from the declared variables.
type registryInfo struct {
	kinds map[string]tmpl.Kind
	// switches holds the line switches: the enablers and the text variables
	// that render as "#" by default (web.strict.servername and the
	// *.upstream.disable variables), so that they can prefix a line.
	switches map[string]bool
	// leadingSpace holds the variables whose value renders with a leading
	// space (web.ssl.protocols renders " TLSv1.2 TLSv1.3").
	leadingSpace map[string]bool
}

// registry is built once: the variables are declared in code (vars_*.go), so
// this needs neither LDAP nor a configuration. Switches and leading spaces
// are found by rendering each variable at its default value.
var registry = sync.OnceValue(func() registryInfo {
	g := newBareGenerator(defaultWorkDir)
	ctx := context.Background()

	info := registryInfo{
		kinds:        make(map[string]tmpl.Kind, len(g.Variables)),
		switches:     map[string]bool{},
		leadingSpace: map[string]bool{},
	}

	for name, v := range g.Variables {
		kind := kindOf(v.ValueType)
		info.kinds[name] = kind

		text, err := g.ExpandVariable(ctx, name)

		switch {
		case kind == tmpl.KindEnabler, err == nil && text == "#":
			info.switches[name] = true
		case err == nil && strings.HasPrefix(text, " "):
			info.leadingSpace[name] = true
		}
	}

	return info
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
	k, ok := registry().kinds[name]

	return k, ok
}

// IsLineSwitch reports whether name is a variable that switches a line on or
// off when it prefixes it: a declared enabler variable, or a text variable
// that renders as "#" by default. For the template formatter.
func IsLineSwitch(name string) bool {
	return registry().switches[name]
}

// HasLeadingSpace reports whether the value of the variable name renders with
// a leading space. For the template formatter.
func HasLeadingSpace(name string) bool {
	return registry().leadingSpace[name]
}

// FormatOptions are the options of tmpl.Format for the variables the
// generator declares.
func FormatOptions() tmpl.FormatOptions {
	return tmpl.FormatOptions{IsSwitch: IsLineSwitch, LeadingSpace: HasLeadingSpace}
}
