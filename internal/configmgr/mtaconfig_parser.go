// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/parser"
)

// ParseMtaConfig parses the zmconfigd.cf file using the proper parser and populates the MtaConfig.
func (cm *ConfigManager) ParseMtaConfig(ctx context.Context, configFile string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Parsing MTA config file",
		"config_file", configFile)

	// Create parser with an empty lexer - Parse() will reinitialize it with file content
	emptyLexer := parser.NewLexer(ctx, strings.NewReader(""))
	p := parser.NewParser(emptyLexer)

	// Parse the config file
	mtaConfig, err := p.Parse(ctx, configFile)
	if err != nil {
		return fmt.Errorf("failed to parse MTA config file %s: %w", configFile, err)
	}

	// Copy parsed sections to state's MtaConfig
	for sectionName, section := range mtaConfig.Sections {
		logger.DebugContext(ctx, "Found SECTION",
			"section_name", sectionName)

		// Process LDAP directives that require lookups
		for ldapKey, lookupSpec := range section.Ldap {
			// lookupSpec format: "TYPE:KEY" (e.g., "LOCAL:ldap_url")
			parts := strings.SplitN(lookupSpec, ":", 2)
			if len(parts) != 2 {
				logger.WarnContext(ctx, "Invalid LDAP lookup spec",
					"ldap_key", ldapKey,
					"lookup_spec", lookupSpec)

				continue
			}

			lookupType := parts[0]
			lookupKey := parts[1]

			val, err := cm.LookUpConfig(ctx, lookupType, lookupKey)
			if err != nil {
				logger.WarnContext(ctx, "Error looking up config for LDAP",
					"ldap_key", ldapKey,
					"lookup_type", lookupType,
					"lookup_key", lookupKey,
					"error", err)

				val = "" // Treat lookup failure as empty string
			}

			// Update the section's LDAP map with the resolved value
			section.Ldap[ldapKey] = val
			logger.DebugContext(ctx, "Adding LDAP to section",
				"ldap_key", ldapKey,
				"value", val,
				"section", sectionName)
		}

		// Log section details
		if len(section.Rewrites) > 0 {
			logger.DebugContext(ctx, "Section has rewrites",
				"section", sectionName,
				"count", len(section.Rewrites))
		}

		if len(section.Restarts) > 0 {
			logger.DebugContext(ctx, "Section has restarts",
				"section", sectionName,
				"count", len(section.Restarts))
		}

		if len(section.Postconf) > 0 {
			logger.DebugContext(ctx, "Section has postconf",
				"section", sectionName,
				"count", len(section.Postconf))
		}

		if len(section.Depends) > 0 {
			logger.DebugContext(ctx, "Section has dependencies",
				"section", sectionName,
				"count", len(section.Depends))
		}

		if section.Proxygen {
			logger.DebugContext(ctx, "Section has PROXYGEN",
				"section", sectionName)
		}
	}

	// Replace state's MtaConfig with parsed config
	cm.State.MtaConfig = mtaConfig

	logger.DebugContext(ctx, "Finished parsing MTA config file",
		"section_count", len(mtaConfig.Sections))

	return nil
}
