// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zextras/carbonio-configd/internal/fileutil"
	"github.com/zextras/carbonio-configd/internal/logger"
)

// Test-only helpers. Production code writes template output through
// Generator.ProcessTemplateWithProcessor; these methods give the tests a
// direct way to drive TemplateProcessor and read resolved variables.

// GetVariable retrieves a variable by keyword.
func (g *Generator) GetVariable(keyword string) (*Variable, error) {
	v, ok := g.Variables[keyword]
	if !ok {
		return nil, fmt.Errorf("variable %s not found", keyword)
	}

	return v, nil
}

// truncateString truncates a string to maxLen characters.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + fmt.Sprintf("\n... (truncated, %d more bytes)", len(s)-maxLen)
}

// WriteOutput writes processed template to the output directory.
func (tp *TemplateProcessor) WriteOutput(ctx context.Context, name string, content string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	outputPath := filepath.Join(tp.outputDir, strings.TrimSuffix(name, ".template"))

	dryRun := false
	verbose := false

	if tp.generator != nil {
		dryRun = tp.generator.DryRun
		verbose = tp.generator.Verbose
	}

	if dryRun {
		logger.DebugContext(ctx, "[DRY-RUN] Would write file",
			"path", outputPath,
			"byte_count", len(content))

		if verbose {
			logger.DebugContext(ctx, "[DRY-RUN] Content preview",
				"content", truncateString(content, 500))
		}

		return nil
	}

	if err := os.MkdirAll(tp.outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := fileutil.AtomicWrite(outputPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	if verbose {
		logger.DebugContext(ctx, "Wrote file",
			"path", outputPath,
			"byte_count", len(content))
	}

	return nil
}

// ProcessTemplateFile loads, processes, and writes a single template.
func (tp *TemplateProcessor) ProcessTemplateFile(ctx context.Context, name string) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	tmpl, err := tp.LoadTemplate(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to load template %s: %w", name, err)
	}

	content, err := tp.ProcessTemplate(ctx, tmpl)
	if err != nil {
		return fmt.Errorf("failed to process template %s: %w", name, err)
	}

	if err := tp.WriteOutput(ctx, name, content); err != nil {
		return fmt.Errorf("failed to write output for template %s: %w", name, err)
	}

	return nil
}

// ProcessAllTemplates processes all .template files in the template directory.
func (tp *TemplateProcessor) ProcessAllTemplates(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	entries, err := os.ReadDir(tp.templateDir)
	if err != nil {
		return fmt.Errorf("failed to read template directory %s: %w", tp.templateDir, err)
	}

	var processingErrors []error

	successCount := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".template") {
			continue
		}

		if err := tp.ProcessTemplateFile(ctx, entry.Name()); err != nil {
			processingErrors = append(processingErrors, err)
		} else {
			successCount++
		}
	}

	if len(processingErrors) > 0 {
		return fmt.Errorf("processed %d templates with %d errors: %v",
			successCount, len(processingErrors), processingErrors)
	}

	logger.DebugContext(ctx, "Successfully processed templates",
		"count", successCount)

	return nil
}
