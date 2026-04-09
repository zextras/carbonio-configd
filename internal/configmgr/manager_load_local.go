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

	_, err := retryWithBackoff(ctx, "LocalConfig", maxRetries, func() (struct{}, error) {
		output, err := cm.executeLocalConfigCommand(ctx)
		if err != nil {
			return struct{}{}, err
		}

		if err := cm.parseLocalConfigOutput(ctx, output); err != nil {
			return struct{}{}, err
		}

		return struct{}{}, nil
	})
	if err != nil {
		return err
	}

	// Post-process configuration
	cm.postProcessLocalConfig()

	// Update main config based on local config
	cm.mainConfig.SetVals(cm.State.LocalConfig)

	dt := time.Since(t1)
	logger.DebugContext(ctx, "Localconfig load completed",
		"duration_seconds", dt.Seconds())

	return nil
}

// executeLocalConfigCommand loads localconfig by directly parsing XML file.
func (cm *ConfigManager) executeLocalConfigCommand(ctx context.Context) (string, error) {
	// Check if we have cached output from previous runs
	if cm.cachedLocalConfigOutput != "" {
		logger.DebugContext(ctx, "Using cached localconfig output")

		return cm.cachedLocalConfigOutput, nil
	}

	// Load configuration directly from XML file
	localCfg, err := localconfig.LoadLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load localconfig: %w", err)
	}

	// Convert to key=value format matching old zmlocalconfig -s output
	output := localconfig.FormatAsKeyValue(localCfg)

	// Cache the output for subsequent runs
	cm.cachedLocalConfigOutput = output

	logger.DebugContext(ctx, "Loaded and cached localconfig from XML",
		"key_count", len(localCfg))

	return output, nil
}

// parseLocalConfigOutput parses the output from localconfig command.
func (cm *ConfigManager) parseLocalConfigOutput(ctx context.Context, output string) error {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	logger.DebugContext(ctx, "Localconfig loaded",
		"entry_count", len(lines))

	if len(lines) == 0 {
		logger.DebugContext(ctx, "No data returned")

		return fmt.Errorf("no data returned from localconfig")
	}

	// Parse key=value pairs
	cm.State.LocalConfig.Data = make(map[string]string)

	for _, line := range lines {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			continue
		}

		cm.State.LocalConfig.Data[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	return nil
}

// postProcessLocalConfig performs post-processing on local configuration.
func (cm *ConfigManager) postProcessLocalConfig() {
	// Set default for zmconfigd_listen_port if not present
	if _, ok := cm.State.LocalConfig.Data["zmconfigd_listen_port"]; !ok {
		cm.State.LocalConfig.Data["zmconfigd_listen_port"] = "7171"
	}

	// Derive OpenDKIM URIs from ldap_url if present
	if ldapURL, ok := cm.State.LocalConfig.Data["ldap_url"]; ok && ldapURL != "" {
		urls := strings.Fields(ldapURL)
		signingTableURIs := make([]string, len(urls))
		keyTableURIs := make([]string, len(urls))

		for i, url := range urls {
			signingTableURIs[i] = url + "/?DKIMSelector?sub?(DKIMIdentity=$d)"
			keyTableURIs[i] = url + "/?DKIMDomain,DKIMSelector,DKIMKey,?sub?(DKIMSelector=$d)"
		}

		cm.State.LocalConfig.Data["opendkim_signingtable_uri"] = strings.Join(signingTableURIs, " ")
		cm.State.LocalConfig.Data["opendkim_keytable_uri"] = strings.Join(keyTableURIs, " ")
	}
}
