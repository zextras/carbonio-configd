// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"strings"

	"github.com/zextras/carbonio-configd/internal/config"
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

	serviceConfig := cm.State.ServerConfig.ServiceConfig.Snapshot()

	for sectionName, section := range mtaConfig.Sections {
		logger.DebugContext(ctx, "Found SECTION",
			"section_name", sectionName)

		// Mirror jylibs/mtaconfig.py load: a section whose mapped node service
		// is not enabled is skipped entirely. Dropping it here keeps it out of
		// every consumer (compare, rewrites, mapfiles, restarts, dependencies)
		// and means no LDAP lookups are resolved for it. Deleting the current
		// key while ranging a map is well defined.
		if !sectionEnabledOnNode(sectionName, serviceConfig) {
			logger.DebugContext(ctx, "Section service not enabled on node, dropping section",
				"section", sectionName)

			delete(mtaConfig.Sections, sectionName)

			continue
		}

		cm.resolveSectionLDAP(ctx, sectionName, section)
		cm.registerConditionalRequiredVars(ctx, section, section.Conditionals)
		logSectionDetails(ctx, sectionName, section)
	}

	cm.State.SetMtaConfig(mtaConfig)

	logger.DebugContext(ctx, "Finished parsing MTA config file",
		"section_count", len(mtaConfig.Sections))

	return nil
}

// registerConditionalRequiredVars adds the lookups named by POSTCONF values of
// the if-blocks that hold on this node to the section's required vars. Legacy
// evaluates each if at load time and only a true branch runs requiredvars(), so
// a variable used solely by a false branch must not mark the section changed
// (that would restart services for no effective change). Nested blocks count
// only when every enclosing block holds.
func (cm *ConfigManager) registerConditionalRequiredVars(
	ctx context.Context,
	section *config.MtaConfigSection,
	conditionals []config.Conditional,
) {
	for i := range conditionals {
		cond := &conditionals[i]

		holds, err := cm.CheckConditional(ctx, cond.Type, cond.Key)
		if err != nil || holds == cond.Negated {
			continue
		}

		for _, value := range cond.Postconf {
			if name, cfgType, ok := parser.PostconfRequiredVar(value, true); ok {
				section.RequiredVars[name] = cfgType
			}
		}

		cm.registerConditionalRequiredVars(ctx, section, cond.Nested)
	}
}

func (cm *ConfigManager) resolveSectionLDAP(ctx context.Context, sectionName string, section *config.MtaConfigSection) {
	for ldapKey, lookupSpec := range section.Ldap {
		parts := strings.SplitN(lookupSpec, ":", 2)
		if len(parts) != 2 {
			logger.WarnContext(ctx, "Invalid LDAP lookup spec",
				"ldap_key", ldapKey, "lookup_spec", lookupSpec)

			continue
		}

		val, err := cm.LookUpConfig(ctx, parts[0], parts[1])
		if err != nil {
			logger.WarnContext(ctx, "Error looking up config for LDAP",
				"ldap_key", ldapKey, "lookup_type", parts[0],
				"lookup_key", parts[1], "error", err)

			val = ""
		}

		section.Ldap[ldapKey] = val
		logger.DebugContext(ctx, "Adding LDAP to section",
			"ldap_key", ldapKey, "value", val, "section", sectionName)
	}
}

func logSectionDetails(ctx context.Context, sectionName string, section *config.MtaConfigSection) {
	if len(section.Rewrites) > 0 {
		logger.DebugContext(ctx, "Section has rewrites",
			"section", sectionName, "count", len(section.Rewrites))
	}

	if len(section.Restarts) > 0 {
		logger.DebugContext(ctx, "Section has restarts",
			"section", sectionName, "count", len(section.Restarts))
	}

	if len(section.Postconf) > 0 {
		logger.DebugContext(ctx, "Section has postconf",
			"section", sectionName, "count", len(section.Postconf))
	}

	if len(section.Depends) > 0 {
		logger.DebugContext(ctx, "Section has dependencies",
			"section", sectionName, "count", len(section.Depends))
	}

	if section.Proxygen {
		logger.DebugContext(ctx, "Section has PROXYGEN", "section", sectionName)
	}
}
