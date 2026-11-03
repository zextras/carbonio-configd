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

// orderedKeys returns the keys to print: order verbatim (an explicit key list
// keeps its order and repeats, keys without a value are skipped, like legacy
// ConfigWriter.add on a null value), or every key sorted when order is empty.
func orderedKeys(config map[string]string, order []string) []string {
	if len(order) == 0 {
		return sortedKeys(config)
	}

	keys := make([]string, 0, len(order))

	for _, k := range order {
		if _, ok := config[k]; ok {
			keys = append(keys, k)
		}
	}

	return keys
}

// FormatPlain writes config in "key = value" format, in the order of order
// (alphabetical when empty).
func FormatPlain(w io.Writer, config map[string]string, order []string) {
	for _, k := range orderedKeys(config, order) {
		_, _ = fmt.Fprintf(w, "%s = %s\n", k, config[k])
	}
}

// FormatShell writes config in shell-eval format: key='value';
func FormatShell(w io.Writer, config map[string]string, order []string) {
	for _, k := range orderedKeys(config, order) {
		_, _ = fmt.Fprintf(w, "%s='%s';\n", k, ShellEscape(config[k]))
	}
}

// FormatExport writes config in shell export format: export key='value';
func FormatExport(w io.Writer, config map[string]string, order []string) {
	for _, k := range orderedKeys(config, order) {
		_, _ = fmt.Fprintf(w, "export %s='%s';\n", k, ShellEscape(config[k]))
	}
}

// FormatNokey writes values only (no keys), one per line.
func FormatNokey(w io.Writer, config map[string]string, order []string) {
	for _, k := range orderedKeys(config, order) {
		_, _ = fmt.Fprintln(w, config[k])
	}
}

// FormatXML writes config as a localconfig XML document.
func FormatXML(w io.Writer, config map[string]string, order []string) error {
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
	for _, k := range orderedKeys(config, order) {
		xc.Keys = append(xc.Keys, xmlValue{Name: k, Value: normalizeSpace(config[k])})
	}

	// dom4j prints a blank line after the declaration.
	_, _ = fmt.Fprint(w, xml.Header, "\n")

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

// maskedValue is what the legacy ConfigWriter prints instead of a password.
const maskedValue = "*"

// MaskPasswords replaces the value of every key whose name contains the
// case-sensitive substring "password" with "*": exactly the legacy
// ConfigWriter.add rule (secret-looking keys such as api_secret or ldap_pass
// are not masked).
func MaskPasswords(config map[string]string) map[string]string {
	masked := make(map[string]string, len(config))
	for k, v := range config {
		if strings.Contains(k, "password") {
			masked[k] = maskedValue
		} else {
			masked[k] = v
		}
	}

	return masked
}

// normalizeSpace collapses every whitespace run to one space and trims the
// ends: what the dom4j pretty printer does to element text.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
