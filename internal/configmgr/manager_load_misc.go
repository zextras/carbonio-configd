// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// miscCmdResult holds the outcome of a single misc command execution.
type miscCmdResult struct {
	cmdName string
	name    string // key under which the output is published in MiscConfig
	output  string
	err     error
}

// LoadMiscConfig loads miscellaneous configuration by executing misc commands.
// Commands are executed in parallel to improve performance.
func (cm *ConfigManager) LoadMiscConfig(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	span := tracing.StartSpan("LoadMiscConfig")
	defer tracing.EndSpan(span)

	t1 := time.Now()

	miscCommands := []string{"garpu", "garpb", "gamau"}

	var wg sync.WaitGroup

	resultsChan := make(chan miscCmdResult, len(miscCommands))

	for _, cmdName := range miscCommands {
		wg.Go(func() {
			result := cm.executeMiscCommand(ctx, cmdName)
			resultsChan <- result
		})
	}

	wg.Wait()
	close(resultsChan)

	successCount := 0
	published := make(map[string]string, len(miscCommands))

	for result := range resultsChan {
		if result.err == nil && result.output != "" {
			successCount++
			published[result.name] = result.output
		}
	}

	// Publish only when the caller still wants this load: a cancelled (e.g.
	// timed-out) loader must not overwrite the state of a later cycle.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("misc config load cancelled before publish: %w", err)
	}

	cm.sink(ctx).setMisc(published)

	dt := time.Since(t1)
	logger.DebugContext(ctx, "Miscconfig loaded",
		"duration_seconds", dt.Seconds(),
		"successful_commands", successCount,
		"total_commands", len(miscCommands))

	return nil
}

// executeMiscCommand fetches a single misc command (via cache or directly)
// and returns the outcome; the caller publishes it.
func (cm *ConfigManager) executeMiscCommand(ctx context.Context, cmdName string) miscCmdResult {
	output, err := cm.resolveMiscOutput(ctx, cmdName)
	if err != nil {
		logger.WarnContext(ctx, "Failed to load misc command",
			"command", cmdName, "error", err)

		return miscCmdResult{cmdName: cmdName, err: err}
	}

	if output == "" {
		return miscCmdResult{cmdName: cmdName}
	}

	cmdObj := cm.CommandRegistry.Commands[cmdName]
	if cmdObj == nil {
		logger.WarnContext(ctx, "Command object not available for storing result",
			"command", cmdName)

		return miscCmdResult{cmdName: cmdName, err: fmt.Errorf("command not available")}
	}

	logger.DebugContext(ctx, "Fetched misc command output",
		"command_name", cmdObj.Name, "output", output)

	return miscCmdResult{cmdName: cmdName, name: cmdObj.Name, output: output}
}

// resolveMiscOutput returns the output for a misc command, using the cache when available.
func (cm *ConfigManager) resolveMiscOutput(ctx context.Context, cmdName string) (string, error) {
	if cm.Cache == nil {
		return cm.fetchMiscCommand(ctx, cmdName)
	}

	cacheKey := fmt.Sprintf("ldap:misc:%s", cmdName)

	cachedData, err := cm.Cache.GetCachedConfig(ctx, cacheKey, func() (any, error) {
		return cm.fetchMiscCommand(ctx, cmdName)
	})
	if err != nil {
		return "", err
	}

	output, _ := cachedData.(string)

	return output, nil
}

// fetchMiscCommand fetches a single misc command output
//
//nolint:unparam // error return required for cache interface compatibility
func (cm *ConfigManager) fetchMiscCommand(ctx context.Context, cmdName string) (string, error) {
	cmd := cm.CommandRegistry.Commands[cmdName]

	// If command is not found (nil), skip execution
	// This happens in test environments where the LDAP commands aren't registered
	if cmd == nil {
		logger.WarnContext(ctx, "Command not available, skipping",
			"command", cmdName)

		return "", nil
	}

	rc, output, _ := cmd.Execute(ctx)
	if rc != 0 {
		logger.WarnContext(ctx, "Skipping misc command update",
			"description", cmd.Desc)
		logger.DebugContext(ctx, "Command details",
			"command", cmd.String())
		// Return empty string, not error (matches original behavior)
		return "", nil
	}

	if output == "" {
		logger.WarnContext(ctx, "Skipping misc command - no data returned",
			"description", cmd.Desc)

		return "", nil
	}

	return output, nil
}

// LoadGlobalConfig loads global configuration from LDAP using zmprov gacf.
func (cm *ConfigManager) LoadGlobalConfig(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	span := tracing.StartSpan("LoadGlobalConfig")
	defer tracing.EndSpan(span)

	return cm.loadGlobalConfigWithRetry(ctx, 3) // Retry up to 3 times for LDAP operations
}

// loadGlobalConfigWithRetry implements retry logic for GlobalConfig loading.
// maxRetries is kept as a parameter for symmetry with
// loadServerConfigWithRetry and to allow per-call tuning, even though all
// current callers pass 3.
//
//nolint:unparam // see comment above
func (cm *ConfigManager) loadGlobalConfigWithRetry(ctx context.Context, maxRetries int) error {
	t1 := time.Now()

	configData, err := cm.cachedGlobalConfig(ctx, maxRetries)
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("global config load cancelled before publish: %w", err)
	}

	cm.sink(ctx).setGlobal(configData)

	dt := time.Since(t1)
	logger.DebugContext(ctx, "GlobalConfig loaded",
		"duration_seconds", dt.Seconds())

	return nil
}

// cachedGlobalConfig returns the global config through the shared cache entry.
func (cm *ConfigManager) cachedGlobalConfig(ctx context.Context, maxRetries int) (map[string]string, error) {
	return loadConfigWithCache[map[string]string](ctx, cm.Cache, "ldap:global_config", func() (any, error) {
		return cm.fetchGlobalConfig(ctx, maxRetries)
	})
}

// fetchGlobalConfig fetches fresh global config from LDAP (cache miss path)
func (cm *ConfigManager) fetchGlobalConfig(ctx context.Context, maxRetries int) (map[string]string, error) {
	return retryWithBackoff(ctx, cm.env.Clock, "GlobalConfig", maxRetries, func() (map[string]string, error) {
		configData, err := cm.fetchGlobalAttrs(ctx)
		if err != nil {
			return nil, err
		}

		// Post-processing: Set zimbraQuarantineBannedItems based on conditions
		if configData["zimbraMtaBlockedExtensionWarnRecipient"] == constTRUE &&
			configData["zimbraAmavisQuarantineAccount"] != "" {
			configData["zimbraQuarantineBannedItems"] = constTRUE
		} else {
			configData["zimbraQuarantineBannedItems"] = constFALSE
		}

		// Post-processing: Sort and create XML versions of SSL protocol lists.
		// Operate directly on the local configData to avoid transient writes
		// to shared state (cm.State.GlobalConfig.Data) that concurrent readers
		// could observe.
		processCommonSSLConfig(configData)

		return configData, nil
	})
}
