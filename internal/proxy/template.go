// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/logger"
)

// bufPool reuses bytes.Buffer allocations across template processing calls.
var bufPool = sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 8192)) },
}

// SSL variable key constants used in template processing.
const (
	varKeySSLCrt     = "ssl.crt"
	varKeySSLKey     = "ssl.key"
	varKeyOrigSSLCrt = "_orig_ssl.crt"
	varKeyOrigSSLKey = "_orig_ssl.key"
)

// Template represents a parsed nginx configuration template
type Template struct {
	Name    string
	Path    string
	Content string
	Lines   []string
}

// TemplateProcessor processes nginx configuration templates
type TemplateProcessor struct {
	generator             *Generator
	templateDir           string
	outputDir             string
	varPattern            *regexp.Regexp
	explodePattern        *regexp.Regexp
	enablerPattern        *regexp.Regexp
	emptyDirectivePattern *regexp.Regexp
	// debugVars is the set of variable names that trigger extra debug logging in interpolateLine.
	debugVars map[string]struct{}
	// Mock functions for testing
	domainProvider func() []DomainInfo
	serverProvider func(serviceName string) []ServerInfo
}

// NewTemplateProcessor creates a new template processor
func NewTemplateProcessor(gen *Generator, templateDir, outputDir string) *TemplateProcessor {
	return &TemplateProcessor{
		generator:             gen,
		templateDir:           templateDir,
		outputDir:             outputDir,
		varPattern:            regexp.MustCompile(`\$\{([a-zA-Z0-9._:]+)\}`),
		explodePattern:        regexp.MustCompile(`^!\{explode\s+(\w+)\(([^)]*)\)\}`),
		enablerPattern:        regexp.MustCompile(`^(\s*)\$\{([^}]+)\}(.+)$`),
		emptyDirectivePattern: regexp.MustCompile(`^\s+[a-z0-9_]+\s+;`),
		debugVars: map[string]struct{}{
			"mail.imap.enabled":  {},
			"mail.pop3.enabled":  {},
			"mail.imaps.enabled": {},
			"mail.pop3s.enabled": {},
		},
	}
}

// LoadTemplate reads a template file from disk
func (tp *TemplateProcessor) LoadTemplate(ctx context.Context, name string) (*Template, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")
	// If name is an absolute path, use it directly; otherwise join with templateDir
	var path string
	if filepath.IsAbs(name) {
		path = name
	} else {
		path = filepath.Join(tp.templateDir, name)
	}

	//nolint:gosec // G304: File path comes from trusted configuration
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file %s from %s: %w", name, path, err)
	}

	// Convert to string once — strings.Split and the Content field would
	// otherwise each copy the full buffer, doubling peak allocations.
	contentStr := string(content)
	lines := strings.Split(contentStr, "\n")

	logger.DebugContext(ctx, "Loaded template",
		"name", name,
		"line_count", len(lines),
		"byte_count", len(content))

	return &Template{
		Name:    name,
		Path:    path,
		Content: contentStr,
		Lines:   lines,
	}, nil
}

// ProcessTemplate processes a template with variable substitution
func (tp *TemplateProcessor) ProcessTemplate(ctx context.Context, tmpl *Template) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	output := bufPool.Get().(*bytes.Buffer)

	output.Reset()
	defer bufPool.Put(output)

	writer := bufio.NewWriter(output)

	// Check if first line contains explode directive
	if explodeType, explodeArgs, remaining, ok := tp.parseExplodeDirective(tmpl.Lines); ok {
		if err := tp.processExplode(ctx, explodeType, explodeArgs, remaining, writer); err != nil {
			return "", fmt.Errorf("error processing explode directive: %w", err)
		}

		if err := writer.Flush(); err != nil {
			return "", fmt.Errorf("error flushing output: %w", err)
		}

		return output.String(), nil
	}

	// No explode directive - process template normally
	for lineNum, line := range tmpl.Lines {
		// Process variable substitutions
		processed, err := tp.interpolateLine(ctx, line)
		if err != nil {
			return "", fmt.Errorf("error processing line %d: %w", lineNum+1, err)
		}

		// Write processed line
		if _, err := writer.WriteString(processed + "\n"); err != nil {
			return "", fmt.Errorf("error writing output: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return "", fmt.Errorf("error flushing output: %w", err)
	}

	return output.String(), nil
}

// processTemplateLines iterates over template lines, optionally skipping comment lines,
// interpolates each line, and writes the result to writer.
func (tp *TemplateProcessor) processTemplateLines(
	ctx context.Context, lines []string, writer *bufio.Writer, skipComments bool,
) error {
	for _, line := range lines {
		if skipComments && strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		processed, err := tp.interpolateLine(ctx, line)
		if err != nil {
			return fmt.Errorf("error processing exploded template line: %w", err)
		}

		if _, err := writer.WriteString(processed + "\n"); err != nil {
			return fmt.Errorf("error writing exploded output: %w", err)
		}
	}

	return nil
}

// ProcessTemplateFile is a convenience method that loads, processes, and writes a template
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

// ProcessAllTemplates processes all .template files in the template directory
func (tp *TemplateProcessor) ProcessAllTemplates(ctx context.Context) error {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	entries, err := os.ReadDir(tp.templateDir)
	if err != nil {
		return fmt.Errorf("failed to read template directory %s: %w", tp.templateDir, err)
	}

	var processingErrors []error

	successCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if strings.HasSuffix(entry.Name(), ".template") {
			if err := tp.ProcessTemplateFile(ctx, entry.Name()); err != nil {
				processingErrors = append(processingErrors, err)
			} else {
				successCount++
			}
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

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + fmt.Sprintf("\n... (truncated, %d more bytes)", len(s)-maxLen)
}

// formatEnabler formats a boolean value for ENABLER type variables
// Returns "" if true (line enabled), "#" if false (line commented out)
// This matches Java's ProxyConfVar.formatEnabler() behavior
func formatEnabler(value any) string {
	if value == nil {
		return "#"
	}

	switch v := value.(type) {
	case bool:
		if v {
			return ""
		}

		return "#"
	case string:
		// For string enablers, any non-empty value means enabled
		if v != "" {
			return ""
		}

		return "#"
	case int, int64:
		if v != 0 {
			return ""
		}

		return "#"
	default:
		// Unknown type, default to disabled
		return "#"
	}
}

// formatValue converts a value to its string representation
func formatValue(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case int, int64:
		return fmt.Sprintf("%d", v)
	case bool:
		if v {
			return "on"
		}

		return nginxOff
	case []string:
		return strings.Join(v, " ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatTimeValue formats time values for output
// This matches Java ProxyConfGen behavior for regular TIME values:
// Java ProxyConfGen outputs integer time values as milliseconds with "ms" suffix
// Example: 3600000 -> "3600000ms", "10s" -> "10s" (already has unit)
func formatTimeValue(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		// Already formatted with unit (e.g., "10s", "2m")
		return v
	case int:
		// Integer values are milliseconds, add "ms" suffix
		return fmt.Sprintf("%dms", v)
	case int64:
		// Integer values are milliseconds, add "ms" suffix
		return fmt.Sprintf("%dms", v)
	default:
		// Fallback to string representation
		return fmt.Sprintf("%v", v)
	}
}

// formatTimeInSecValue formats time values for TimeInSec type variables
// This matches Java's TimeInSecVarWrapper behavior:
// - LDAP stores values in milliseconds
// - Converts to plain seconds (divides by 1000)
// - Outputs as plain number without unit suffix
// Example: 300000 (ms) -> "300" (seconds)
func formatTimeInSecValue(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		// If it's a string, try to parse as integer milliseconds
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			return fmt.Sprintf("%d", ms/1000)
		}

		return v
	case int:
		// Convert milliseconds to seconds
		return fmt.Sprintf("%d", v/1000)
	case int64:
		// Convert milliseconds to seconds
		return fmt.Sprintf("%d", v/1000)
	default:
		// Fallback to string representation
		return fmt.Sprintf("%v", v)
	}
}
