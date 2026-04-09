// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// FormatPlain writes config in "key = value" format, sorted alphabetically.
func FormatPlain(w io.Writer, config map[string]string) {
	for _, k := range sortedKeys(config) {
		_, _ = fmt.Fprintf(w, "%s = %s\n", k, config[k])
	}
}

// FormatShell writes config in shell-eval format: key='value';
func FormatShell(w io.Writer, config map[string]string) {
	for _, k := range sortedKeys(config) {
		_, _ = fmt.Fprintf(w, "%s='%s';\n", k, ShellEscape(config[k]))
	}
}

// FormatExport writes config in shell export format: export key='value';
func FormatExport(w io.Writer, config map[string]string) {
	for _, k := range sortedKeys(config) {
		_, _ = fmt.Fprintf(w, "export %s='%s';\n", k, ShellEscape(config[k]))
	}
}

// FormatNokey writes values only (no keys), one per line.
// When orderedKeys is provided, output follows that order; otherwise alphabetical.
func FormatNokey(w io.Writer, config map[string]string, orderedKeys []string) {
	if len(orderedKeys) == 0 {
		orderedKeys = sortedKeys(config)
	}

	for _, k := range orderedKeys {
		if v, ok := config[k]; ok {
			_, _ = fmt.Fprintln(w, v)
		}
	}
}

// FormatXML writes config as a localconfig XML document.
func FormatXML(w io.Writer, config map[string]string) error {
	type xmlValue struct {
		XMLName xml.Name `xml:"key"`
		Name    string   `xml:"name,attr"`
		Value   string   `xml:"value"`
	}

	type xmlConfig struct {
		XMLName xml.Name   `xml:"localconfig"`
		Keys    []xmlValue `xml:"key"`
	}

	xc := xmlConfig{}
	for _, k := range sortedKeys(config) {
		xc.Keys = append(xc.Keys, xmlValue{Name: k, Value: config[k]})
	}

	_, _ = fmt.Fprint(w, xml.Header)

	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")

	if err := enc.Encode(xc); err != nil {
		return fmt.Errorf("failed to encode XML: %w", err)
	}

	_, _ = fmt.Fprintln(w)

	return nil
}

// ShellEscape escapes single quotes for safe use in shell single-quoted strings.
func ShellEscape(s string) string {
	result := make([]byte, 0, len(s))
	for i := range len(s) {
		if s[i] == '\'' {
			result = append(result, '\'', '\\', '\'', '\'')
		} else {
			result = append(result, s[i])
		}
	}

	return string(result)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// maskedValue is the placeholder used to hide sensitive configuration
// values when logging or exporting configuration.
const maskedValue = "**********"

// MaskPasswords replaces values of sensitive keys with a masked string.
// Sensitive keys contain "password", "secret", or "_pass" in their name.
func MaskPasswords(config map[string]string) map[string]string {
	masked := make(map[string]string, len(config))
	for k, v := range config {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "password") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "_pass") {
			masked[k] = maskedValue
		} else {
			masked[k] = v
		}
	}

	return masked
}
