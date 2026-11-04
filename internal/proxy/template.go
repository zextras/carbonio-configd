// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/logger"
	"github.com/zextras/carbonio-configd/internal/proxy/tmpl"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// bufPool reuses bytes.Buffer allocations across template processing calls.
var bufPool = sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 8192)) },
}

// SSL variable key constants used in template processing.
const (
	varKeySSLCrt     = tmpl.VarSSLCrt
	varKeySSLKey     = tmpl.VarSSLKey
	varKeyOrigSSLCrt = "_orig_ssl.crt"
	varKeyOrigSSLKey = "_orig_ssl.key"
	mailImapEnabled  = "mail.imap.enabled"
	mailImapsEnabled = "mail.imaps.enabled"
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
	generator   *Generator
	fs          sys.FS // reads template files
	templateDir string
	outputDir   string
	// Domain and server sources replacing the LDAP queries (tests, offline rendering).
	domainProvider func() []DomainInfo
	serverProvider func(serviceName string) []ServerInfo
	// hostResolver replaces the DNS lookup of a virtual hostname (offline rendering);
	// nil resolves through the DNS.
	hostResolver func(ctx context.Context, host string) net.IP
}

// NewTemplateProcessor creates a new template processor
func NewTemplateProcessor(gen *Generator, fsys sys.FS, templateDir, outputDir string) *TemplateProcessor {
	return &TemplateProcessor{
		generator:   gen,
		fs:          fsys,
		templateDir: templateDir,
		outputDir:   outputDir,
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
	content, err := tp.fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file %s from %s: %w", name, path, err)
	}

	// Convert to string once — strings.Split and the Content field would
	// otherwise each copy the full buffer, doubling peak allocations.
	contentStr := string(content)
	lines := tmpl.SplitLines(contentStr)

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
func (tp *TemplateProcessor) ProcessTemplate(ctx context.Context, tpl *Template) (string, error) {
	ctx = logger.ContextWithComponentOnce(ctx, "proxy")

	output := bufPool.Get().(*bytes.Buffer)

	output.Reset()
	defer bufPool.Put(output)

	writer := bufio.NewWriter(output)

	// Check if first line contains explode directive
	if explodeType, explodeArgs, remaining, ok := tp.parseExplodeDirective(tpl.Lines); ok {
		if err := tp.processExplode(ctx, explodeType, explodeArgs, remaining, writer); err != nil {
			return "", fmt.Errorf("error processing explode directive: %w", err)
		}

		if err := writer.Flush(); err != nil {
			return "", fmt.Errorf("error flushing output: %w", err)
		}

		return output.String(), nil
	}

	// No explode directive - process template normally
	for _, line := range tpl.Lines {
		// Process variable substitutions and write the processed line
		if _, err := writer.WriteString(tp.interpolateLine(ctx, line) + "\n"); err != nil {
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
		if skipComments && tmpl.IsCommentLine(line) {
			continue
		}

		if _, err := writer.WriteString(tp.interpolateLine(ctx, line) + "\n"); err != nil {
			return fmt.Errorf("error writing exploded output: %w", err)
		}
	}

	return nil
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
