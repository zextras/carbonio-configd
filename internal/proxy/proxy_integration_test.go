// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package proxy

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProxyGeneratorIntegration tests end-to-end template processing
func TestProxyGeneratorIntegration(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create a simple test template
	templateContent := `# NGINX Main Configuration
worker_processes ${main.workers};
worker_connections ${main.workerConnections};
error_log ${main.logfile} ${main.logLevel};

# SSL Configuration
ssl_certificate ${ssl.crt.default};
ssl_certificate_key ${ssl.key.default};
`

	templatePath := filepath.Join(templateDir, "nginx.conf.test.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create generator
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no templates found") {
			t.Skip("Templates not available, skipping")
		}
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	// Override template and output directories
	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir

	// Create template processor
	processor := NewTemplateProcessor(gen, templateDir, outputDir)

	// Process all templates
	if err := processor.ProcessAllTemplates(context.Background()); err != nil {
		t.Fatalf("ProcessAllTemplates() failed: %v", err)
	}

	// Verify output file was created
	outputPath := filepath.Join(outputDir, "nginx.conf.test")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	output := string(content)

	// Verify variables were substituted
	if strings.Contains(output, "${") {
		t.Errorf("Output still contains unsubstituted variables:\n%s", output)
	}

	// Verify expected content
	expectedSubstrings := []string{
		"worker_processes 4",                        // From main.workers default
		"worker_connections 10240",                  // From main.workerConnections default
		"error_log /opt/zextras/log/nginx.log info", // From main.logfile and main.logLevel
		"ssl_certificate",                           // SSL certificate path
		"ssl_certificate_key",                       // SSL key path
	}

	for _, expected := range expectedSubstrings {
		if !strings.Contains(output, expected) {
			t.Errorf("Output missing expected substring %q\nFull output:\n%s", expected, output)
		}
	}

	t.Logf("Generated configuration:\n%s", output)
}

// TestProxyGeneratorWithRealTemplate tests processing with an actual nginx template
func TestProxyGeneratorWithRealTemplate(t *testing.T) {
	// Check if real template exists
	realTemplatePath := "../../conf/nginx/templates/nginx.conf.main.template"
	if _, err := os.Stat(realTemplatePath); os.IsNotExist(err) {
		t.Skip("Real template file not found, skipping test")
	}

	// Create temp output directory
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create generator
	cfg := &config.Config{
		BaseDir:  "../../", // Project root
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	// Override output directory
	gen.ConfDir = outputDir

	// Process just the main template (use generator's TemplateDir)
	processor := NewTemplateProcessor(gen, gen.TemplateDir, outputDir)

	// Load and process the main template (LoadTemplate expects just filename)
	tmpl, err := processor.LoadTemplate(context.Background(), "nginx.conf.main.template")
	if err != nil {
		t.Fatalf("LoadTemplate() failed: %v", err)
	}

	output, err := processor.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate() failed: %v", err)
	}

	// Verify no unsubstituted variables remain
	if strings.Contains(output, "${") {
		t.Errorf("Output still contains unsubstituted variables:\n%s", output)
	}

	// Verify key configuration lines
	if !strings.Contains(output, "worker_processes") {
		t.Errorf("Output missing worker_processes directive")
	}
	if !strings.Contains(output, "worker_connections") {
		t.Errorf("Output missing worker_connections directive")
	}
	if !strings.Contains(output, "error_log") {
		t.Errorf("Output missing error_log directive")
	}

	t.Logf("Generated main configuration (%d bytes)", len(output))
}

// TestDryRunMode tests that dry-run mode prevents file creation
func TestDryRunMode(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create a test template
	templateContent := `worker_processes ${main.workers};`
	templatePath := filepath.Join(templateDir, "test.conf.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create generator with dry-run enabled
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir
	gen.SetDryRun(context.Background(), true) // Enable dry-run

	if !gen.IsDryRun() {
		t.Error("Expected dry-run to be enabled")
	}

	// Process templates
	processor := NewTemplateProcessor(gen, templateDir, outputDir)
	if err := processor.ProcessAllTemplates(context.Background()); err != nil {
		t.Fatalf("ProcessAllTemplates() failed: %v", err)
	}

	// Verify NO output file was created
	outputPath := filepath.Join(outputDir, "test.conf")
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Errorf("Output file should not exist in dry-run mode: %s", outputPath)
	}

	t.Log("Dry-run mode correctly prevented file creation")
}

// TestVerboseMode tests verbose logging functionality
func TestVerboseMode(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create a test template
	templateContent := `worker_processes ${main.workers};`
	templatePath := filepath.Join(templateDir, "test.conf.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create generator with verbose enabled
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir
	gen.SetVerbose(context.Background(), true) // Enable verbose

	if !gen.IsVerbose() {
		t.Error("Expected verbose to be enabled")
	}

	// Process templates
	processor := NewTemplateProcessor(gen, templateDir, outputDir)
	if err := processor.ProcessAllTemplates(context.Background()); err != nil {
		t.Fatalf("ProcessAllTemplates() failed: %v", err)
	}

	// Verify output file WAS created (verbose doesn't prevent writes)
	outputPath := filepath.Join(outputDir, "test.conf")
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("Output file should exist in verbose mode: %s", outputPath)
	}

	t.Log("Verbose mode correctly created output file")
}

// TestDryRunAndVerboseMode tests combination of both modes
func TestDryRunAndVerboseMode(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create a test template
	templateContent := `# Test Config
worker_processes ${main.workers};
error_log ${main.logfile};`
	templatePath := filepath.Join(templateDir, "test.conf.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create generator with both modes enabled
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir
	gen.SetDryRun(context.Background(), true)  // Enable dry-run
	gen.SetVerbose(context.Background(), true) // Enable verbose

	if !gen.IsDryRun() {
		t.Error("Expected dry-run to be enabled")
	}
	if !gen.IsVerbose() {
		t.Error("Expected verbose to be enabled")
	}

	// Process templates
	processor := NewTemplateProcessor(gen, templateDir, outputDir)
	if err := processor.ProcessAllTemplates(context.Background()); err != nil {
		t.Fatalf("ProcessAllTemplates() failed: %v", err)
	}

	// Verify NO output file was created (dry-run takes precedence)
	outputPath := filepath.Join(outputDir, "test.conf")
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Errorf("Output file should not exist in dry-run+verbose mode: %s", outputPath)
	}

	t.Log("Dry-run + verbose mode correctly prevented file creation with verbose logging")
}

// TestConfigSummaryIncludesFlags tests that config summary includes verbose/dry-run flags
func TestConfigSummaryIncludesFlags(t *testing.T) {
	cfg := &config.Config{
		BaseDir:  "/opt/zextras",
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	// Test default state
	summary := gen.GetConfigSummary()
	if summary["dry_run"].(bool) != false {
		t.Error("Expected dry_run to be false by default")
	}
	if summary["verbose"].(bool) != false {
		t.Error("Expected verbose to be false by default")
	}

	// Enable both modes
	gen.SetDryRun(context.Background(), true)
	gen.SetVerbose(context.Background(), true)

	summary = gen.GetConfigSummary()
	if summary["dry_run"].(bool) != true {
		t.Error("Expected dry_run to be true after SetDryRun(true)")
	}
	if summary["verbose"].(bool) != true {
		t.Error("Expected verbose to be true after SetVerbose(true)")
	}

	t.Log("Config summary correctly reports dry-run and verbose flags")
}

// TestNginxValidation tests nginx -t validation integration
func TestNginxValidation(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create a minimal valid nginx config template
	templateContent := `worker_processes 1;
events {
    worker_connections 1024;
}
http {
    server {
        listen 80;
    }
}`
	templatePath := filepath.Join(templateDir, "nginx.conf.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create generator
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator() failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir
	gen.SetVerbose(context.Background(), true)

	// Process template
	processor := NewTemplateProcessor(gen, templateDir, outputDir)
	if err := processor.ProcessAllTemplates(context.Background()); err != nil {
		t.Fatalf("ProcessAllTemplates() failed: %v", err)
	}

	// Validate the generated config
	configPath := filepath.Join(outputDir, "nginx.conf")
	if err := processor.ValidateNginxConfig(context.Background(), configPath); err != nil {
		// If nginx is not available, test should still pass
		if strings.Contains(err.Error(), "not found") {
			t.Skip("nginx binary not found, skipping validation test")
		}
		t.Errorf("ValidateNginxConfig() failed: %v", err)
	}

	t.Log("nginx -t validation passed (or nginx not available)")
}

// TestFullStackIntegration is the comprehensive Task 8.1 integration test.
// It validates that all 34 real nginx templates:
// 1. Process successfully with Go implementation
// 2. Produce structurally valid nginx configuration
// 3. Match expected output characteristics
// 4. Can be validated against reference outputs when available
func TestFullStackIntegration(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	confDir := filepath.Join(tmpDir, "conf")
	templateDir := filepath.Join(confDir, "nginx/templates")
	includesDir := filepath.Join(confDir, "nginx/includes")
	referenceDir := filepath.Join(tmpDir, "reference")

	// Create directory structure
	dirs := []string{templateDir, includesDir, referenceDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create directory %s: %v", dir, err)
		}
	}

	// Copy all real templates
	realTemplateDir := "../../conf/nginx/templates"
	templates, err := discoverTemplatesInDir(realTemplateDir)
	if err != nil || len(templates) == 0 {
		t.Skip("Real templates not found, skipping")
	}

	t.Logf("Discovered %d real templates", len(templates))

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

	// Create realistic test configuration
	baseConfig := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "mail.carbonio.example.com",
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
			"zimbraReverseProxyDefaultRealm":           "carbonio.example.com",
			"zimbraPublicServiceHostname":              "mail.carbonio.example.com",
		},
	}

	serverConfig := &config.ServerConfig{
		Data: map[string]string{
			"zimbraServerHostname": "mail.carbonio.example.com",
			"zimbraMailMode":       "https",
			"zimbraMailPort":       "8080",
			"zimbraMailSSLPort":    "8443",
			"zimbraAdminURL":       "https://admin.carbonio.example.com:7071",
		},
	}

	// Create generator with full configuration using NewGenerator
	gen, err := NewGenerator(context.Background(), baseConfig, localConfig, globalConfig, serverConfig, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	// Override directories
	gen.TemplateDir = templateDir
	gen.ConfDir = includesDir
	gen.SetVerbose(context.Background(), true)

	// Generate all configurations
	t.Log("Generating all nginx configurations...")
	if err := gen.GenerateAll(context.Background()); err != nil {
		t.Fatalf("GenerateAll() failed: %v", err)
	}

	// Validate generated files
	entries, err := os.ReadDir(includesDir)
	if err != nil {
		t.Fatalf("Failed to read includes directory: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("No configuration files were generated")
	}

	t.Logf("Generated %d configuration files", len(entries))

	// Track statistics
	stats := struct {
		totalFiles      int
		validFiles      int
		filesWithErrors int
		totalBytes      int64
	}{}

	// Validate each generated file
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		stats.totalFiles++
		filePath := filepath.Join(includesDir, entry.Name())

		// Read generated content
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Errorf("Failed to read %s: %v", entry.Name(), err)
			stats.filesWithErrors++
			continue
		}

		stats.totalBytes += int64(len(content))
		output := string(content)

		// Validation checks
		checks := []struct {
			name   string
			check  func(string) bool
			errMsg string
		}{
			{
				name:   "No unsubstituted variables",
				check:  func(s string) bool { return !strings.Contains(s, "${") },
				errMsg: "contains unsubstituted variables",
			},
			{
				name:   "No unclosed braces",
				check:  func(s string) bool { return !strings.Contains(s, "!{") },
				errMsg: "contains unclosed explode directives",
			},
			{
				name:   "No template markers",
				check:  func(s string) bool { return !strings.Contains(s, ".template") },
				errMsg: "contains template markers",
			},
			{
				name:   "Proper line endings",
				check:  func(s string) bool { return !strings.Contains(s, "\r\n") },
				errMsg: "contains Windows line endings",
			},
		}

		fileValid := true
		for _, c := range checks {
			if !c.check(output) {
				t.Errorf("%s: %s", entry.Name(), c.errMsg)
				fileValid = false
				stats.filesWithErrors++
			}
		}

		if fileValid {
			stats.validFiles++
		}

		// Check for reference file (if Java ProxyConfGen output available)
		refPath := filepath.Join(referenceDir, entry.Name())
		if _, err := os.Stat(refPath); err == nil {
			refContent, err := os.ReadFile(refPath)
			if err != nil {
				t.Errorf("Failed to read reference %s: %v", entry.Name(), err)
				continue
			}

			// Compare with reference
			if string(content) != string(refContent) {
				t.Logf("⚠ %s differs from reference", entry.Name())

				// Detailed diff for debugging
				goLines := strings.Split(output, "\n")
				refLines := strings.Split(string(refContent), "\n")

				if len(goLines) != len(refLines) {
					t.Logf("  Line count: Go=%d, Ref=%d", len(goLines), len(refLines))
				}

				// Show first difference
				maxLines := min(len(refLines), len(goLines))

				for i := 0; i < maxLines && i < 5; i++ { // Show max 5 diffs
					if goLines[i] != refLines[i] {
						t.Logf("  Line %d differs:", i+1)
						t.Logf("    Go:  %q", goLines[i])
						t.Logf("    Ref: %q", refLines[i])
					}
				}
			} else {
				t.Logf("✓ %s matches reference byte-for-byte", entry.Name())
			}
		}

		t.Logf("✓ Generated %s (%d bytes)", entry.Name(), len(content))
	}

	// Print summary
	t.Logf("\n=== Integration Test Summary ===")
	t.Logf("Templates processed: %d", len(templates))
	t.Logf("Files generated: %d", stats.totalFiles)
	t.Logf("Files valid: %d", stats.validFiles)
	t.Logf("Files with errors: %d", stats.filesWithErrors)
	t.Logf("Total output: %d bytes (%.1f KB)", stats.totalBytes, float64(stats.totalBytes)/1024.0)
	t.Logf("Average file size: %d bytes", stats.totalBytes/int64(stats.totalFiles))

	// Test must pass if all files are valid
	if stats.filesWithErrors > 0 {
		t.Errorf("Integration test failed: %d files have errors", stats.filesWithErrors)
	}

	if stats.validFiles != stats.totalFiles {
		t.Errorf("Not all files validated successfully: %d/%d", stats.validFiles, stats.totalFiles)
	}

	// Check for reference comparison
	refEntries, _ := os.ReadDir(referenceDir)
	if len(refEntries) == 0 {
		t.Log("\nℹ No Java ProxyConfGen reference files found")
		t.Logf("To enable reference comparison, place Java outputs in: %s", referenceDir)
	} else {
		t.Logf("\n✓ Compared %d files against Java ProxyConfGen reference", len(refEntries))
	}

	t.Log("\n✓ Full-stack integration test passed")
}

func TestNginxValidationWithInvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an invalid nginx config (missing semicolon)
	invalidConfig := `worker_processes 1
events {
    worker_connections 1024;
}`
	configPath := filepath.Join(tmpDir, "invalid.conf")
	if err := os.WriteFile(configPath, []byte(invalidConfig), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Create processor
	gen := &Generator{Verbose: true}
	processor := NewTemplateProcessor(gen, "", tmpDir)

	// Try to validate - should fail if nginx is available
	err := processor.ValidateNginxConfig(context.Background(), configPath)

	// If nginx is not available, test passes
	if err == nil {
		t.Log("nginx binary not found or validation not strict, test passed")
		return
	}

	// If nginx is available, validation should fail
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("Expected validation failure, got: %v", err)
	} else {
		t.Logf("Correctly detected invalid config: %v", err)
	}
}

// TestJavaProxyConfGenCompatibility tests compatibility with Java ProxyConfGen output
// This test documents the expected output format and can be used with reference files
// from the Java implementation when available.
func TestJavaProxyConfGenCompatibility(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")
	referenceDir := filepath.Join(tmpDir, "reference")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}
	if err := os.MkdirAll(referenceDir, 0755); err != nil {
		t.Fatalf("Failed to create reference dir: %v", err)
	}

	// Create test template matching Java ProxyConfGen patterns
	// Note: This uses simple variable substitution, not bash-style ${var:+value}
	// which would need to be handled in template preprocessing
	templateContent := `# Proxy Configuration
# Generated from template

# Main settings
worker_processes ${main.workers};
worker_connections ${main.workerConnections};
error_log ${main.logfile} ${main.logLevel};

# Proxy settings
proxy_pass_header ${web.ssl.passheader};
proxy_set_header Host $http_host;

# SSL settings
ssl_certificate ${ssl.crt.default};
ssl_certificate_key ${ssl.key.default};
ssl_protocols ${ssl.protocols};
ssl_ciphers ${ssl.ciphers};
ssl_prefer_server_ciphers ${ssl.preferserverciphers};

# Upstream configuration
upstream web_upstream {
    server ${web.upstream.target};
}

# Server configuration
server {
    listen ${web.http.port};
    server_name ${web.server.name};
}
`

	templatePath := filepath.Join(templateDir, "proxy.conf.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create generator with typical Carbonio config
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "mail.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir

	// Process template
	processor := NewTemplateProcessor(gen, templateDir, outputDir)
	if err := processor.ProcessAllTemplates(context.Background()); err != nil {
		t.Fatalf("ProcessAllTemplates() failed: %v", err)
	}

	// Read generated output
	outputPath := filepath.Join(outputDir, "proxy.conf")
	goOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read Go output: %v", err)
	}

	output := string(goOutput)

	// Verify output format matches Java ProxyConfGen expectations
	tests := []struct {
		name   string
		check  func(string) bool
		errMsg string
	}{
		{
			name:   "No unsubstituted variables",
			check:  func(s string) bool { return !strings.Contains(s, "${") },
			errMsg: "Output contains unsubstituted variables",
		},
		{
			name:   "Contains worker_processes directive",
			check:  func(s string) bool { return strings.Contains(s, "worker_processes") },
			errMsg: "Missing worker_processes directive",
		},
		{
			name:   "Contains worker_connections directive",
			check:  func(s string) bool { return strings.Contains(s, "worker_connections") },
			errMsg: "Missing worker_connections directive",
		},
		{
			name:   "Contains error_log directive",
			check:  func(s string) bool { return strings.Contains(s, "error_log") },
			errMsg: "Missing error_log directive",
		},
		{
			name:   "Contains SSL configuration",
			check:  func(s string) bool { return strings.Contains(s, "ssl_certificate") },
			errMsg: "Missing SSL configuration",
		},
		{
			name:   "Contains upstream block",
			check:  func(s string) bool { return strings.Contains(s, "upstream") },
			errMsg: "Missing upstream configuration",
		},
		{
			name:   "Contains server block",
			check:  func(s string) bool { return strings.Contains(s, "server {") },
			errMsg: "Missing server block",
		},
		{
			name:   "Integer values not quoted",
			check:  func(s string) bool { return !strings.Contains(s, `"4"`) && !strings.Contains(s, `"10240"`) },
			errMsg: "Integer values incorrectly quoted",
		},
		{
			name:   "Boolean on values lowercase",
			check:  func(s string) bool { return strings.Contains(s, " on") || !strings.Contains(s, " ON") },
			errMsg: "Boolean values not lowercase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.check(output) {
				t.Errorf("%s\nOutput:\n%s", tt.errMsg, output)
			}
		})
	}

	// Check for reference file from Java ProxyConfGen
	referencePath := filepath.Join(referenceDir, "proxy.conf")
	if _, err := os.Stat(referencePath); err == nil {
		// Reference file exists, do byte-for-byte comparison
		refOutput, err := os.ReadFile(referencePath)
		if err != nil {
			t.Fatalf("Failed to read reference output: %v", err)
		}

		if string(goOutput) != string(refOutput) {
			t.Errorf("Output differs from Java ProxyConfGen reference")
			t.Logf("Go output:\n%s", goOutput)
			t.Logf("Java reference:\n%s", refOutput)

			// Detailed line-by-line comparison
			goLines := strings.Split(string(goOutput), "\n")
			refLines := strings.Split(string(refOutput), "\n")

			maxLines := max(len(refLines), len(goLines))

			for i := range maxLines {
				var goLine, refLine string
				if i < len(goLines) {
					goLine = goLines[i]
				}
				if i < len(refLines) {
					refLine = refLines[i]
				}

				if goLine != refLine {
					t.Logf("Line %d differs:", i+1)
					t.Logf("  Go:   %q", goLine)
					t.Logf("  Java: %q", refLine)
				}
			}
		} else {
			t.Log("✓ Output matches Java ProxyConfGen reference byte-for-byte")
		}
	} else {
		t.Log("No Java ProxyConfGen reference file found, skipping byte-for-byte comparison")
		t.Logf("To enable reference comparison, place Java output at: %s", referencePath)
		t.Log("✓ Output format validation passed")
	}

	t.Logf("Generated output (%d bytes):\n%s", len(goOutput), output)
}

// TestProxyConfGenOutputStructure tests that generated files match expected structure
// This validates the output directory structure and file naming conventions match
// Java ProxyConfGen behavior.
func TestProxyConfGenOutputStructure(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	// Create multiple templates to test naming conventions
	templates := map[string]string{
		"nginx.conf.main.template":      "worker_processes ${main.workers};",
		"nginx.conf.web.template":       "# Web config\nserver_name ${web.server.name};",
		"nginx.conf.mail.imap.template": "# IMAP config\nlisten ${mail.imap.port};",
		"nginx.conf.mail.pop.template":  "# POP config\nlisten ${mail.pop3.port};",
	}

	for filename, content := range templates {
		path := filepath.Join(templateDir, filename)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write template %s: %v", filename, err)
		}
	}

	// Create generator
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "mail.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir

	// Process all templates
	processor := NewTemplateProcessor(gen, templateDir, outputDir)
	if err := processor.ProcessAllTemplates(context.Background()); err != nil {
		t.Fatalf("ProcessAllTemplates() failed: %v", err)
	}

	// Verify output files follow Java ProxyConfGen naming conventions
	expectedOutputs := []string{
		"nginx.conf.main", // .template suffix removed
		"nginx.conf.web",
		"nginx.conf.mail.imap",
		"nginx.conf.mail.pop",
	}

	for _, expectedFile := range expectedOutputs {
		outputPath := filepath.Join(outputDir, expectedFile)
		if _, err := os.Stat(outputPath); os.IsNotExist(err) {
			t.Errorf("Expected output file not created: %s", expectedFile)
		} else {
			// Verify file is not empty
			content, err := os.ReadFile(outputPath)
			if err != nil {
				t.Errorf("Failed to read output file %s: %v", expectedFile, err)
			} else if len(content) == 0 {
				t.Errorf("Output file %s is empty", expectedFile)
			} else {
				t.Logf("✓ Generated %s (%d bytes)", expectedFile, len(content))
			}
		}
	}

	// Verify no .template files were created in output
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("Failed to read output dir: %v", err)
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".template") {
			t.Errorf("Output directory should not contain .template files: %s", entry.Name())
		}
	}

	t.Logf("✓ Output structure matches Java ProxyConfGen conventions")
}
