// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// LoadServerConfig loads server-specific configuration from LDAP using zmprov gs.
func (cm *ConfigManager) LoadServerConfig(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	span := tracing.StartSpan("LoadServerConfig")
	defer tracing.EndSpan(span)

	return cm.loadServerConfigWithRetry(ctx, 3) // Retry up to 3 times for LDAP operations
}

// ServerConfigData holds both Data and ServiceConfig for caching
type ServerConfigData struct {
	Data          map[string]string
	ServiceConfig map[string]string
}

// loadServerConfigWithRetry implements retry logic for ServerConfig loading.
func (cm *ConfigManager) loadServerConfigWithRetry(ctx context.Context, maxRetries int) error {
	if cm.mainConfig.Hostname == "" {
		return fmt.Errorf("hostname required for ServerConfig load")
	}

	t1 := time.Now()

	cacheKey := fmt.Sprintf("ldap:server_config:%s", cm.mainConfig.Hostname)

	configData, err := loadConfigWithCache[*ServerConfigData](ctx, cm.Cache, cacheKey, func() (any, error) {
		return cm.fetchServerConfig(ctx, maxRetries)
	})
	if err != nil {
		return err
	}

	cm.State.ServerConfig.Data = configData.Data
	cm.State.ServerConfig.ServiceConfig = configData.ServiceConfig

	dt := time.Since(t1)
	logger.DebugContext(ctx, "ServerConfig loaded",
		"duration_seconds", dt.Seconds())

	return nil
}

// fetchServerConfig fetches fresh server config from LDAP (cache miss path)
func (cm *ConfigManager) fetchServerConfig(ctx context.Context, maxRetries int) (*ServerConfigData, error) {
	return retryWithBackoff(ctx, "ServerConfig", maxRetries, func() (*ServerConfigData, error) {
		cmd := commands.Commands["gs"]
		if cmd == nil {
			return nil, fmt.Errorf("gs command not available (commands.Initialize() not called)")
		}

		rc, output, errMsg := cmd.Execute(ctx, cm.mainConfig.Hostname)
		if rc != 0 {
			return nil, fmt.Errorf("gs command failed with rc=%d: %s", rc, errMsg)
		}

		if output == "" {
			return nil, fmt.Errorf("no data returned from gs")
		}

		// Parse LDAP attribute output (key: value format)
		configDataMap := parseLDAPCommandOutput(output)

		configData := &ServerConfigData{
			Data:          configDataMap,
			ServiceConfig: make(map[string]string),
		}

		cm.postProcessServerConfig(configData)

		return configData, nil
	})
}

// postProcessServerConfig applies all post-processing transformations to server
// config data: SSL sorting, network formatting, service enablement, RBL extraction,
// IP mode, milter config, and comma-separated list conversion.
//
// Operates directly on the supplied configData so no transient mutation of
// cm.State.ServerConfig is visible to concurrent readers.
func (cm *ConfigManager) postProcessServerConfig(configData *ServerConfigData) {
	data := configData.Data
	svc := configData.ServiceConfig

	// SSL protocol sorting
	processCommonSSLConfig(data)

	// zimbraMtaMyNetworksPerLine
	if value, ok := data["zimbraMtaMyNetworks"]; ok && value != "" {
		networks := strings.Fields(value)
		data["zimbraMtaMyNetworksPerLine"] = strings.Join(networks, "\n")
	}

	// Populate ServiceConfig from zimbraServiceEnabled
	if serviceList, ok := data[zimbraServiceEnabled]; ok && serviceList != "" {
		for s := range strings.FieldsSeq(serviceList) {
			svc[s] = zimbraServiceEnabled
			switch s {
			case "mailbox":
				svc["mailboxd"] = zimbraServiceEnabled
			case "mta":
				svc["sasl"] = zimbraServiceEnabled
			}
		}
	}

	// zimbraMtaRestriction - extract RBL lists
	processMtaRestrictionRBLsForData(data)

	// zimbraIPMode configuration
	processIPModeConfigForData(data)

	// zimbraMtaSmtpdMilters (milter configuration)
	processMilterConfigForData(data)

	// Convert space-separated lists to comma-separated
	for _, key := range []string{
		"zimbraMtaHeaderChecks",
		"zimbraMtaImportEnvironment",
		"zimbraMtaLmtpConnectionCacheDestinations",
		"zimbraMtaLmtpHostLookup",
		"zimbraMtaSmtpSaslMechanismFilter",
		"zimbraMtaNotifyClasses",
		"zimbraMtaPropagateUnmatchedExtensions",
		"zimbraMtaSmtpdSaslSecurityOptions",
		"zimbraMtaSmtpSaslSecurityOptions",
		"zimbraMtaSmtpdSaslTlsSecurityOptions",
	} {
		convertToCommaSeparatedForData(data, key)
	}
}

// rblType pairs an MTA restriction pattern keyword with the config data key
// where extracted domain matches are stored.
type rblType struct {
	pattern string
	dataKey string
}

// processRBLPatterns extracts and removes all RBL entries for each type in one pass,
// returning a map of dataKey→matches and the cleaned restriction string.
func processRBLPatterns(restriction string, types []rblType) (extracted map[string][]string, cleaned string) {
	extracted = make(map[string][]string, len(types))
	for _, t := range types {
		extracted[t.dataKey] = extractRBLMatches(restriction, t.pattern)
		restriction = removeRBLEntries(restriction, t.pattern)
	}

	return extracted, restriction
}

// processMtaRestrictionRBLsForData operates on the provided server config map
// without touching shared state.
func processMtaRestrictionRBLsForData(data map[string]string) {
	restriction, ok := data["zimbraMtaRestriction"]
	if !ok || restriction == "" {
		return
	}

	rblTypes := []rblType{
		{pattern: "reject_rbl_client", dataKey: "zimbraMtaRestrictionRBLs"},
		{pattern: "reject_rhsbl_client", dataKey: "zimbraMtaRestrictionRHSBLCs"},
		{pattern: "reject_rhsbl_sender", dataKey: "zimbraMtaRestrictionRHSBLSs"},
		{pattern: "reject_rhsbl_reverse_client", dataKey: "zimbraMtaRestrictionRHSBLRCs"},
	}

	extracted, cleaned := processRBLPatterns(restriction, rblTypes)
	for key, matches := range extracted {
		data[key] = strings.Join(matches, ", ")
	}

	data["zimbraMtaRestriction"] = cleaned
}

// extractRBLMatches finds all RBL entries matching the given pattern.
func extractRBLMatches(text, pattern string) []string {
	// Pattern: reject_rbl_client <domain>
	// Use simple string parsing instead of regex for better performance
	var matches []string

	words := strings.Fields(text)
	for i := 0; i < len(words)-1; i++ {
		if words[i] == pattern {
			matches = append(matches, words[i+1])
		}
	}

	return matches
}

// removeRBLEntries removes all RBL entries of the given pattern from text.
func removeRBLEntries(text, pattern string) string {
	// Pattern: reject_rbl_client <domain>
	words := strings.Fields(text)

	var result []string

	skip := false
	for i, word := range words {
		if skip {
			skip = false
			continue
		}

		if word == pattern && i < len(words)-1 {
			skip = true // Skip the next word (the domain)
			continue
		}

		result = append(result, word)
	}

	return strings.Join(result, " ")
}

// processIPModeConfigForData operates on the provided server config map
// without touching shared state.
func processIPModeConfigForData(data map[string]string) {
	ipMode, ok := data["zimbraIPMode"]
	if !ok || ipMode == "" {
		return
	}

	ipMode = strings.ToLower(ipMode)
	data["zimbraIPv4BindAddress"] = localhostIPv4

	switch ipMode {
	case constIPv4:
		data["zimbraUnboundBindAddress"] = localhostIPv4
		data["zimbraLocalBindAddress"] = localhostIPv4
		data["zimbraPostconfProtocol"] = constIPv4
		data["zimbraAmavisListenSockets"] = "'10024','10026','10032'"

		data["zimbraInetMode"] = "inet"
		if _, ok := data["zimbraMilterBindAddress"]; !ok {
			data["zimbraMilterBindAddress"] = localhostIPv4
		}
	case constIPv6:
		data["zimbraUnboundBindAddress"] = localhostIPv6
		data["zimbraLocalBindAddress"] = localhostIPv6
		data["zimbraPostconfProtocol"] = constIPv6
		data["zimbraAmavisListenSockets"] = "'[::1]:10024','[::1]:10026','[::1]:10032'"

		data["zimbraInetMode"] = "inet6"
		if _, ok := data["zimbraMilterBindAddress"]; !ok {
			data["zimbraMilterBindAddress"] = "[::1]"
		}
	case "both":
		data["zimbraUnboundBindAddress"] = localhostIPv4 + " " + localhostIPv6
		data["zimbraLocalBindAddress"] = localhostIPv6
		data["zimbraPostconfProtocol"] = "all"
		data["zimbraAmavisListenSockets"] =
			"'10024','10026','10032','[::1]:10024','[::1]:10026','[::1]:10032'"

		data["zimbraInetMode"] = "inet6"
		if _, ok := data["zimbraMilterBindAddress"]; !ok {
			data["zimbraMilterBindAddress"] = "[::1]"
		}
	}
}

// processMilterConfigForData operates on the provided server config map
// without touching shared state.
func processMilterConfigForData(data map[string]string) {
	var milter string

	if enabled, ok := data["zimbraMilterServerEnabled"]; ok && enabled == constTRUE {
		bindAddr := data["zimbraMilterBindAddress"]

		bindPort := data["zimbraMilterBindPort"]
		if bindAddr != "" && bindPort != "" {
			milter = fmt.Sprintf("inet:%s:%s", bindAddr, bindPort)
		}
	} else {
		data["zimbraMtaSmtpdMilters"] = ""
	}

	if milter != "" {
		existingMilters, ok := data["zimbraMtaSmtpdMilters"]
		if ok && existingMilters != "" {
			data["zimbraMtaSmtpdMilters"] = fmt.Sprintf("%s, %s", existingMilters, milter)
		} else {
			data["zimbraMtaSmtpdMilters"] = milter
		}
	}
}

// convertToCommaSeparatedForData operates on the provided server config map
// without touching shared state.
func convertToCommaSeparatedForData(data map[string]string, key string) {
	if value, ok := data[key]; ok && value != "" {
		values := strings.Fields(value)
		data[key] = strings.Join(values, ", ")
	}
}
