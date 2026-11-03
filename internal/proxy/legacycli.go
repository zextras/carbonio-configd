// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// valueTypeNames are the legacy ProxyConfValueType names printed by -d/-D.
var valueTypeNames = map[ValueType]string{
	ValueTypeInteger:   "INTEGER",
	ValueTypeLong:      "LONG",
	ValueTypeString:    "STRING",
	ValueTypeBoolean:   "BOOLEAN",
	ValueTypeEnabler:   "ENABLER",
	ValueTypeTime:      "TIME",
	ValueTypeTimeInSec: "TIME",
	ValueTypeCustom:    "CUSTOM",
}

// ApplyOverrides applies legacy "-c name=value" overrides: the value becomes
// the literal config text of an existing variable. Malformed entries and
// unknown names are logged and ignored, as ProxyConfGen.overrideDefaultVars
// does.
func (g *Generator) ApplyOverrides(ctx context.Context, overrides []string) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	for _, o := range overrides {
		logger.DebugContext(ctx, "Processing config override "+o)

		k, v, ok := strings.Cut(o, "=")
		if !ok || k == "" {
			logger.InfoContext(ctx, "Ignoring config override "+o+" because it is not of the form name=value")

			continue
		}

		variable, exists := g.Variables[k]
		if !exists {
			logger.InfoContext(ctx, "Ignoring non-existent config variable "+k)

			continue
		}

		logger.InfoContext(ctx, "Overriding config variable "+k+" with "+v)

		variable.ConfigText = &v
	}
}

// WriteVariables prints the variable map as legacy ProxyConfGen -d/-D do:
// one block per variable, sorted by keyword.
func (g *Generator) WriteVariables(ctx context.Context, w io.Writer) error {
	keys := make([]string, 0, len(g.Variables))
	for k := range g.Variables {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	const none = "(none)"

	for _, k := range keys {
		v := g.Variables[k]

		text, err := g.ExpandVariable(ctx, k)
		if err != nil {
			return fmt.Errorf("format %s: %w", k, err)
		}

		attr := none
		if v.Attribute != "" {
			attr = v.Attribute
		}

		_, err = fmt.Fprintf(w,
			"  NGINX Keyword:         %s\n"+
				"  Description:           %s\n"+
				"  Value Type:            %s\n"+
				"  Controlling Attribute: %s\n"+
				"  Default Value:         %s\n"+
				"  Current Value:         %s\n"+
				"  Config Text:           %s\n\n",
			k, v.Description, valueTypeNames[v.ValueType], attr,
			printable(v.DefaultValue, none), printable(v.Value, none), text)
		if err != nil {
			return fmt.Errorf("write variables: %w", err)
		}
	}

	return nil
}

func printable(v any, none string) string {
	if v == nil {
		return none
	}

	return fmt.Sprint(v)
}
