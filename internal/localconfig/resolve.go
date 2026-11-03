// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"maps"
	"regexp"
	"slices"
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

// Legacy is a view of the configured keys with the semantics of the Java
// LocalConfig/KnownKey pair: raw lookups prefer the configured value, then the
// compiled-in default; a key with neither (unknown, or a known key whose Java
// default is null) has no value.
type Legacy struct {
	configured map[string]string
}

// NewLegacy wraps the keys configured in localconfig.xml (not copied).
func NewLegacy(configured map[string]string) *Legacy {
	return &Legacy{configured: configured}
}

// Raw is LocalConfig.getRaw: the unexpanded value of key, if it has one.
func (l *Legacy) Raw(key string) (string, bool) {
	if v, ok := l.configured[key]; ok {
		return v, true
	}

	v, ok := Defaults[key]

	return v, ok
}

// Keys is LocalConfig.allKeys: every known key with a default plus every
// configured key, sorted.
func (l *Legacy) Keys() []string {
	set := make(map[string]struct{}, len(Defaults)+len(l.configured))
	for k := range Defaults {
		set[k] = struct{}{}
	}

	for k := range l.configured {
		set[k] = struct{}{}
	}

	return slices.Sorted(maps.Keys(set))
}

// ConfigError is the legacy ConfigException raised by an invalid reference.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string {
	return "com.zimbra.common.localconfig.ConfigException: " + e.Msg
}

// Expand is LocalConfig.expand: key's value with every ${name} replaced,
// recursively. A reference cycle or a reference to a key without a value is a
// *ConfigError. ok is false when key itself has no value.
func (l *Legacy) Expand(key string) (value string, ok bool, err error) {
	if _, ok := l.Raw(key); !ok {
		return "", false, nil
	}

	value, err = l.expandDeep(key, nil)

	return value, err == nil, err
}

// Validate is the legacy LocalConfig.expandAll run by the constructor: every
// known and configured key must expand, or the whole invocation fails.
func (l *Legacy) Validate() error {
	for _, key := range l.Keys() {
		if _, _, err := l.Expand(key); err != nil {
			return err
		}
	}

	return nil
}

func (l *Legacy) expandDeep(key string, seen []string) (string, error) {
	if slices.Contains(seen, key) {
		return "", &ConfigError{Msg: "recursive expansion of key '" + key + "': " + strings.Join(seen, " ")}
	}

	seen = append(seen, key)

	replacement, ok := l.Raw(key)
	if !ok {
		return "", &ConfigError{Msg: "null valued key '" + key + "' referenced"}
	}

	for {
		begin := strings.Index(replacement, "${")
		if begin < 0 {
			break
		}

		end := strings.IndexByte(replacement[begin:], '}')
		if end < 0 {
			break
		}

		nested := replacement[begin+2 : begin+end]

		expanded, err := l.expandDeep(nested, seen)
		if err != nil {
			return "", err
		}

		replacement = strings.ReplaceAll(replacement, "${"+nested+"}", expanded)
	}

	return replacement, nil
}
