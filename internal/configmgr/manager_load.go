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
	"github.com/zextras/carbonio-configd/internal/intern"
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

	threadWaitTime := parseLDAPReadTimeout(cm.State.LocalConfig.Data)

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

		shouldRetry, err := evaluateLoadResult(timedOut, errors, attempt, maxRetries, threadWaitTime)
		if !shouldRetry {
			if err == nil {
				logger.DebugContext(ctx, "All configs fetched")
			}

			return err
		}

		lastErr = err
	}

	return lastErr
}

func parseLDAPReadTimeout(localConfigData map[string]string) time.Duration {
	const defaultTimeoutMs = 60000

	ldapReadTimeout := defaultTimeoutMs

	if s, ok := localConfigData["ldap_read_timeout"]; ok {
		if v, err := strconv.Atoi(s); err == nil {
			ldapReadTimeout = v
		}
	}

	return time.Duration(ldapReadTimeout/1000) * time.Second
}

func evaluateLoadResult(
	timedOut bool, errors []error, attempt, maxRetries int, threadWaitTime time.Duration,
) (shouldRetry bool, err error) {
	if !timedOut && len(errors) == 0 {
		return false, nil
	}

	if attempt < maxRetries {
		if timedOut {
			return true, fmt.Errorf("timeout on attempt %d", attempt)
		}

		return true, errors[0]
	}

	if len(errors) > 0 {
		return false, errors[0]
	}

	return false, fmt.Errorf("configuration loading timed out after %v (with retry)", threadWaitTime*2)
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
