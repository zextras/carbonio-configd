// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package template provides interfaces for processing configuration file templates.
// It handles variable expansion (%%VAR:key%%, %%LOCAL:key%%, etc.) and conditional
// block processing (if/fi statements) in template files.
package template

import "github.com/zextras/carbonio-configd/internal/config"

// Processor interface defines methods for template processing.
type Processor interface {
	// ExpandVariables performs first pass expansion of %%VAR:key%%, %%LOCAL:key%%, %%SERVICE:key%%
	ExpandVariables(template string, configs ConfigSet) (string, error)

	// ProcessConditionals performs second pass processing of if/fi conditional blocks
	ProcessConditionals(template string, configs ConfigSet) (string, error)

	// ProcessTemplate performs complete template processing (both passes)
	ProcessTemplate(template string, configs ConfigSet) (string, error)

	// ProcessFile reads a template file and processes it
	ProcessFile(templatePath string, configs ConfigSet) (string, error)
}

// ConfigSet holds all configuration sources needed for template expansion.
type ConfigSet struct {
	Local   *config.LocalConfig
	Global  *config.GlobalConfig
	Misc    *config.MiscConfig
	Server  *config.ServerConfig
	Service map[string]string // service name -> enabled status
}

// Variable represents a template variable to be substituted.
type Variable struct {
	Type  string // VAR, LOCAL, SERVICE, MAPFILE, MAPLOCAL, FILE
	Key   string
	Value string
}

// Conditional represents an if/fi block in a template.
type Conditional struct {
	Type    string // VAR, SERVICE
	Key     string
	Negate  bool
	Content string
}
