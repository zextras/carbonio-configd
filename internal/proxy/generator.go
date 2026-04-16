// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy - main proxy configuration generation
package proxy

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/tracing"
)

// DiscoverTemplates scans the template directory and returns a list of template files
// Templates are discovered from:
// 1. conf/nginx/templates/ (standard templates)
// 2. conf/nginx/templates_custom/ (custom overrides, if present)
//
// Custom templates take precedence over standard templates with the same name
func (g *Generator) DiscoverTemplates(ctx context.Context) ([]string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	logger.DebugContext(ctx, "Discovering templates",
		"template_dir", g.TemplateDir)

	standardTemplates, err := discoverTemplatesInDir(g.TemplateDir)
	if err != nil {
		return nil, fmt.Errorf("failed to discover standard templates: %w", err)
	}

	logger.DebugContext(ctx, "Found standard templates",
		"count", len(standardTemplates))

	// Check for custom template directory
	customTemplateDir := filepath.Join(g.ConfDir, "nginx", "templates_custom")
	customTemplates := make(map[string]string)

	if _, err := os.Stat(customTemplateDir); err == nil {
		// Custom directory exists, scan it
		customFiles, err := discoverTemplatesInDir(customTemplateDir)
		if err != nil {
			logger.ErrorContext(ctx, "Failed to scan custom templates",
				"error", err)
		} else {
			logger.DebugContext(ctx, "Found custom templates",
				"count", len(customFiles))

			for _, path := range customFiles {
				basename := filepath.Base(path)
				customTemplates[basename] = path
			}
		}
	}

	// Build final template list: custom templates override standard ones
	templateMap := make(map[string]string)

	for _, path := range standardTemplates {
		basename := filepath.Base(path)
		templateMap[basename] = path
	}

	// Override with custom templates
	for basename, customPath := range customTemplates {
		logger.InfoContext(ctx, "Using custom template",
			"basename", basename)
		templateMap[basename] = customPath
	}

	// Convert map to sorted list for deterministic ordering
	templates := make([]string, 0, len(templateMap))
	for _, path := range templateMap {
		templates = append(templates, path)
	}

	sort.Strings(templates)

	logger.DebugContext(ctx, "Discovered templates",
		"total_count", len(templates))

	return templates, nil
}

// discoverTemplatesInDir scans a directory for .template files
func discoverTemplatesInDir(dir string) ([]string, error) {
	var templates []string

	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return templates, nil
	}

	// Walk directory and find .template files
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only include .template files
		if strings.HasSuffix(path, ".template") {
			templates = append(templates, path)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return templates, nil
}

// GenerateAll generates all nginx configuration files from templates
// This is the main entry point for proxy configuration generation
func (g *Generator) GenerateAll(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	span := tracing.StartSpan("GenerateAllProxyConfigs")
	defer tracing.EndSpan(span)

	startTime := time.Now()

	logger.DebugContext(ctx, "Starting nginx proxy configuration generation")

	// Discover all templates
	discoverStart := time.Now()
	templates, err := g.DiscoverTemplates(ctx)
	discoverDuration := time.Since(discoverStart)
	logger.DebugContext(ctx, "Timing: Template discovery took",
		"duration_seconds", discoverDuration.Seconds())

	if err != nil {
		return fmt.Errorf("failed to discover templates: %w", err)
	}

	if len(templates) == 0 {
		return fmt.Errorf("no templates found in %s", g.TemplateDir)
	}

	// Ensure includes directory exists
	if !g.DryRun {
		if err := g.ensureIncludesDir(ctx); err != nil {
			return fmt.Errorf("failed to create includes directory: %w", err)
		}
	}

	// Create a single template processor for all templates (reuse across templates)
	// This avoids creating 40+ processor instances with duplicate regex compilations
	// Note: Upstream data is prefetched in NewGenerator() before variable resolution
	processorStart := time.Now()
	processor := NewTemplateProcessor(g, g.TemplateDir, g.IncludesDir)
	processorDuration := time.Since(processorStart)
	logger.DebugContext(ctx, "Timing: Processor creation took",
		"duration_seconds", processorDuration.Seconds())

	logger.DebugContext(ctx, "Created reusable template processor",
		"template_count", len(templates))

	// Process each template
	successCount := 0
	errorCount := 0
	totalProcessingTime := time.Duration(0)

	for _, templatePath := range templates {
		templateName := filepath.Base(templatePath)
		logger.DebugContext(ctx, "Processing template",
			"name", templateName)

		// Generate output filename by removing .template suffix
		outputName := strings.TrimSuffix(templateName, ".template")

		templateStart := time.Now()

		if err := g.ProcessTemplateWithProcessor(ctx, processor, templatePath, outputName); err != nil {
			logger.ErrorContext(ctx, "Failed to process template",
				"name", templateName,
				"error", err)

			errorCount++

			continue
		}

		templateDuration := time.Since(templateStart)
		totalProcessingTime += templateDuration
		logger.DebugContext(ctx, "Timing: Template processing time",
			"name", templateName,
			"duration_seconds", templateDuration.Seconds())

		successCount++

		if g.Verbose {
			logger.DebugContext(ctx, "Successfully generated file",
				"name", outputName)
		}
	}

	totalDuration := time.Since(startTime)
	logger.DebugContext(ctx, "Timing: Template generation totals",
		"discovery_seconds", discoverDuration.Seconds(),
		"processor_seconds", processorDuration.Seconds(),
		"processing_seconds", totalProcessingTime.Seconds(),
		"total_seconds", totalDuration.Seconds())

	// Summary
	logger.DebugContext(ctx, "Nginx proxy configuration generation complete",
		"succeeded", successCount,
		"failed", errorCount)

	if errorCount > 0 {
		return fmt.Errorf("failed to process %d templates", errorCount)
	}

	return nil
}

// prefetchOperation executes a single prefetch operation with timing and error logging.
func (g *Generator) prefetchOperation(
	ctx context.Context,
	name string,
	fn func() (any, error),
	errChan chan<- error) {
	t := time.Now()

	logger.DebugContext(ctx, "Prefetching "+name)

	if _, err := fn(); err != nil {
		logger.WarnContext(ctx, "Failed to prefetch "+name,
			"error", err,
			"duration_seconds", time.Since(t).Seconds())

		errChan <- err
	} else {
		logger.DebugContext(ctx, "Prefetched "+name+" successfully",
			"duration_seconds", time.Since(t).Seconds())
	}
}

// PrefetchUpstreamData fetches all upstream LDAP data in parallel to populate the cache.
// This is called before template processing to avoid sequential cache misses during template rendering.
// Expected time savings: 3-4 seconds (parallel execution vs sequential cache misses)
func (g *Generator) PrefetchUpstreamData(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	logger.DebugContext(ctx, "Prefetching upstream LDAP data in parallel")

	t1 := time.Now()

	var wg sync.WaitGroup

	errChan := make(chan error, 3) // 3 query types

	// Prefetch getAllReverseProxyBackends
	wg.Go(func() {
		g.prefetchOperation(ctx, "getAllReverseProxyBackends", func() (any, error) {
			return g.getAllReverseProxyBackends(ctx)
		}, errChan)
	})

	// Prefetch getAllReverseProxyBackendsSSL
	wg.Go(func() {
		g.prefetchOperation(ctx, "getAllReverseProxyBackendsSSL", func() (any, error) {
			return g.getAllReverseProxyBackendsSSL(ctx)
		}, errChan)
	})

	// Prefetch getAllMemcachedServers
	wg.Go(func() {
		g.prefetchOperation(ctx, "getAllMemcachedServers", func() (any, error) {
			return g.getAllMemcachedServers(ctx)
		}, errChan)
	})

	// Wait for all prefetch operations to complete
	wg.Wait()
	close(errChan)

	dt := time.Since(t1)
	logger.DebugContext(ctx, "Prefetch completed",
		"duration_seconds", dt.Seconds())

	// Collect errors (but don't fail - queries will happen on-demand)
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("prefetch completed with %d errors (first: %w)", len(errors), errors[0])
	}

	return nil
}

// ProcessTemplate processes a single template file and writes the output
// This method creates a new processor for each call (for backward compatibility)
func (g *Generator) ProcessTemplate(ctx context.Context, templatePath, outputName string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	logger.DebugContext(ctx, "ProcessTemplate called",
		"template", templatePath,
		"output", outputName)

	// Create template processor
	processor := NewTemplateProcessor(g, g.TemplateDir, g.IncludesDir)

	return g.ProcessTemplateWithProcessor(ctx, processor, templatePath, outputName)
}

// ProcessTemplateWithProcessor processes a single template using an existing processor
// This allows processor reuse across multiple templates for better performance
func (g *Generator) ProcessTemplateWithProcessor(
	ctx context.Context, processor *TemplateProcessor, templatePath, outputName string,
) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	logger.DebugContext(ctx, "Processing template with reused processor",
		"template", templatePath,
		"output", outputName)

	// Load template
	tmpl, err := processor.LoadTemplate(ctx, templatePath)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to load template",
			"template", templatePath,
			"error", err)

		return fmt.Errorf("failed to load template: %w", err)
	}

	logger.DebugContext(ctx, "Template loaded",
		"template", templatePath,
		"line_count", len(tmpl.Lines))

	// Process template
	result, err := processor.ProcessTemplate(ctx, tmpl)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to process template",
			"template", templatePath,
			"error", err)

		return fmt.Errorf("failed to process template: %w", err)
	}

	logger.DebugContext(ctx, "Template processed",
		"template", templatePath,
		"byte_count", len(result))

	// Write output file. The main entry-point config (nginx.conf, rendered from
	// nginx.conf.template) must live in ConfDir — that's where nginx -c points.
	// All other rendered templates go into the IncludesDir.
	outputPath := filepath.Join(g.IncludesDir, outputName)
	if outputName == "nginx.conf" {
		outputPath = filepath.Join(g.ConfDir, outputName)
	}

	logger.DebugContext(ctx, "Writing output file",
		"path", outputPath,
		"byte_count", len(result))

	if err := g.writeFile(ctx, outputPath, []byte(result)); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	logger.DebugContext(ctx, "Successfully wrote file",
		"path", outputPath)

	return nil
}

// ensureIncludesDir creates the includes directory if it doesn't exist
func (g *Generator) ensureIncludesDir(ctx context.Context) error {
	if err := os.MkdirAll(g.IncludesDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	logger.DebugContext(ctx, "Includes directory ready",
		"path", g.IncludesDir)

	return nil
}

// writeFile writes a file atomically with proper permissions
// Uses the pattern: write temp file → chmod → rename
// This ensures no partial updates are visible
func (g *Generator) writeFile(ctx context.Context, path string, content []byte) error {
	if g.DryRun {
		logger.DebugContext(ctx, "DRY-RUN: Would write file",
			"byte_count", len(content),
			"path", path)

		return nil
	}

	// Create temp file in the same directory
	dir := filepath.Dir(path)

	tempFile, err := os.CreateTemp(dir, ".configd-tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	tempPath := tempFile.Name()

	// Clean up temp file on error
	defer func() {
		if tempFile != nil {
			if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
				logger.WarnContext(ctx, "Failed to remove temp file",
					"path", tempPath,
					"error", err)
			}
		}
	}()

	// Write content to temp file
	if _, err := tempFile.Write(content); err != nil {
		if cerr := tempFile.Close(); cerr != nil {
			logger.WarnContext(ctx, "Failed to close temp file",
				"path", tempPath,
				"error", cerr)
		}

		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Close temp file
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set proper permissions (0644)
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomically rename to final location
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Success - don't delete temp file
	tempFile = nil

	logger.DebugContext(ctx, "Wrote file",
		"byte_count", len(content),
		"path", path)

	return nil
}

// CleanIncludesDir removes all generated files from includes directory
// Used when regenerating configuration from scratch
func (g *Generator) CleanIncludesDir(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	if g.DryRun {
		logger.DebugContext(ctx, "DRY-RUN: Would clean includes directory",
			"path", g.IncludesDir)

		return nil
	}

	logger.DebugContext(ctx, "Cleaning includes directory",
		"path", g.IncludesDir)

	// Read directory
	entries, err := os.ReadDir(g.IncludesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist, nothing to clean
		}

		return fmt.Errorf("failed to read includes directory: %w", err)
	}

	// Remove each file
	removedCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(g.IncludesDir, entry.Name())
		if err := os.Remove(path); err != nil {
			logger.ErrorContext(ctx, "Failed to remove file",
				"path", path,
				"error", err)

			continue
		}

		removedCount++

		if g.Verbose {
			logger.DebugContext(ctx, "Removed file",
				"path", path)
		}
	}

	logger.DebugContext(ctx, "Cleaned files from includes directory",
		"removed_count", removedCount)

	return nil
}

// ValidateTemplate validates a template file syntax without generating output
func (g *Generator) ValidateTemplate(ctx context.Context, templatePath string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	// Create template processor
	processor := NewTemplateProcessor(g, g.TemplateDir, g.IncludesDir)

	// Load template
	tmpl, err := processor.LoadTemplate(ctx, templatePath)
	if err != nil {
		return fmt.Errorf("failed to load template: %w", err)
	}

	// Process template (this validates syntax)
	_, err = processor.ProcessTemplate(ctx, tmpl)
	if err != nil {
		return fmt.Errorf("template validation failed: %w", err)
	}

	logger.DebugContext(ctx, "Template validation successful",
		"template", templatePath)

	return nil
}

// ValidateAllTemplates validates all discovered templates
func (g *Generator) ValidateAllTemplates(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	logger.DebugContext(ctx, "Validating all templates")

	templates, err := g.DiscoverTemplates(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover templates: %w", err)
	}

	errorCount := 0

	for _, templatePath := range templates {
		if err := g.ValidateTemplate(ctx, templatePath); err != nil {
			logger.ErrorContext(ctx, "Validation failed",
				"template", filepath.Base(templatePath),
				"error", err)

			errorCount++
		}
	}

	if errorCount > 0 {
		return fmt.Errorf("%d templates failed validation", errorCount)
	}

	logger.DebugContext(ctx, "All templates validated successfully")

	return nil
}

// GetCarboVersion returns the Carbonio version string for banners
// Reads from local config or returns default value
func (g *Generator) GetCarboVersion() string {
	if g.LocalConfig != nil {
		if version, ok := g.LocalConfig.Data["carbonio_version"]; ok && version != "" {
			return version
		}
		// Fallback to zimbraversion
		if version, ok := g.LocalConfig.Data["zimbra_version"]; ok && version != "" {
			return version
		}
	}

	return "24.12.0" // Default Carbonio version
}
