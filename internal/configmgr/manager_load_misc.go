// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// miscCmdResult holds the outcome of a single misc command execution.
type miscCmdResult struct {
	cmdName string
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

	cm.State.MiscConfig.Data = make(map[string]string)

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	resultsChan := make(chan miscCmdResult, len(miscCommands))

	for _, cmdName := range miscCommands {
		wg.Go(func() {
			result := cm.executeMiscCommand(ctx, cmdName, &mu)
			resultsChan <- result
		})
	}

	wg.Wait()
	close(resultsChan)

	successCount := 0

	for result := range resultsChan {
		if result.err == nil && result.output != "" {
			successCount++
		}
	}

	dt := time.Since(t1)
	logger.DebugContext(ctx, "Miscconfig loaded",
		"duration_seconds", dt.Seconds(),
		"successful_commands", successCount,
		"total_commands", len(miscCommands))

	return nil
}

// executeMiscCommand fetches a single misc command (via cache or directly),
// stores the result in MiscConfig.Data, and returns the outcome.
func (cm *ConfigManager) executeMiscCommand(ctx context.Context, cmdName string, mu *sync.Mutex) miscCmdResult {
	output, err := cm.resolveMiscOutput(ctx, cmdName)
	if err != nil {
		logger.WarnContext(ctx, "Failed to load misc command",
			"command", cmdName, "error", err)

		return miscCmdResult{cmdName: cmdName, err: err}
	}

	if output == "" {
		return miscCmdResult{cmdName: cmdName}
	}

	cmdObj := commands.Commands[cmdName]
	if cmdObj == nil {
		logger.WarnContext(ctx, "Command object not available for storing result",
			"command", cmdName)

		return miscCmdResult{cmdName: cmdName, err: fmt.Errorf("command not available")}
	}

	mu.Lock()
	cm.State.MiscConfig.Data[cmdObj.Name] = output
	mu.Unlock()

	logger.DebugContext(ctx, "Stored misc command output",
		"command_name", cmdObj.Name, "output", output)

	return miscCmdResult{cmdName: cmdName, output: output}
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
	cmd := commands.Commands[cmdName]

	// If command is not found (nil), skip execution
	// This happens in test environments where commands.Initialize() isn't called
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
		logger.ErrorContext(ctx, "Skipping misc command - no data returned",
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

	configData, err := loadConfigWithCache[map[string]string](ctx, cm.Cache, "ldap:global_config", func() (any, error) {
		return cm.fetchGlobalConfig(ctx, maxRetries)
	})
	if err != nil {
		return err
	}

	cm.State.GlobalConfig.Data = configData

	dt := time.Since(t1)
	logger.DebugContext(ctx, "GlobalConfig loaded",
		"duration_seconds", dt.Seconds())

	return nil
}

// fetchGlobalConfig fetches fresh global config from LDAP (cache miss path)
func (cm *ConfigManager) fetchGlobalConfig(ctx context.Context, maxRetries int) (map[string]string, error) {
	return retryWithBackoff(ctx, "GlobalConfig", maxRetries, func() (map[string]string, error) {
		cmd := commands.Commands["gacf"]
		if cmd == nil {
			return nil, fmt.Errorf("gacf command not available (commands.Initialize() not called)")
		}

		rc, output, errMsg := cmd.Execute(ctx)
		if rc != 0 {
			return nil, fmt.Errorf("gacf command failed with rc=%d: %s", rc, errMsg)
		}

		if output == "" {
			return nil, fmt.Errorf("no data returned from gacf")
		}

		// Parse LDAP attribute output (key: value format)
		configData := parseLDAPCommandOutput(output)

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

// LoadMtaConfig loads the zmconfigd.cf file.
func (cm *ConfigManager) LoadMtaConfig(ctx context.Context, configFile string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	// This will parse the zmconfigd.cf file.
	// For now, it's a placeholder.
	logger.DebugContext(ctx, "Loading MTA config from file",
		"config_file", configFile)

	// Simulate parsing a simple config file
	section := &config.MtaConfigSection{
		Name:         "proxy",
		Depends:      make(map[string]bool),
		Rewrites:     make(map[string]config.RewriteEntry),
		Restarts:     make(map[string]bool),
		RequiredVars: make(map[string]string),
		Postconf:     make(map[string]string),
		Postconfd:    make(map[string]string),
		Ldap:         make(map[string]string),
	}
	section.Rewrites["conf/nginx/nginx.conf.zmconfigd"] = config.RewriteEntry{Value: "conf/nginx/nginx.conf", Mode: "0644"}
	section.Restarts["proxy"] = true
	section.RequiredVars["zimbraReverseProxyLookupTarget"] = "VAR"
	cm.State.MtaConfig.Sections["proxy"] = section

	logger.DebugContext(ctx, "MTA config loaded")

	return nil
}
