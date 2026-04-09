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

	"github.com/zextras/carbonio-configd/internal/intern"
)

// DefaultConfigPath is the standard location of localconfig.xml in Carbonio.
const DefaultConfigPath = "/opt/zextras/conf/localconfig.xml"

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
	// Read file
	// #nosec G304 - path is intentionally provided by caller for flexibility in testing and deployment
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read localconfig file: %w", err)
	}

	// Parse XML
	var config LocalConfig
	if err := xml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse localconfig XML: %w", err)
	}

	// Convert to map. Local-config keys are interned so every downstream map
	// keyed on them shares the same backing storage across the process.
	result := make(map[string]string, len(config.Keys))
	for _, key := range config.Keys {
		result[intern.Key(key.Name)] = strings.TrimSpace(key.Value)
	}

	return result, nil
}

// LoadResolvedConfig loads localconfig.xml, merges defaults, and resolves
// ${variable} references.
func LoadResolvedConfig() (map[string]string, error) {
	return LoadResolvedConfigFromFile(DefaultConfigPath)
}

// LoadResolvedConfigFromFile loads localconfig from a custom path, merges
// defaults, and resolves ${variable} references.
func LoadResolvedConfigFromFile(path string) (map[string]string, error) {
	config, err := LoadLocalConfigFromFile(path)
	if err != nil {
		return nil, err
	}

	MergeDefaults(config)
	Interpolate(config)

	return config, nil
}

// FormatAsKeyValue converts a map to key=value format matching zmlocalconfig -s output.
// Useful for compatibility with existing code that parses zmlocalconfig output.
func FormatAsKeyValue(config map[string]string) string {
	var builder strings.Builder
	for key, value := range config {
		builder.WriteString(key)
		builder.WriteString(" = ")
		builder.WriteString(value)
		builder.WriteString("\n")
	}

	return builder.String()
}
