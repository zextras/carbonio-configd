// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package configmgr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/fileutil"
	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/mtaops"
	"github.com/zextras/carbonio-configd/internal/proxy"
	"github.com/zextras/carbonio-configd/internal/state"
)

const (
	serviceEnabled  = "enabled"
	serviceDisabled = "disabled"
)

// CompileActions compiles the MTA configuration actions.
func (cm *ConfigManager) CompileActions(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "configmgr")
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

		// Check if section needs processing (forced, requested, or changed)
		processSection := false

		if len(forcedConfig) > 0 {
			if _, ok := forcedConfig[sn]; ok {
				processSection = true

				logger.DebugContext(ctx, "Processing forced config for section",
					"section", sn)
			}
		}

		if len(requestedConfigs) > 0 {
			if _, ok := requestedConfigs[sn]; ok {
				processSection = true

				logger.DebugContext(ctx, "Processing requested config for section",
					"section", sn)
			}
		}

		if section.Changed || firstRun {
			processSection = true
		}

		if !processSection {
			logger.DebugContext(ctx, "Section did not change, skipping action compilation",
				"section", sn)

			continue
		}

		cm.compileSectionActions(ctx, sn, section, requestedConfigs, forcedConfig, firstRun, serviceConfig)
	}

	logger.DebugContext(ctx, "Action compilation complete")
}

// compileSectionActions compiles actions for a changed section
//
//nolint:gocyclo,cyclop // Section action compilation requires multiple checks and accumulations
func (cm *ConfigManager) compileSectionActions(
	ctx context.Context, sn string, section *config.MtaConfigSection,
	requestedConfigs, forcedConfig map[string]string, firstRun bool, serviceConfig map[string]string) {
	logger.DebugContext(ctx, "Section changed, compiling rewrites",
		"section", sn)

	// Log the files that will be rewritten for this service
	if len(section.Rewrites) > 0 {
		logger.DebugContext(ctx, "Service queuing files for rewrite",
			"service", sn,
			"file_count", len(section.Rewrites))

		for rewriteKey, rewriteEntry := range section.Rewrites {
			logger.DebugContext(ctx, "Queuing file rewrite",
				"source", rewriteKey,
				"target", rewriteEntry.Value)
			cm.State.CurRewrites(ctx, rewriteKey, &rewriteEntry)
		}
	}

	logger.DebugContext(ctx, "Section changed, compiling postconf",
		"section", sn)

	for postconfKey, postconfVal := range section.Postconf {
		cm.State.CurPostconf(ctx, postconfKey, postconfVal)
	}

	logger.DebugContext(ctx, "Section changed, compiling postconfd",
		"section", sn)

	for postconfdKey, postconfdVal := range section.Postconfd {
		cm.State.CurPostconfd(ctx, postconfdKey, postconfdVal)
	}

	if section.Proxygen {
		logger.DebugContext(ctx, "Section has PROXYGEN directive, compiling proxygen",
			"section", sn)
		cm.State.Proxygen(true)
	}

	logger.DebugContext(ctx, "Section changed, compiling ldap",
		"section", sn)

	for ldapKey, ldapVal := range section.Ldap {
		cm.State.CurLdap(ctx, ldapKey, ldapVal)
	}

	// Process conditionals
	logger.DebugContext(ctx, "Section changed, compiling conditionals",
		"section", sn)
	cm.processConditionals(ctx, section.Conditionals)

	// Restarts are not triggered on forced or first run (mirroring Jython)
	if firstRun || len(forcedConfig) > 0 || len(requestedConfigs) > 0 {
		return
	}

	logger.DebugContext(ctx, "Section changed, compiling restarts",
		"section", sn)

	for restartService := range section.Restarts {
		isServiceEnabled, err := cm.LookUpConfig(ctx, "SERVICE", restartService)
		if err == nil && state.IsTrueValue(isServiceEnabled) {
			logger.DebugContext(ctx, "Adding restart",
				"service", restartService)
			cm.State.CurRestarts(restartService, -1) // -1 for restart

			continue
		}

		// Special handling for opendkim and archiving from Jython
		switch {
		case restartService == "archiving" && !state.IsTrueValue(serviceConfig[restartService]):
			logger.DebugContext(ctx, "Service not enabled, skipping stop",
				"service", restartService)
		case restartService == "opendkim" && state.IsTrueValue(serviceConfig["mta"]):
			logger.DebugContext(ctx, "Adding restart opendkim")
			cm.State.CurRestarts(restartService, -1)
		default:
			logger.DebugContext(ctx, "Adding stop",
				"service", restartService)
			cm.State.CurRestarts(restartService, 0) // 0 for stop
		}
	}
}

// DoConfigRewrites executes configuration rewrites, postconf, postconfd, and LDAP changes.
func (cm *ConfigManager) DoConfigRewrites(ctx context.Context) error {
	ctx = logger.ContextWithComponent(ctx, "configmgr")
	logger.DebugContext(ctx, "Executing config rewrites")

	var wg sync.WaitGroup

	errChan := make(chan error, 6) // Buffer matches goroutine count to prevent blocking

	// Proxygen takes longest, do it first
	wg.Go(func() {
		if cm.State.CurrentActions.Proxygen {
			logger.DebugContext(ctx, "Running proxygen")
			// Use the new method that passes loaded configs
			if err := cm.RunProxygenWithConfigs(ctx); err != nil {
				errChan <- fmt.Errorf("proxygen failed: %w", err)
			} else {
				logger.DebugContext(ctx, "Proxygen executed successfully")
				cm.State.Proxygen(false)
			}
		}
	})

	wg.Go(func() {
		cm.doRewrites(ctx)
	})

	wg.Go(func() {
		cm.doPostconf(ctx)
	})

	wg.Go(func() {
		cm.doPostconfd(ctx)
	})

	wg.Go(func() {
		cm.doLdap(ctx)
	})

	wg.Go(func() {
		cm.doMapfile(ctx)
	})

	wg.Wait()
	close(errChan)

	// Collect all errors from concurrent goroutines
	var errs []error

	for err := range errChan {
		logger.ErrorContext(ctx, "Error during config rewrite",
			"error", err)
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	logger.DebugContext(ctx, "Config rewrites complete")

	return nil
}

func (cm *ConfigManager) doRewrites(ctx context.Context) {
	if len(cm.State.CurrentActions.Rewrites) == 0 {
		return
	}

	// Snapshot rewrites to avoid concurrent map read/write with DelRewrite
	// called from processRewrite goroutines.
	rewrites := make(map[string]config.RewriteEntry, len(cm.State.CurrentActions.Rewrites))
	maps.Copy(rewrites, cm.State.CurrentActions.Rewrites)

	startTime := time.Now()
	totalFiles := len(rewrites)
	logger.DebugContext(ctx, "Starting configuration file rewrites",
		"total_files", totalFiles)

	// Use a semaphore to limit concurrent file operations
	// This prevents overwhelming the disk I/O system
	maxConcurrent := 4 // Tuned for balance between parallelism and I/O contention
	semaphore := make(chan struct{}, maxConcurrent)

	var wg sync.WaitGroup

	fileCount := 0

	var mu sync.Mutex // Protect fileCount for logging

	for filePath, rewriteEntry := range rewrites {
		// Check for cancellation before starting new goroutine
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "File rewrites cancelled by shutdown signal")
			wg.Wait() // Wait for ongoing rewrites to complete

			return
		default:
		}

		wg.Add(1)

		semaphore <- struct{}{} // Acquire semaphore slot

		// Increment file count under mutex
		mu.Lock()

		fileCount++
		currentFileNum := fileCount

		mu.Unlock()

		// Process file in parallel goroutine
		go func(fp string, re config.RewriteEntry, fileNum int) {
			defer wg.Done()
			defer func() { <-semaphore }() // Release semaphore slot

			var fileStartTime time.Time
			if logger.IsDebug(ctx) {
				fileStartTime = time.Now()
				logger.DebugContext(ctx, "Rewriting file",
					"file_number", fileNum,
					"total_files", totalFiles,
					"source", fp,
					"target", re.Value)
			}

			cm.processRewrite(ctx, fp, re)

			if logger.IsDebug(ctx) {
				elapsed := time.Since(fileStartTime)
				logger.DebugContext(ctx, "Completed file rewrite",
					"file_number", fileNum,
					"total_files", totalFiles,
					"target", re.Value,
					"duration_seconds", elapsed.Seconds())
			}
		}(filePath, rewriteEntry, currentFileNum)
	}

	// Wait for all rewrites to complete
	wg.Wait()

	totalElapsed := time.Since(startTime)
	logger.DebugContext(ctx, "All configuration file rewrites completed",
		"duration_seconds", totalElapsed.Seconds())
}

// cleanupRewriteFiles cleans up temporary and source files
func cleanupRewriteFiles(ctx context.Context, srcFile, tmpFile *os.File, tmpFileName string) {
	if srcFile != nil {
		if err := srcFile.Close(); err != nil && !isAlreadyClosedError(err) {
			logger.ErrorContext(ctx, "Error closing source file",
				"error", err)
		}
	}

	if tmpFile != nil {
		if err := tmpFile.Close(); err != nil && !isAlreadyClosedError(err) {
			logger.ErrorContext(ctx, "Error closing temporary file",
				"error", err)
		}
	}

	if tmpFileName != "" {
		if _, err := os.Stat(tmpFileName); err == nil {
			if err := os.Remove(tmpFileName); err != nil {
				logger.WarnContext(ctx, "Failed to remove temporary file",
					"file", tmpFileName,
					"error", err)
			}
		}
	}
}

// isAlreadyClosedError checks if an error is due to an already closed file
func isAlreadyClosedError(err error) bool {
	return err != nil && (err.Error() == "file already closed" || strings.Contains(err.Error(), "already closed"))
}

// processRewrite processes a single file rewrite
//
//nolint:gocyclo,cyclop // File rewrite requires multiple error checks and file operations
func (cm *ConfigManager) processRewrite(ctx context.Context, filePath string, rewriteEntry config.RewriteEntry) {
	srcPath := cm.mainConfig.BaseDir + "/" + filePath
	destPath := cm.mainConfig.BaseDir + "/" + rewriteEntry.Value

	tmpFile, err := os.CreateTemp("", "zmconfigd-rewrite-")
	if err != nil {
		logger.ErrorContext(ctx, "Failed to create temporary file for rewrite",
			"error", err)

		return
	}

	tmpFileName := tmpFile.Name()

	var srcFile *os.File

	defer cleanupRewriteFiles(ctx, srcFile, tmpFile, tmpFileName)

	//nolint:gosec // G304: File path comes from trusted configuration
	srcFile, err = os.Open(srcPath)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to open source file for rewrite",
			"source_path", srcPath,
			"error", err)

		return
	}

	lineCount := 0

	scanner := bufio.NewScanner(srcFile)
	for scanner.Scan() {
		lineCount++
		line := scanner.Text()
		transformedLine := cm.Transformer.Transform(ctx, line)

		if _, err := tmpFile.WriteString(transformedLine + "\n"); err != nil {
			logger.ErrorContext(ctx, "Failed to write to temporary file during rewrite",
				"error", err)

			return
		}
	}

	if err := scanner.Err(); err != nil {
		logger.ErrorContext(ctx, "Error reading source file during rewrite",
			"source_path", srcPath,
			"error", err)

		return
	}

	// Close files explicitly to release handles before chmod/rename operations
	if err := srcFile.Close(); err != nil {
		logger.ErrorContext(ctx, "Error closing source file",
			"error", err)
	}

	if err := tmpFile.Close(); err != nil {
		logger.ErrorContext(ctx, "Error closing temporary file",
			"error", err)
	}

	// Parse mode string to os.FileMode, default to 0644 if not specified
	var fileMode os.FileMode
	if rewriteEntry.Mode == "" {
		fileMode = 0o644 // Default mode for config files
	} else {
		modeInt, err := strconv.ParseInt(rewriteEntry.Mode, 8, 32)
		if err != nil {
			logger.ErrorContext(ctx, "Invalid file mode for rewrite",
				"mode", rewriteEntry.Mode,
				"error", err)

			return
		}
		//nolint:gosec // G115: modeInt is validated by ParseInt with base 8 and 32-bit size
		fileMode = os.FileMode(modeInt)
	}

	// Set permissions on the temporary file
	err = os.Chmod(tmpFileName, fileMode)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to set permissions on temporary file",
			"file", tmpFileName,
			"error", err)

		return
	}

	// Atomically replace the destination file
	// Try rename first (fast if same filesystem)
	if err := os.Rename(tmpFileName, destPath); err != nil {
		// If rename fails (e.g., cross-device link), fall back to copy+delete
		logger.DebugContext(ctx, "Rename failed, falling back to copy",
			"dest_path", destPath,
			"error", err)

		if err := fileutil.CopyFile(ctx, tmpFileName, destPath); err != nil {
			logger.ErrorContext(ctx, "Failed to copy temporary file to destination",
				"temp_file", tmpFileName,
				"dest_path", destPath,
				"error", err)

			return
		}
		// Set permissions on the copied file
		if err := os.Chmod(destPath, fileMode); err != nil {
			logger.ErrorContext(ctx, "Failed to set permissions on copied file",
				"dest_path", destPath,
				"error", err)

			return
		}
		// Clean up temp file
		if err := os.Remove(tmpFileName); err != nil {
			logger.WarnContext(ctx, "Failed to remove temp file",
				"temp_file", tmpFileName,
				"error", err)
		}
	}

	modeStr := rewriteEntry.Mode
	if modeStr == "" {
		modeStr = "0644"
	}

	logger.DebugContext(ctx, "File rewrite completed",
		"dest_path", destPath,
		"mode", modeStr,
		"lines_processed", lineCount)
	cm.State.DelRewrite(filePath)
}

// resolveValueSpec parses a value specification and resolves it to a concrete value.
// It handles LITERAL values directly and delegates other types to the MTA resolver.
func (cm *ConfigManager) resolveValueSpec(ctx context.Context, key, valueSpec string) (string, error) {
	valueType, valueKey := parseValueSpec(valueSpec)

	if valueType == configTypeLITERAL {
		return valueKey, nil
	}

	resolvedValue, err := cm.mtaResolver.ResolveValue(ctx, valueType, valueKey, cm.State)
	if err != nil {
		return "", fmt.Errorf("failed to resolve value for key %s: %w", key, err)
	}

	return resolvedValue, nil
}

// parseValueSpec parses a valueSpec string and returns the value type and key.
// Parser stores formats like: "VAR:key", "LOCAL:key", "MAPLOCAL:key", "FILE /path", or literal values.
func parseValueSpec(valueSpec string) (valueType, valueKey string) {
	switch {
	case strings.Contains(valueSpec, ":"):
		// Check for colon-separated type:key format (VAR:, LOCAL:, MAPLOCAL:)
		before, after, _ := strings.Cut(valueSpec, ":")

		prefix := before
		switch prefix {
		case configTypeVAR, configTypeLOCAL, configTypeMAPLOCAL:
			valueType = prefix
			valueKey = after
		default:
			// Not a recognized type prefix, treat as literal
			valueType = configTypeLITERAL
			valueKey = valueSpec
		}
	case strings.HasPrefix(valueSpec, configTypeFILE+" "):
		// FILE is space-separated: "FILE /path/to/file"
		valueType = configTypeFILE
		valueKey = strings.TrimPrefix(valueSpec, configTypeFILE+" ")
	case valueSpec == "":
		// Empty value
		valueType = configTypeLITERAL
		valueKey = ""
	default:
		// Literal value
		valueType = configTypeLITERAL
		valueKey = valueSpec
	}

	return valueType, valueKey
}

func (cm *ConfigManager) doPostconf(ctx context.Context) {
	if len(cm.State.CurrentActions.Postconf) == 0 {
		return
	}

	logger.DebugContext(ctx, "Executing postconf commands")

	// Collect all operations first for batch execution
	ops := make([]mtaops.PostconfOperation, 0, len(cm.State.CurrentActions.Postconf))

	for key, valueSpec := range cm.State.CurrentActions.Postconf {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Postconf operations cancelled by shutdown signal")

			return
		default:
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, key, valueSpec)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve postconf value",
				"key", key,
				"error", err)

			continue
		}

		// Add to batch
		ops = append(ops, mtaops.PostconfOperation{
			Key:   key,
			Value: resolvedValue,
		})
	}

	// Execute all operations in a single batch
	if len(ops) > 0 {
		if err := cm.mtaExecutor.ExecutePostconfBatch(ctx, ops); err != nil {
			logger.ErrorContext(ctx, "Failed to execute postconf batch",
				"error", err)
		} else {
			logger.DebugContext(ctx, "Successfully executed postconf batch",
				"operation_count", len(ops))
		}
	}

	cm.State.ClearPostconf()
}

func (cm *ConfigManager) doPostconfd(ctx context.Context) {
	if len(cm.State.CurrentActions.Postconfd) == 0 {
		return
	}

	logger.DebugContext(ctx, "Executing postconfd commands")

	// Collect all postconfd operations for batch execution
	ops := make([]mtaops.PostconfdOperation, 0, len(cm.State.CurrentActions.Postconfd))

	for key := range cm.State.CurrentActions.Postconfd {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Postconfd operations cancelled by shutdown signal")

			return
		default:
		}

		ops = append(ops, mtaops.PostconfdOperation{
			Key: key,
		})
	}

	// Execute all operations in a single batch
	if len(ops) > 0 {
		if err := cm.mtaExecutor.ExecutePostconfdBatch(ctx, ops); err != nil {
			logger.ErrorContext(ctx, "Failed to execute postconfd batch",
				"error", err)
		} else {
			logger.DebugContext(ctx, "Successfully executed postconfd batch",
				"operation_count", len(ops))
		}
	}

	cm.State.ClearPostconfd()
}

func (cm *ConfigManager) doLdap(ctx context.Context) {
	if len(cm.State.CurrentActions.Ldap) == 0 {
		return
	}

	logger.DebugContext(ctx, "Processing LDAP attributes",
		"attribute_count", len(cm.State.CurrentActions.Ldap))

	// Resolve and execute each LDAP directive
	for key, valueSpec := range cm.State.CurrentActions.Ldap {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "LDAP operations cancelled by shutdown signal")

			return
		default:
		}

		// Resolve the value
		resolvedValue, err := cm.resolveValueSpec(ctx, key, valueSpec)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to resolve LDAP value",
				"key", key,
				"error", err)

			continue
		}

		// Create the operation
		op := mtaops.LdapOperation{
			Key:   key,
			Value: resolvedValue,
		}

		// Execute the operation
		if err := cm.mtaExecutor.ExecuteLdapWrite(ctx, op); err != nil {
			logger.ErrorContext(ctx, "Failed to execute LDAP write",
				"key", key,
				"value", op.Value,
				"error", err)
		} else {
			logger.DebugContext(ctx, "Successfully executed LDAP write",
				"key", key,
				"value", op.Value)
			cm.State.DelLdap(key)
		}
	}

	logger.DebugContext(ctx, "LDAP operations complete")
}

func (cm *ConfigManager) doMapfile(ctx context.Context) {
	// MAPFILE operations are tracked in RequiredVars as type "MAPFILE" or "MAPLOCAL"
	// We need to check for changed MAPFILE/MAPLOCAL variables and write them to files
	logger.DebugContext(ctx, "Checking for MAPFILE operations")

	for _, section := range cm.State.MtaConfig.Sections {
		// Check for cancellation
		select {
		case <-ctx.Done():
			logger.InfoContext(ctx, "Mapfile operations cancelled by shutdown signal")

			return
		default:
		}

		if !section.Changed && !cm.State.FirstRun {
			continue
		}

		for varName, varType := range section.RequiredVars {
			if varType != configTypeMAPFILE && varType != configTypeMAPLOCAL {
				continue
			}

			isLocal := (varType == "MAPLOCAL")

			// Create the operation
			op := mtaops.MapfileOperation{
				Key:     varName,
				IsLocal: isLocal,
			}

			// Execute the operation (fetches from LDAP, decodes, writes file)
			if err := cm.mtaExecutor.ExecuteMapfile(ctx, op); err != nil {
				logger.ErrorContext(ctx, "Failed to execute MAPFILE",
					"var_name", varName,
					"error", err)
			} else {
				logger.DebugContext(ctx, "Successfully executed MAPFILE",
					"var_name", varName)
			}
		}
	}

	logger.DebugContext(ctx, "MAPFILE operations complete")
}

// DoRestarts executes service restarts based on the current state.
// DoRestarts executes service restarts using the ServiceManager with dependency cascading.
func (cm *ConfigManager) DoRestarts(ctx context.Context) {
	ctx = logger.ContextWithComponent(ctx, "configmgr")
	logger.DebugContext(ctx, "Executing service restarts")

	// Transfer State.CurrentActions.Restarts to ServiceManager.RestartQueue
	// This bridges the gap between the state tracking and service control layers
	for service := range cm.State.CurrentActions.Restarts {
		if err := cm.ServiceMgr.AddRestart(ctx, service); err != nil {
			logger.WarnContext(ctx, "Failed to queue restart",
				"service", service,
				"error", err)
		}
	}

	// Create a lookup function that wraps LookUpConfig for SERVICE_* keys
	configLookup := func(key string) string {
		// Extract service name from SERVICE_<name> key format
		// Key is expected in format "SERVICE_MTA", "SERVICE_PROXY", etc.
		if len(key) > 8 && key[:8] == "SERVICE_" {
			serviceName := strings.ToLower(key[8:])

			value, err := cm.LookUpConfig(ctx, "SERVICE", serviceName)
			if err == nil && strings.EqualFold(value, constTRUE) {
				return serviceEnabled
			}
		}

		return serviceDisabled
	}

	// Process all queued restarts with dependency cascading
	if err := cm.ServiceMgr.ProcessRestarts(ctx, configLookup); err != nil {
		logger.ErrorContext(ctx, "Error during service restarts",
			"error", err)
	}

	logger.DebugContext(ctx, "Service restarts complete")
}

// ProcessIsRunning checks if a service is currently running.
func (cm *ConfigManager) ProcessIsRunning(ctx context.Context, service string) bool {
	ctx = logger.ContextWithComponent(ctx, "configmgr")
	// Use the ServiceMgr to check process status
	running, err := cm.ServiceMgr.IsRunning(ctx, service)
	if err != nil {
		logger.WarnContext(ctx, "Error checking if service is running",
			"service", service,
			"error", err)
	}

	return running
}

// ProcessIsNotRunning checks if a service is currently not running.
func (cm *ConfigManager) ProcessIsNotRunning(ctx context.Context, service string) bool {
	ctx = logger.ContextWithComponent(ctx, "configmgr")
	return !cm.ProcessIsRunning(ctx, service)
}

// RunProxygenWithConfigs executes proxy configuration generation with loaded configs.
// This method provides loaded LocalConfig, GlobalConfig, and ServerConfig to the proxy generator,
// allowing it to resolve variables from actual LDAP data.
func (cm *ConfigManager) RunProxygenWithConfigs(ctx context.Context) error {
	ctx = logger.ContextWithComponent(ctx, "configmgr")
	startTime := time.Now()

	logger.DebugContext(ctx, "Running proxygen with loaded configurations")

	// NOTE: DO NOT invalidate LDAP cache here. The configs are already loaded
	// and passed to the proxy generator. Cache invalidation should only happen
	// when SIGHUP/network reload is explicitly requested (see LoadAllConfigsWithRetry).

	// Create proxy generator with loaded configs from state
	initStart := time.Now()
	gen, err := proxy.LoadConfiguration(
		ctx,
		cm.mainConfig,
		cm.State.LocalConfig,
		cm.State.GlobalConfig,
		cm.State.ServerConfig,
		cm.LdapClient,
		cm.Cache)
	initDuration := time.Since(initStart)
	logger.DebugContext(ctx, "Proxy generator initialization completed",
		"duration_seconds", initDuration.Seconds())

	if err != nil {
		return fmt.Errorf("failed to initialize proxy generator: %w", err)
	}

	// Generate all nginx configuration files
	logger.DebugContext(ctx, "Generating nginx proxy configuration files")

	genStart := time.Now()

	if err := gen.GenerateAll(ctx); err != nil {
		return fmt.Errorf("proxy configuration generation failed: %w", err)
	}

	genDuration := time.Since(genStart)

	totalDuration := time.Since(startTime)
	logger.DebugContext(ctx, "RunProxygenWithConfigs timing",
		"init_seconds", initDuration.Seconds(),
		"generation_seconds", genDuration.Seconds(),
		"total_seconds", totalDuration.Seconds())

	logger.DebugContext(ctx, "Proxy configuration generation completed successfully")

	return nil
}
