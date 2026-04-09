// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// interpolateLine replaces all ${VAR} references in a line
func (tp *TemplateProcessor) interpolateLine(ctx context.Context, line string) (string, error) {
	// Check for enabler variables at the start of the line
	// Pattern: optional whitespace + ${var} + rest of line
	// Examples: "    ${mail.imap.enabled} include ..."
	//           "    ${core.ipboth.enabled}listen ..."
	// Note: No space required after } - enabler can be directly followed by directive
	enablerPattern := tp.enablerPattern

	if matches := enablerPattern.FindStringSubmatch(line); matches != nil {
		if _, ok := tp.debugVars[matches[2]]; ok {
			logger.DebugContext(ctx, "Checking potential enabler line",
				"line", line)
			logger.DebugContext(ctx, "Pattern matches",
				"matches", true)
		}

		result, handled, err := tp.processEnablerLine(ctx, matches)
		if err != nil {
			return "", err
		}

		if handled {
			return result, nil
		}
	}

	// Normal variable substitution for non-enabler variables
	result := tp.varPattern.ReplaceAllStringFunc(line, func(match string) string {
		// Extract variable name from ${VAR}
		varName := tp.varPattern.FindStringSubmatch(match)[1]

		// Look up variable value
		value, err := tp.generator.ExpandVariable(ctx, varName)
		if err != nil {
			// For missing variables, return empty string (fail silently for now)
			// In production, might want to log this
			return ""
		}

		return value
	})

	// Check if the line ends up with a directive that has no arguments
	// Pattern: whitespace + word (letters/digits/underscores) + whitespace + semicolon
	// Example: "    imap_id         ;" or "    proxy_issue_pop3_xoip   ;"
	// Must have at least one space between directive and semicolon
	if tp.emptyDirectivePattern.MatchString(result) {
		// Comment out the line by prepending "# "
		trimmed := strings.TrimSpace(result)
		logger.DebugContext(ctx, "Commenting out empty directive",
			"directive", trimmed)
		result = "    # " + trimmed
	}

	return result, nil
}

// processEnablerLine handles enabler variable logic for a line that matched the
// enabler pattern. It returns the processed line, whether the enabler was handled
// (i.e. the variable was an actual enabler type), and any error.
func (tp *TemplateProcessor) processEnablerLine(
	ctx context.Context, matches []string,
) (result string, handled bool, err error) {
	indent := matches[1]
	varName := matches[2]
	restOfLine := matches[3]

	if logger.IsDebug(ctx) {
		logger.DebugContext(ctx, "Matched enabler pattern",
			"variable", varName,
			"rest_of_line", restOfLine)
	}

	// Check if this is an enabler variable
	v, exists := tp.generator.Variables[varName]
	if !exists || v.ValueType != ValueTypeEnabler {
		return "", false, nil
	}

	// Get the boolean value
	val := v.Value
	isEnabled := false

	logger.DebugContext(ctx, "Found enabler variable",
		"variable", varName,
		"value", val,
		"value_type", fmt.Sprintf("%T", val))

	// Handle different value types
	switch v := val.(type) {
	case bool:
		isEnabled = v
		logger.DebugContext(ctx, "Enabler is bool",
			"variable", varName,
			"enabled", isEnabled)
	case string:
		// For string enablers, any non-empty value means enabled
		// This handles both "TRUE" and keyword-style enablers (e.g., "server")
		isEnabled = v != ""
		logger.DebugContext(ctx, "Enabler is string",
			"variable", varName,
			"value", v,
			"enabled", isEnabled)
	case int, int64:
		isEnabled = v != 0
		logger.DebugContext(ctx, "Enabler is int",
			"variable", varName,
			"value", v,
			"enabled", isEnabled)
	default:
		logger.ErrorContext(ctx, "Enabler has unexpected type",
			"variable", varName,
			"type", fmt.Sprintf("%T", val),
			"value", val)
	}

	processedLine := indent + restOfLine

	// Recursively process the rest of the line (it may have other ${} variables)
	processedRest, err := tp.interpolateLine(ctx, processedLine)
	if err != nil {
		return "", false, err
	}

	if isEnabled {
		// Variable is true - remove the enabler variable, keep the rest of the line
		logger.DebugContext(ctx, "Enabler is TRUE, processing rest of line",
			"variable", varName,
			"processed_line", processedLine)

		return processedRest, true, nil
	}

	// Variable is false - comment out the line (without the enabler variable)
	logger.DebugContext(ctx, "Enabler is FALSE, commenting out line",
		"variable", varName)

	// Remove the indent from processed line and add it back with comment
	trimmedLine := strings.TrimLeft(processedRest, " \t")

	return indent + "#" + trimmedLine, true, nil
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
