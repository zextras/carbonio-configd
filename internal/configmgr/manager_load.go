// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/cache"
	"github.com/zextras/carbonio-configd/internal/commands"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/intern"
	"github.com/zextras/carbonio-configd/internal/localconfig"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// loadFunc pairs a name with a config-loading function for concurrent execution.
type loadFunc struct {
	name string
	fn   func(context.Context) error
}

// runLoadAttempt launches all loadFuncs concurrently and waits with a two-stage
// timeout. Returns whether the attempt timed out, any loader errors, and a non-nil
// ctxErr when the context is cancelled.
func runLoadAttempt(
	ctx context.Context,
	loadFuncs []loadFunc,
	timeout time.Duration,
) (timedOut bool, errs []error, ctxErr error) {
	var wg sync.WaitGroup

	errChan := make(chan error, len(loadFuncs))
	threadStatus := make(map[string]bool)

	var statusMu sync.Mutex

	threadTimings := make(map[string]time.Duration)

	var timingMu sync.Mutex

	for _, lf := range loadFuncs {
		wg.Go(func() {
			logger.DebugContext(ctx, "Starting config load", "thread", lf.name)

			threadStart := time.Now()
			loadErr := lf.fn(ctx)
			threadDuration := time.Since(threadStart)

			timingMu.Lock()
			threadTimings[lf.name] = threadDuration
			timingMu.Unlock()

			logger.DebugContext(ctx, "Timing: Config load duration",
				"thread", lf.name,
				"duration_seconds", threadDuration.Seconds())

			statusMu.Lock()
			threadStatus[lf.name] = loadErr == nil
			statusMu.Unlock()

			if loadErr != nil {
				errChan <- fmt.Errorf("thread %s failed: %w", lf.name, loadErr)
			}

			logger.DebugContext(ctx, "Finished config load", "thread", lf.name)
		})
	}

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
		close(errChan)
	}()

	select {
	case <-ctx.Done():
		logger.InfoContext(ctx, "Configuration loading cancelled by shutdown signal")
		<-done

		return false, nil, ctx.Err()
	case <-done:
		logger.DebugContext(ctx, "All config threads completed")
	case <-time.After(timeout):
		logger.ErrorContext(ctx, "Configuration loading timed out", "timeout", timeout)

		statusMu.Lock()
		for _, lf := range loadFuncs {
			if !threadStatus[lf.name] {
				logger.WarnContext(ctx, "Thread still alive, waiting",
					"thread", lf.name,
					"wait_seconds", int(timeout.Seconds()))
			}
		}
		statusMu.Unlock()

		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Configuration loading cancelled during retry wait")
			<-done

			return false, nil, ctx.Err()
		case <-done:
			logger.DebugContext(ctx, "All threads completed after extended wait")
		case <-time.After(timeout):
			statusMu.Lock()
			for _, lf := range loadFuncs {
				if !threadStatus[lf.name] {
					logger.ErrorContext(ctx, "Thread still alive, aborting", "thread", lf.name)
				}
			}
			statusMu.Unlock()

			timedOut = true

			<-done
		}
	}

	for err := range errChan {
		logger.ErrorContext(ctx, "Error during config load", "error", err)
		errs = append(errs, err)
	}

	return timedOut, errs, nil
}

// LoadAllConfigs loads all configurations (local, global, server, misc).
func (cm *ConfigManager) LoadAllConfigs(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")
	return cm.LoadAllConfigsWithRetry(ctx, 1) // Default: single attempt (no retry)
}

// LoadAllConfigsWithRetry loads all configurations with specified retry attempts.
func (cm *ConfigManager) LoadAllConfigsWithRetry(ctx context.Context, maxRetries int) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	span := tracing.StartSpan("LoadAllConfigs")
	defer tracing.EndSpan(span)

	logger.DebugContext(ctx, "Fetching all configs")

	// Always invalidate caches before loading configs.
	// The polling interval (default 300s) is the throttle — caching across
	// loops defeats configd's core purpose of detecting LDAP changes.
	cm.ClearLocalConfigCache(ctx)
	cm.InvalidateLDAPCache(ctx)
	commands.ResetProvisioning(ctx, "config")
	commands.ResetProvisioning(ctx, "server")
	commands.ResetProvisioning(ctx, "local")

	cm.State.FileCache = make(map[string]string)

	ldapReadTimeoutStr, ok := cm.State.LocalConfig.Data["ldap_read_timeout"]
	ldapReadTimeout := 60000 // Default to 60 seconds (60000 ms)

	if ok {
		if timeout, err := strconv.Atoi(ldapReadTimeoutStr); err == nil {
			ldapReadTimeout = timeout
		}
	}

	threadWaitTime := time.Duration(ldapReadTimeout/1000) * time.Second

	loadFuncs := []loadFunc{
		{"lc", cm.LoadLocalConfig},  // Thread name matches Python
		{"gc", cm.LoadGlobalConfig}, // Thread name matches Python
		{"mc", cm.LoadMiscConfig},   // Thread name matches Python
		{"sc", cm.LoadServerConfig}, // Thread name matches Python
	}

	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			logger.DebugContext(ctx, "Retry attempt for config loading",
				"attempt", attempt,
				"max_retries", maxRetries)
			time.Sleep(2 * time.Second)
		}

		timedOut, errors, ctxErr := runLoadAttempt(ctx, loadFuncs, threadWaitTime)
		if ctxErr != nil {
			return ctxErr
		}

		if timedOut && attempt < maxRetries {
			lastErr = fmt.Errorf("timeout on attempt %d", attempt)
			continue
		}

		if len(errors) > 0 && attempt < maxRetries {
			lastErr = errors[0]
			continue
		}

		if len(errors) == 0 && !timedOut {
			logger.DebugContext(ctx, "All configs fetched")
			return nil
		}

		if len(errors) > 0 {
			lastErr = errors[0]
		} else {
			lastErr = fmt.Errorf("configuration loading timed out after %v (with retry)", threadWaitTime*2)
		}

		break
	}

	return lastErr
}

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

// loadConfigWithCache is a generic helper that fetches a typed config value,
// optionally going through the ConfigCache when available.
func loadConfigWithCache[T any](
	ctx context.Context, c *cache.ConfigCache, key string, fetch func() (any, error),
) (T, error) {
	var zero T

	if c == nil {
		result, err := fetch()
		if err != nil {
			return zero, err
		}

		typed, ok := result.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected type from fetch for key %s", key)
		}

		return typed, nil
	}

	result, err := c.GetCachedConfig(ctx, key, fetch)
	if err != nil {
		return zero, err
	}

	typed, ok := result.(T)
	if !ok {
		return zero, fmt.Errorf("unexpected type from cache for key %s", key)
	}

	return typed, nil
}

// processCommonSSLConfig applies the standard SSL post-processing (sort + XML)
// to the three well-known SSL keys shared by global and server config.
func processCommonSSLConfig(configData map[string]string) {
	processSortedSSLConfigForTarget(configData, "zimbraMailboxdSSLProtocols")
	processSortedSSLConfigForTarget(configData, "zimbraSSLExcludeCipherSuites")
	processSortedSSLConfigForTarget(configData, "zimbraSSLIncludeCipherSuites")
}

// processSortedSSLConfigForTarget is a generic helper that sorts SSL-related config values
// and creates XML versions for either global or server config.
func processSortedSSLConfigForTarget(configData map[string]string, key string) {
	if value, ok := configData[key]; ok && value != "" {
		// Sort the space-separated values (case-insensitive)
		values := strings.Fields(value)
		sorted := make([]string, len(values))
		copy(sorted, values)
		// Sort case-insensitively
		slices.SortFunc(sorted, func(a, b string) int {
			return cmp.Compare(strings.ToLower(a), strings.ToLower(b))
		})

		configData[key] = strings.Join(sorted, " ")

		// Create XML version
		xmlKey := key + "XML"

		xmlItems := make([]string, len(sorted))
		for i, val := range sorted {
			xmlItems[i] = fmt.Sprintf("<Item>%s</Item>", val)
		}

		configData[xmlKey] = strings.Join(xmlItems, "\n")
	}
}

// LoadMiscConfig loads miscellaneous configuration by executing misc commands.
// Commands are executed in parallel to improve performance.
func (cm *ConfigManager) LoadMiscConfig(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "configmgr")

	span := tracing.StartSpan("LoadMiscConfig")
	defer tracing.EndSpan(span)

	t1 := time.Now()

	// List of misc commands to execute (matches Python's miscCommands list)
	miscCommands := []string{"garpu", "garpb", "gamau"}

	// Initialize the data map
	cm.State.MiscConfig.Data = make(map[string]string)

	// Use WaitGroup for parallel execution
	var wg sync.WaitGroup
	// Use mutex to protect concurrent map writes
	var mu sync.Mutex
	// Channel to collect errors (optional, for monitoring)
	type cmdResult struct {
		cmdName string
		output  string
		err     error
	}

	resultsChan := make(chan cmdResult, len(miscCommands))

	// Execute all commands in parallel
	for _, cmdName := range miscCommands {
		wg.Go(func() {
			var (
				output string
				err    error
			)

			// If cache is available, use it
			if cm.Cache != nil {
				cacheKey := fmt.Sprintf("ldap:misc:%s", cmdName)

				cachedData, cacheErr := cm.Cache.GetCachedConfig(ctx, cacheKey, func() (any, error) {
					// Fetch function - only runs on cache miss
					return cm.fetchMiscCommand(ctx, cmdName)
				})
				if cacheErr != nil {
					// Non-fatal error - log and report
					logger.WarnContext(ctx, "Failed to load misc command",
						"command", cmdName,
						"error", cacheErr)

					resultsChan <- cmdResult{cmdName: cmdName, output: "", err: cacheErr}

					return
				}

				// Type assert to string
				var ok bool

				output, ok = cachedData.(string)
				if !ok || output == "" {
					resultsChan <- cmdResult{cmdName: cmdName, output: "", err: nil}
					return
				}
			} else {
				// No cache - fetch directly (test environment)
				output, err = cm.fetchMiscCommand(ctx, cmdName)
				if err != nil || output == "" {
					resultsChan <- cmdResult{cmdName: cmdName, output: "", err: err}
					return
				}
			}

			// Store the output using the command name as key
			cmdObj := commands.Commands[cmdName]
			if cmdObj == nil {
				logger.WarnContext(ctx, "Command object not available for storing result",
					"command", cmdName)

				resultsChan <- cmdResult{cmdName: cmdName, output: "", err: fmt.Errorf("command not available")}

				return
			}

			mu.Lock()

			cm.State.MiscConfig.Data[cmdObj.Name] = output

			mu.Unlock()

			logger.DebugContext(ctx, "Stored misc command output",
				"command_name", cmdObj.Name,
				"output", output)

			resultsChan <- cmdResult{cmdName: cmdName, output: output, err: nil}
		})
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(resultsChan)

	// Log results summary
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

// parseLDAPCommandOutput parses the output of zmprov commands (key: value format)
// Handles multi-value attributes by concatenating with newlines.
// Handles base64-encoded values (key:: base64value) by preserving the base64 string.
func parseLDAPCommandOutput(output string) map[string]string {
	configData := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// LDAP output format: "key: value" or "key:: base64value"
		// Check for base64-encoded value (double colon)
		if strings.Contains(line, "::") {
			parts := strings.SplitN(line, "::", 2)
			if len(parts) == 2 {
				key := intern.Attr(strings.TrimSpace(parts[0]))
				value := strings.TrimSpace(parts[1])
				// Store base64-encoded value as-is (will be decoded by executor)
				configData[key] = value

				continue
			}
			// Malformed base64 line - skip silently
			continue
		}

		// Regular format: "key: value"
		// SplitN with limit 2 preserves additional colons in the value
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := intern.Attr(strings.TrimSpace(parts[0]))
			value := strings.TrimSpace(parts[1])
			// Handle multi-value attributes by concatenating with newlines
			if existingValue, exists := configData[key]; exists {
				configData[key] = existingValue + "\n" + value
			} else {
				configData[key] = value
			}
		}
		// Malformed lines without colon are silently skipped
	}

	return configData
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
