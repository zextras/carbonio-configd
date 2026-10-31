// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/snapshot"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// LoadLocalConfig loads local configuration using zmlocalconfig command.
func (cm *ConfigManager) LoadLocalConfig(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	span := tracing.StartSpan("LoadLocalConfig")
	defer tracing.EndSpan(span)

	return cm.loadLocalConfigWithRetry(ctx, 3) // Retry up to 3 times
}

// loadLocalConfigWithRetry implements retry logic for LocalConfig loading.
func (cm *ConfigManager) loadLocalConfigWithRetry(ctx context.Context, maxRetries int) error {
	t1 := time.Now()

	data, err := retryWithBackoff(ctx, cm.env.Clock, "LocalConfig", maxRetries, func() (map[string]string, error) {
		output, err := cm.executeLocalConfigCommand(ctx)
		if err != nil {
			return nil, err
		}

		return cm.parseLocalConfigOutput(ctx, output)
	})
	if err != nil {
		return err
	}

	// Do not publish once the caller gave up on this load.
	if err := ctx.Err(); err != nil {
		return err
	}

	cm.sink(ctx).setLocal(data)

	// Update main config based on local config
	cm.mainConfig.SetVals(snapshot.New(snapshot.Parts{Local: data}).LocalConfig())

	dt := time.Since(t1)
	logger.DebugContext(ctx, "Localconfig load completed",
		"duration_seconds", dt.Seconds())

	return nil
}

// executeLocalConfigCommand loads localconfig by directly parsing XML file.
func (cm *ConfigManager) executeLocalConfigCommand(ctx context.Context) (string, error) {
	// Check if we have cached output from previous runs
	cm.cachedLocalConfigMu.RLock()
	cachedOutput := cm.cachedLocalConfigOutput
	cm.cachedLocalConfigMu.RUnlock()

	if cachedOutput != "" {
		logger.DebugContext(ctx, "Using cached localconfig output")

		return cachedOutput, nil
	}

	// Legacy getlocal (jylibs/commands.py) reads LC.get over LC.getAllKeys: the
	// LC.java defaults merged under localconfig.xml with ${var} expanded
	// (zmlocalconfig -s -x).
	localCfg, err := cm.loadResolvedLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load localconfig: %w", err)
	}

	// Convert to key=value format matching old zmlocalconfig -s output
	output := localconfig.FormatAsKeyValue(localCfg)

	// Cache the output for subsequent runs
	cm.cachedLocalConfigMu.Lock()
	cm.cachedLocalConfigOutput = output
	cm.cachedLocalConfigMu.Unlock()

	logger.DebugContext(ctx, "Loaded and cached localconfig from XML",
		"key_count", len(localCfg))

	return output, nil
}

// parseLocalConfigOutput parses the output from localconfig command and applies
// the post-processing defaults. Nothing is returned once ctx is cancelled.
func (cm *ConfigManager) parseLocalConfigOutput(ctx context.Context, output string) (map[string]string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	logger.DebugContext(ctx, "Localconfig loaded",
		"entry_count", len(lines))

	if len(lines) == 0 {
		logger.DebugContext(ctx, "No data returned")

		return nil, fmt.Errorf("no data returned from localconfig")
	}

	// Parse key=value pairs
	data := make(map[string]string, len(lines))

	for _, line := range lines {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			continue
		}

		data[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	applyLocalConfigDefaults(data)

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("localconfig load cancelled before publish: %w", err)
	}

	return data, nil
}

// applyLocalConfigDefaults performs post-processing on a parsed local configuration.
func applyLocalConfigDefaults(data map[string]string) {
	// Set default for zmconfigd_listen_port if not present
	if _, ok := data["zmconfigd_listen_port"]; !ok {
		data["zmconfigd_listen_port"] = "7171"
	}

	// Derive OpenDKIM URIs from ldap_url if present
	if ldapURL := data["ldap_url"]; ldapURL != "" {
		urls := strings.Fields(ldapURL)
		signingTableURIs := make([]string, len(urls))
		keyTableURIs := make([]string, len(urls))

		for i, url := range urls {
			signingTableURIs[i] = url + "/?DKIMSelector?sub?(DKIMIdentity=$d)"
			keyTableURIs[i] = url + "/?DKIMDomain,DKIMSelector,DKIMKey,?sub?(DKIMSelector=$d)"
		}

		data["opendkim_signingtable_uri"] = strings.Join(signingTableURIs, " ")
		data["opendkim_keytable_uri"] = strings.Join(keyTableURIs, " ")
	}
}
