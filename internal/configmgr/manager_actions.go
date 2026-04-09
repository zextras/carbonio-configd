// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/state"
)

const (
	serviceEnabled  = "enabled"
	serviceDisabled = "disabled"
)

// CompileActions compiles the MTA configuration actions.
func (cm *ConfigManager) CompileActions(ctx context.Context) {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	logger.DebugContext(ctx, "Compiling actions")

	// Snapshot and clear requested configs so we process exactly what was
	// requested, while allowing new requests to accumulate for the next loop.
	// Also snapshot other State fields to avoid races with concurrent goroutines.
	snapshot := cm.State.SnapshotCompileActions()
	requestedConfigs := snapshot.RequestedConfig
	mtaSections := snapshot.MtaSections
	forcedConfig := snapshot.ForcedConfig
	firstRun := snapshot.FirstRun
	serviceConfig := snapshot.ServiceConfig

	for sn, section := range mtaSections {
		logger.DebugContext(ctx, "Compiling actions for section",
			"section", sn)

		if !shouldProcessSection(ctx, sn, section, forcedConfig, requestedConfigs, firstRun) {
			logger.DebugContext(ctx, "Section did not change, skipping action compilation",
				"section", sn)

			continue
		}

		cm.compileSectionActions(ctx, sn, section, requestedConfigs, forcedConfig, firstRun, serviceConfig)
	}

	logger.DebugContext(ctx, "Action compilation complete")
}

func shouldProcessSection(
	ctx context.Context, sn string, section *config.MtaConfigSection,
	forcedConfig, requestedConfigs map[string]string, firstRun bool,
) bool {
	if _, ok := forcedConfig[sn]; ok {
		logger.DebugContext(ctx, "Processing forced config for section", "section", sn)
		return true
	}

	if _, ok := requestedConfigs[sn]; ok {
		logger.DebugContext(ctx, "Processing requested config for section", "section", sn)
		return true
	}

	return section.Changed || firstRun
}

// compileSectionRestarts queues restart or stop actions for each service in section.Restarts.
func (cm *ConfigManager) compileSectionRestarts(
	ctx context.Context,
	section *config.MtaConfigSection,
	serviceConfig map[string]string,
) {
	logger.DebugContext(ctx, "Section changed, compiling restarts")

	for restartService := range section.Restarts {
		isServiceEnabled, err := cm.LookUpConfig(ctx, "SERVICE", restartService)
		if err == nil && state.IsTrueValue(isServiceEnabled) {
			logger.DebugContext(ctx, "Adding restart", "service", restartService)
			cm.State.CurRestarts(restartService, -1)

			continue
		}

		// Special handling for opendkim carried over from Jython: if the MTA is
		// enabled we force a restart of opendkim rather than stopping it.
		if restartService == "opendkim" && state.IsTrueValue(serviceConfig["mta"]) {
			logger.DebugContext(ctx, "Adding restart opendkim")
			cm.State.CurRestarts(restartService, -1)

			continue
		}

		logger.DebugContext(ctx, "Adding stop", "service", restartService)
		cm.State.CurRestarts(restartService, 0)
	}
}

// compileSectionActions compiles actions for a changed section.
func (cm *ConfigManager) compileSectionActions(
	ctx context.Context, sn string, section *config.MtaConfigSection,
	requestedConfigs, forcedConfig map[string]string, firstRun bool, serviceConfig map[string]string) {
	logger.DebugContext(ctx, "Section changed, compiling rewrites", "section", sn)

	if len(section.Rewrites) > 0 {
		logger.DebugContext(ctx, "Service queuing files for rewrite",
			"service", sn, "file_count", len(section.Rewrites))

		for rewriteKey, rewriteEntry := range section.Rewrites {
			logger.DebugContext(ctx, "Queuing file rewrite",
				"source", rewriteKey, "target", rewriteEntry.Value)
			cm.State.CurRewrites(ctx, rewriteKey, &rewriteEntry)
		}
	}

	logger.DebugContext(ctx, "Section changed, compiling postconf", "section", sn)

	for postconfKey, postconfVal := range section.Postconf {
		cm.State.CurPostconf(ctx, postconfKey, postconfVal)
	}

	logger.DebugContext(ctx, "Section changed, compiling postconfd", "section", sn)

	for postconfdKey, postconfdVal := range section.Postconfd {
		cm.State.CurPostconfd(ctx, postconfdKey, postconfdVal)
	}

	if section.Proxygen {
		logger.DebugContext(ctx, "Section has PROXYGEN directive, compiling proxygen", "section", sn)
		cm.State.Proxygen(true)
	}

	logger.DebugContext(ctx, "Section changed, compiling ldap", "section", sn)

	for ldapKey, ldapVal := range section.Ldap {
		cm.State.CurLdap(ctx, ldapKey, ldapVal)
	}

	logger.DebugContext(ctx, "Section changed, compiling conditionals", "section", sn)
	cm.processConditionals(ctx, section.Conditionals)

	// Restarts are not triggered on forced or first run (mirroring Jython)
	if firstRun || len(forcedConfig) > 0 || len(requestedConfigs) > 0 {
		return
	}

	cm.compileSectionRestarts(ctx, section, serviceConfig)
}
