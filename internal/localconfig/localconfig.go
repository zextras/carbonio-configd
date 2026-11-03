// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package localconfig provides direct access to Carbonio local configuration
// by parsing /opt/zextras/conf/localconfig.xml, eliminating the need for zmlocalconfig subprocess.
package localconfig

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/intern"
)

// DefaultConfigPath is the standard location of localconfig.xml in Carbonio.
const DefaultConfigPath = config.ZextrasBase + "/conf/localconfig.xml"

// LocalConfig represents the root of localconfig.xml.
type LocalConfig struct {
	XMLName xml.Name `xml:"localconfig"`
	Keys    []Key    `xml:"key"`
}

// Key represents a single configuration key-value pair.
type Key struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value"`
}

// LoadLocalConfig reads and parses localconfig.xml from the default location.
// Returns a map of key=value pairs matching zmlocalconfig -s output format.
func LoadLocalConfig() (map[string]string, error) {
	return LoadLocalConfigFromFile(DefaultConfigPath)
}

// LoadLocalConfigFromFile reads and parses localconfig.xml from a custom path.
// Returns a map of key=value pairs matching zmlocalconfig -s output format.
func LoadLocalConfigFromFile(path string) (map[string]string, error) {
	values, _, err := LoadLocalConfigFile(path)

	return values, err
}

// LoadLocalConfigFile is LoadLocalConfigFromFile that also returns the names of
// the keys that have no <value> element. Legacy treats those as configured
// keys with a null value: they never print, but -n lists (and warns about)
// them. An empty <value/> is the empty string, not null.
func LoadLocalConfigFile(path string) (values map[string]string, nullKeys []string, err error) {
	// #nosec G304 - path is intentionally provided by caller for flexibility in testing and deployment
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read localconfig file: %w", err)
	}

	var lc struct {
		XMLName xml.Name `xml:"localconfig"`
		Keys    []struct {
			Name  string  `xml:"name,attr"`
			Value *string `xml:"value"`
		} `xml:"key"`
	}

	if err := xml.Unmarshal(data, &lc); err != nil {
		return nil, nil, fmt.Errorf("failed to parse localconfig XML: %w", err)
	}

	// Local-config keys are interned so every downstream map keyed on them
	// shares the same backing storage across the process.
	values = make(map[string]string, len(lc.Keys))

	for _, key := range lc.Keys {
		if key.Value == nil {
			nullKeys = append(nullKeys, key.Name)

			continue
		}

		values[intern.Key(key.Name)] = strings.TrimSpace(*key.Value)
	}

	return values, nullKeys, nil
}

// LoadResolvedConfig loads localconfig.xml, merges defaults, and resolves
// ${variable} references.
func LoadResolvedConfig() (map[string]string, error) {
	return LoadResolvedConfigFromFile(DefaultConfigPath)
}

// LoadResolvedConfigFromFile loads localconfig from a custom path, merges
// defaults, and resolves ${variable} references.
func LoadResolvedConfigFromFile(path string) (map[string]string, error) {
	resolved, err := LoadLocalConfigFromFile(path)
	if err != nil {
		return nil, err
	}

	MergeDefaults(resolved)
	Interpolate(resolved)

	return resolved, nil
}

// FormatAsKeyValue converts a map to key=value format matching zmlocalconfig -s output.
// Useful for compatibility with existing code that parses zmlocalconfig output.
func FormatAsKeyValue(values map[string]string) string {
	var builder strings.Builder
	for _, key := range sortedKeys(values) {
		builder.WriteString(key)
		builder.WriteString(" = ")
		builder.WriteString(values[key])
		builder.WriteString("\n")
	}

	return builder.String()
}
