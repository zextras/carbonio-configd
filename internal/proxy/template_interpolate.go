// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/proxy/tmpl"
)

// interpolateLine renders one template line: enabler prefixes, ${VAR}
// substitution and the commenting-out of empty directives (see tmpl.RenderLine).
func (tp *TemplateProcessor) interpolateLine(ctx context.Context, line string) string {
	return tmpl.RenderLine(line, &varResolver{ctx: ctx, g: tp.generator})
}

// varResolver adapts a Generator's variables to tmpl.Resolver.
type varResolver struct {
	ctx context.Context
	g   *Generator
}

// Enabler implements tmpl.Resolver: a variable of type enabler is on when its
// value is true, a non-empty string (keyword-style enablers such as "server")
// or non-zero.
func (r *varResolver) Enabler(name string) (on, ok bool) {
	v, exists := r.g.Variables[name]
	if !exists || v.ValueType != ValueTypeEnabler {
		return false, false
	}

	switch val := v.Value.(type) {
	case bool:
		on = val
	case string:
		on = val != ""
	case int, int64:
		on = val != 0
	default:
		logger.ErrorContext(r.ctx, "Enabler has unexpected type",
			"variable", name,
			"type", fmt.Sprintf("%T", v.Value),
			"value", v.Value)
	}

	return on, true
}

// Value implements tmpl.Resolver. A variable that cannot be expanded renders
// as the empty string.
func (r *varResolver) Value(name string) (string, bool) {
	text, err := r.g.ExpandVariable(r.ctx, name)

	return text, err == nil
}

// ExpandVariable expands a single variable by name
// This is a helper method on Generator that looks up the variable and returns its expanded value
func (g *Generator) ExpandVariable(ctx context.Context, name string) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	v, exists := g.Variables[name]
	if !exists {
		return "", fmt.Errorf("variable %s not found", name)
	}

	// Log for debugging port issues
	if strings.Contains(name, "port") {
		logger.DebugContext(ctx, "ExpandVariable",
			"name", name,
			"value", v.Value,
			"type", fmt.Sprintf("%T", v.Value),
			"value_type", v.ValueType)
	}

	if v.ConfigText != nil {
		return *v.ConfigText, nil
	}

	// Use CustomFormatter if available
	if v.CustomFormatter != nil {
		return v.CustomFormatter(v.Value)
	}

	// Format based on ValueType (Java's approach: different formatting for ENABLER vs BOOLEAN)
	if v.ValueType == ValueTypeEnabler {
		return formatEnabler(v.Value), nil
	}

	// Handle TIME type values - Java outputs integers as milliseconds with "ms" suffix
	if v.ValueType == ValueTypeTime {
		return formatTimeValue(v.Value), nil
	}

	// Handle TimeInSec type values - Convert milliseconds to plain seconds (Java's TimeInSecVarWrapper)
	if v.ValueType == ValueTypeTimeInSec {
		return formatTimeInSecValue(v.Value), nil
	}

	// Default formatting based on type
	return formatValue(v.Value), nil
}
