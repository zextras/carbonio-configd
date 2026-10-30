// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
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
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// loadFunc pairs a name with a config-loading function for concurrent execution.
type loadFunc struct {
	name string
	fn   func(context.Context) error
}

// loadResult is the outcome of one loader. Each goroutine writes only its own
// slot, and the caller reads a slot only after that loader's done channel is
// closed, so no result is ever shared with a still-running loader.
type loadResult struct {
	done chan struct{}
	err  error
}

// loadWait is the outcome of waiting for the loaders.
type loadWait int

const (
	loadWaitDone loadWait = iota
	loadWaitTimedOut
	loadWaitCancelled
)

// awaitLoaders waits until all loaders are done, the timeout elapses or ctx is cancelled.
func awaitLoaders(ctx context.Context, clock sys.Clock, done <-chan struct{}, timeout time.Duration) loadWait {
	timer := clock.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return loadWaitCancelled
	case <-done:
		return loadWaitDone
	case <-timer.C():
		return loadWaitTimedOut
	}
}

// logPendingLoaders logs every loader that has not finished yet.
func logPendingLoaders(ctx context.Context, loadFuncs []loadFunc, results []loadResult, msg string, args ...any) {
	for i, lf := range loadFuncs {
		select {
		case <-results[i].done:
		default:
			logger.WarnContext(ctx, msg, append([]any{"thread", lf.name}, args...)...)
		}
	}
}

// runLoadAttempt launches all loadFuncs concurrently and waits with a two-stage
// timeout (legacy jylibs/state.py: join(thread_wait_time), then once more).
// Returns whether the attempt timed out, the errors of the loaders that finished
// (in loadFuncs order) and a non-nil ctxErr when the context is cancelled.
//
// The loaders run on a child context that is cancelled when this function
// returns, so a hung loader is told to give up instead of blocking the caller.
// Results of loaders still running at timeout are discarded.
func runLoadAttempt(
	ctx context.Context,
	clock sys.Clock,
	loadFuncs []loadFunc,
	timeout time.Duration,
) (timedOut bool, errs []error, ctxErr error) {
	loadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup

	results := make([]loadResult, len(loadFuncs))

	for i, lf := range loadFuncs {
		results[i].done = make(chan struct{})

		wg.Go(func() {
			defer close(results[i].done)

			logger.DebugContext(loadCtx, "Starting config load", "thread", lf.name)

			threadStart := time.Now()
			loadErr := lf.fn(loadCtx)

			logger.DebugContext(loadCtx, "Timing: Config load duration",
				"thread", lf.name,
				"duration_seconds", time.Since(threadStart).Seconds())

			if loadErr != nil {
				results[i].err = fmt.Errorf("thread %s failed: %w", lf.name, loadErr)
			}

			logger.DebugContext(loadCtx, "Finished config load", "thread", lf.name)
		})
	}

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	switch awaitLoaders(ctx, clock, done, timeout) {
	case loadWaitCancelled:
		logger.InfoContext(ctx, "Configuration loading cancelled by shutdown signal")

		return false, nil, ctx.Err()
	case loadWaitTimedOut:
		logger.ErrorContext(ctx, "Configuration loading timed out", "timeout", timeout)
		logPendingLoaders(ctx, loadFuncs, results, "Thread still alive, waiting", "wait_seconds", int(timeout.Seconds()))

		switch awaitLoaders(ctx, clock, done, timeout) {
		case loadWaitCancelled:
			logger.InfoContext(ctx, "Configuration loading cancelled during retry wait")

			return false, nil, ctx.Err()
		case loadWaitTimedOut:
			logPendingLoaders(ctx, loadFuncs, results, "Thread still alive, aborting")

			timedOut = true
		case loadWaitDone:
			logger.DebugContext(ctx, "All threads completed after extended wait")
		}
	case loadWaitDone:
		logger.DebugContext(ctx, "All config threads completed")
	}

	for i := range results {
		select {
		case <-results[i].done:
		default:
			continue
		}

		if results[i].err != nil {
			logger.ErrorContext(ctx, "Error during config load", "error", results[i].err)

			errs = append(errs, results[i].err)
		}
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

	cm.State.ClearFileCache(ctx)

	threadWaitTime := parseLDAPReadTimeout(cm.State.Snapshot().LocalMap())

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

			if err := cm.env.Clock.Sleep(ctx, 2*time.Second); err != nil {
				return err
			}
		}

		// Every attempt stages its results; they are published as one
		// snapshot only when all loaders succeeded.
		stage := &stagedLoad{}

		timedOut, errors, ctxErr := runLoadAttempt(withStage(ctx, stage), cm.env.Clock, loadFuncs, threadWaitTime)
		if ctxErr != nil {
			return ctxErr
		}

		shouldRetry, err := evaluateLoadResult(timedOut, errors, attempt, maxRetries, threadWaitTime)
		if !shouldRetry {
			if err == nil {
				cm.State.Publish(stage.snapshot())
				logger.DebugContext(ctx, "All configs fetched")
			}

			return err
		}

		lastErr = err
	}

	return lastErr
}

func parseLDAPReadTimeout(localConfigData map[string]string) time.Duration {
	const (
		thresholdMs      = 60000 // ldap_read_timeout floor (ms) below which the wait stays at the minimum
		minThreadWaitSec = 60    // jylibs/state.py default thread_wait_time
	)

	ldapReadTimeout := thresholdMs

	if s, ok := localConfigData["ldap_read_timeout"]; ok {
		if v, err := strconv.Atoi(s); err == nil {
			ldapReadTimeout = v
		}
	}

	// Mirror jylibs/state.py:286-288: thread_wait_time is 60s unless the
	// configured ldap_read_timeout (in ms) is at least 60000, in which case it
	// scales as ldap_read_timeout/1000. This enforces a 60s floor so a small
	// ldap_read_timeout cannot drop the per-attempt wait below a minute.
	threadWaitSec := minThreadWaitSec
	if ldapReadTimeout >= thresholdMs {
		threadWaitSec = ldapReadTimeout / 1000
	}

	return time.Duration(threadWaitSec) * time.Second
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

// fetchServerAttrs returns the server's LDAP attributes via the registry,
// failing like the former gs command did when none are available.
func (cm *ConfigManager) fetchServerAttrs(ctx context.Context) (map[string]string, error) {
	if cm.CommandRegistry.ServerAttrs == nil {
		return nil, fmt.Errorf("gs command not available (LDAP commands not registered)")
	}

	attrs, err := cm.CommandRegistry.ServerAttrs(ctx, cm.mainConfig.Hostname)
	if err != nil {
		return nil, err
	}

	if len(attrs) == 0 {
		return nil, fmt.Errorf("no data returned from gs")
	}

	return normalizeLDAPAttrs(attrs), nil
}

// fetchGlobalAttrs returns the global LDAP attributes via the registry,
// failing like the former gacf command did when none are available.
func (cm *ConfigManager) fetchGlobalAttrs(ctx context.Context) (map[string]string, error) {
	if cm.CommandRegistry.GlobalAttrs == nil {
		return nil, fmt.Errorf("gacf command not available (LDAP commands not registered)")
	}

	attrs, err := cm.CommandRegistry.GlobalAttrs(ctx)
	if err != nil {
		return nil, err
	}

	if len(attrs) == 0 {
		return nil, fmt.Errorf("no data returned from gacf")
	}

	return normalizeLDAPAttrs(attrs), nil
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

// normalizeLDAPAttrs returns a fresh config map from LDAP attributes
// (multi-valued attributes already joined with newlines). Each value line is
// trimmed of surrounding whitespace, matching the zmprov-text round trip this
// replaces, and attribute names are interned.
func normalizeLDAPAttrs(attrs map[string]string) map[string]string {
	configData := make(map[string]string, len(attrs))

	for key, value := range attrs {
		if !strings.Contains(value, "\n") {
			configData[intern.Attr(strings.TrimSpace(key))] = strings.TrimSpace(value)

			continue
		}

		lines := strings.Split(value, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimSpace(line)
		}

		configData[intern.Attr(strings.TrimSpace(key))] = strings.Join(lines, "\n")
	}

	return configData
}
