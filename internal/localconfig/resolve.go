// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"regexp"
	"strings"
)

// varPattern matches ${variable_name} references in config values.
var varPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// maxInterpolationDepth prevents infinite loops from circular references.
const maxInterpolationDepth = 10

// Interpolate resolves all ${key} references in the config map values.
// It performs multiple passes (up to maxInterpolationDepth) to handle
// transitive references like ${zimbra_home} inside ${zimbra_log_directory}.
// Returns the number of substitutions made.
func Interpolate(config map[string]string) int {
	totalSubs := 0

	for range maxInterpolationDepth {
		subs := interpolatePass(config)
		totalSubs += subs

		if subs == 0 {
			break
		}
	}

	return totalSubs
}

// interpolatePass performs a single substitution pass over the config map.
// It returns the number of substitutions made in this pass.
func interpolatePass(config map[string]string) int {
	subs := 0

	for key, value := range config {
		if !strings.Contains(value, "${") {
			continue
		}

		config[key] = varPattern.ReplaceAllStringFunc(value, func(match string) string {
			return resolveVarRef(match, config, &subs)
		})
	}

	return subs
}

// resolveVarRef resolves a single ${name} reference against the config map.
// It increments subs when a substitution is made, and returns the original
// match unchanged when the variable is not found or would self-reference.
func resolveVarRef(match string, config map[string]string, subs *int) string {
	varName := match[2 : len(match)-1]

	replacement, ok := config[varName]
	if ok && replacement != match {
		*subs++
		return replacement
	}

	return match
}

// MergeDefaults merges defaults into the config map. XML values (already in
// the map) take precedence — defaults are only applied for missing keys.
func MergeDefaults(config map[string]string) {
	for key, defaultValue := range Defaults {
		if _, exists := config[key]; !exists {
			config[key] = defaultValue
		}
	}
}

// FormatAsShell converts a config map to shell-eval format matching
// LocalConfigCLI -m shell output: key='value';
func FormatAsShell(config map[string]string) string {
	var builder strings.Builder
	for key, value := range config {
		builder.WriteString(key)
		builder.WriteString("='")
		// Escape single quotes within the value for shell safety
		builder.WriteString(strings.ReplaceAll(value, "'", "'\\''"))
		builder.WriteString("';\n")
	}

	return builder.String()
}
