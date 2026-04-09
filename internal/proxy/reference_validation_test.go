// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferenceValidation generates a complete proxy configuration with realistic
// test data and validates it produces structurally correct nginx configuration.
// This serves as a baseline for comparison with the Java ProxyConfGen reference.
func TestReferenceValidation(t *testing.T) {
	// Create temporary directories for test
	tmpDir := t.TempDir()
	confDir := filepath.Join(tmpDir, "conf")
	templateDir := filepath.Join(confDir, "nginx/templates")
	includesDir := filepath.Join(confDir, "nginx/includes")

	// Create directories
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Copy all real templates to test directory
	realTemplateDir := "../../conf/nginx/templates"
	templates, err := discoverTemplatesInDir(realTemplateDir)
	if err != nil || len(templates) == 0 {
		t.Skip("Real templates not available, skipping")
	}

	for _, srcPath := range templates {
		content, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("Failed to read template %s: %v", srcPath, err)
		}

		dstPath := filepath.Join(templateDir, filepath.Base(srcPath))
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			t.Fatalf("Failed to copy template to %s: %v", dstPath, err)
		}
	}

	// Create comprehensive test configuration using real config types
	baseConfig := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "mail.example.com",
	}

	localConfig := &config.LocalConfig{
		Data: map[string]string{
			"zimbraIPMode":               "both",
			"zimbraMailProxyPort":        "80",
			"zimbraMailSSLProxyPort":     "443",
			"zimbraImapBindPort":         "7143",
			"zimbraImapProxyBindPort":    "143",
			"zimbraImapSSLBindPort":      "7993",
			"zimbraImapSSLProxyBindPort": "993",
			"zimbraPop3BindPort":         "7110",
			"zimbraPop3ProxyBindPort":    "110",
			"zimbraPop3SSLBindPort":      "7995",
			"zimbraPop3SSLProxyBindPort": "995",
			"zimbraAdminPort":            "7071",
			"zimbraAdminProxyPort":       "9071",
			"zimbraMemcachedBindPort":    "11211",
		},
	}

	globalConfig := &config.GlobalConfig{
		Data: map[string]string{
			"zimbraReverseProxyHttpEnabled":            "TRUE",
			"zimbraReverseProxyMailMode":               "both",
			"zimbraReverseProxySSLToUpstreamEnabled":   "TRUE",
			"zimbraReverseProxyWorkerProcesses":        "4",
			"zimbraReverseProxyWorkerConnections":      "10240",
			"zimbraReverseProxyLogLevel":               "info",
			"zimbraReverseProxyUpstreamPollingTimeout": "60",
			"zimbraMailProxyReconnect":                 "5000",
			"zimbraReverseProxyDefaultRealm":           "example.com",
		},
	}

	serverConfig := &config.ServerConfig{
		Data: map[string]string{
			"zimbraServerHostname": "mail.example.com",
			"zimbraMailMode":       "https",
			"zimbraMailPort":       "8080",
			"zimbraMailSSLPort":    "8443",
			"zimbraAdminURL":       "https://admin.example.com:7071",
		},
	}

	// Create generator
	gen := &Generator{
		Config:       baseConfig,
		LocalConfig:  localConfig,
		GlobalConfig: globalConfig,
		ServerConfig: serverConfig,
		LdapClient:   nil, // No LDAP needed for basic template generation
		TemplateDir:  templateDir,
		ConfDir:      confDir,
		IncludesDir:  includesDir,
		WorkingDir:   tmpDir,
		Hostname:     "mail.example.com",
		DryRun:       false,
	}

	// Register and resolve variables
	gen.RegisterVariables(context.Background())
	if err := gen.ResolveAllVariables(context.Background()); err != nil {
		t.Fatalf("Failed to resolve variables: %v", err)
	}

	// Generate all configuration files
	if err := gen.GenerateAll(context.Background()); err != nil {
		t.Fatalf("GenerateAll failed: %v", err)
	}

	// Validate generated files exist and are non-empty
	generatedFiles, err := filepath.Glob(filepath.Join(includesDir, "*.conf*"))
	if err != nil {
		t.Fatalf("Failed to list generated files: %v", err)
	}

	if len(generatedFiles) == 0 {
		t.Fatal("No configuration files were generated")
	}

	t.Logf("Generated %d configuration files", len(generatedFiles))

	// Validate each generated file
	for _, file := range generatedFiles {
		basename := filepath.Base(file)
		t.Run(basename, func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("Failed to read %s: %v", basename, err)
			}

			contentStr := string(content)

			// Skip empty files (explode with no items is OK)
			if len(strings.TrimSpace(contentStr)) == 0 {
				t.Logf("File %s is empty (explode with no items)", basename)
				return
			}

			// Basic syntax validation
			validateNginxSyntax(t, basename, contentStr)

			// Content-specific validation
			validateFileContent(t, basename, contentStr)
		})
	}
}

// validateNginxSyntax performs basic nginx syntax validation
func validateNginxSyntax(t *testing.T, filename, content string) {
	// Check for common syntax errors

	// 1. Unclosed braces
	openBraces := strings.Count(content, "{")
	closeBraces := strings.Count(content, "}")
	if openBraces != closeBraces {
		t.Errorf("Unmatched braces in %s: %d open, %d close", filename, openBraces, closeBraces)
	}

	// 2. Unclosed variable references
	if strings.Contains(content, "${") && !strings.Contains(content, "}") {
		t.Errorf("Unclosed variable reference in %s", filename)
	}

	// 3. Unclosed explode directives (should have been processed)
	if strings.Contains(content, "!{explode") {
		t.Errorf("Unprocessed explode directive in %s", filename)
	}

	// 4. Multiple semicolons
	if strings.Contains(content, ";;") {
		t.Errorf("Double semicolon found in %s", filename)
	}

	// 5. Check for proper line endings (no mixed line endings)
	if strings.Contains(content, "\r\n") {
		lines := strings.Split(content, "\n")
		crlfCount := 0
		lfCount := 0
		for _, line := range lines {
			if strings.HasSuffix(line, "\r") {
				crlfCount++
			} else if line != "" {
				lfCount++
			}
		}
		if crlfCount > 0 && lfCount > 0 {
			t.Errorf("Mixed line endings in %s: %d CRLF, %d LF", filename, crlfCount, lfCount)
		}
	}
}

// validateFileContent performs content-specific validation
func validateFileContent(t *testing.T, filename, content string) {
	contentLower := strings.ToLower(content)

	// Validate upstream files contain upstream blocks
	if strings.Contains(filename, "upstream") {
		if !strings.Contains(contentLower, "upstream") {
			t.Errorf("%s should contain 'upstream' directive", filename)
		}
		if !strings.Contains(contentLower, "server") {
			t.Errorf("%s should contain 'server' directive", filename)
		}
	}

	// Validate SSL map files contain map blocks
	if strings.Contains(filename, "ssl") && strings.Contains(filename, "map") {
		if !strings.Contains(contentLower, "map") {
			t.Errorf("%s should contain 'map' directive", filename)
		}
	}

	// Validate web files reference upstream
	if strings.HasPrefix(filename, "nginx.conf.web") {
		if !strings.Contains(contentLower, "proxy_pass") && !strings.Contains(contentLower, "upstream") {
			t.Logf("Warning: %s doesn't contain proxy_pass or upstream reference", filename)
		}
	}

	// Validate mail files reference mail proxy settings
	if strings.Contains(filename, "mail") {
		if !strings.Contains(contentLower, "proxy") && !strings.Contains(contentLower, "upstream") {
			t.Logf("Warning: %s doesn't contain proxy or upstream reference", filename)
		}
	}

	// Validate listen directives have ports
	if strings.Contains(contentLower, "listen") {
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			lineLower := strings.ToLower(line)
			if strings.Contains(lineLower, "listen") {
				// Skip lines that are just variable references (e.g., ${listen.:addresses})
				if strings.HasPrefix(strings.TrimSpace(line), "${") && strings.HasSuffix(strings.TrimSpace(line), "}") {
					continue
				}
				// Validate actual listen directives
				parts := strings.Fields(line)
				if len(parts) < 2 {
					t.Errorf("Line %d: listen directive without address/port: %s", i+1, line)
				}
			}
		}
	}
}

// TestReferenceOutputStructure validates the structure of generated output
func TestReferenceOutputStructure(t *testing.T) {
	// Create temporary directories
	tmpDir := t.TempDir()
	confDir := filepath.Join(tmpDir, "conf")
	templateDir := filepath.Join(confDir, "nginx/templates")
	includesDir := filepath.Join(confDir, "nginx/includes")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Copy real templates
	realTemplateDir := "../../conf/nginx/templates"
	templates, err := discoverTemplatesInDir(realTemplateDir)
	if err != nil || len(templates) == 0 {
		t.Skip("Real templates not available, skipping")
	}

	for _, srcPath := range templates {
		content, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("Failed to read template: %v", err)
		}
		dstPath := filepath.Join(templateDir, filepath.Base(srcPath))
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			t.Fatalf("Failed to write template: %v", err)
		}
	}

	// Minimal config
	baseConfig := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "localhost",
	}

	gen := &Generator{
		Config:       baseConfig,
		LocalConfig:  &config.LocalConfig{Data: map[string]string{}},
		GlobalConfig: &config.GlobalConfig{Data: map[string]string{}},
		ServerConfig: &config.ServerConfig{Data: map[string]string{}},
		LdapClient:   nil,
		TemplateDir:  templateDir,
		ConfDir:      confDir,
		IncludesDir:  includesDir,
		WorkingDir:   tmpDir,
		Hostname:     "localhost",
		DryRun:       false,
	}

	// Register and resolve variables
	gen.RegisterVariables(context.Background())
	if err := gen.ResolveAllVariables(context.Background()); err != nil {
		t.Fatalf("Failed to resolve variables: %v", err)
	}

	// Generate
	if err := gen.GenerateAll(context.Background()); err != nil {
		t.Fatalf("GenerateAll failed: %v", err)
	}

	// Verify includes directory exists
	if _, err := os.Stat(includesDir); os.IsNotExist(err) {
		t.Fatal("Includes directory was not created")
	}

	// Verify includes directory permissions
	info, err := os.Stat(includesDir)
	if err != nil {
		t.Fatalf("Failed to stat includes dir: %v", err)
	}

	mode := info.Mode().Perm()
	if mode != 0755 {
		t.Errorf("Includes dir has wrong permissions: got %o, want 0755", mode)
	}

	// Verify at least some files were generated
	files, err := os.ReadDir(includesDir)
	if err != nil {
		t.Fatalf("Failed to read includes dir: %v", err)
	}

	if len(files) == 0 {
		t.Error("No files generated in includes directory")
	}

	// Verify file permissions
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		fullPath := filepath.Join(includesDir, file.Name())
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Errorf("Failed to stat %s: %v", file.Name(), err)
			continue
		}

		mode := info.Mode().Perm()
		if mode != 0644 {
			t.Errorf("File %s has wrong permissions: got %o, want 0644", file.Name(), mode)
		}
	}

	t.Logf("Successfully generated %d files with correct structure and permissions", len(files))
}
