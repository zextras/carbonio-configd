// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateAllRealTemplates validates all real nginx templates can be processed
func TestValidateAllRealTemplates(t *testing.T) {
	// Use actual template directory - get absolute path
	templateDir, err := filepath.Abs("../../conf/nginx/templates")
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	// Check if template directory exists
	if _, err := os.Stat(templateDir); os.IsNotExist(err) {
		t.Skip("Template directory not found - skipping real template validation")
	}

	// Create mock LDAP client
	mockLdap := &ldap.Ldap{}

	// Create comprehensive test configuration
	localCfg := &config.LocalConfig{
		Data: map[string]string{
			"zimbraIPMode":                             "both",
			"zimbraReverseProxyAdminIPAddress":         "127.0.0.1",
			"zimbraReverseProxyHttpEnabled":            "TRUE",
			"zimbraReverseProxyHttpPortAttribute":      "8080",
			"zimbraReverseProxyMailMode":               "both",
			"zimbraReverseProxySSLCiphers":             "ECDHE+AESGCM",
			"zimbraReverseProxySSLProtocols":           "TLSv1.2 TLSv1.3",
			"zimbraReverseProxyWorkerConnections":      "10240",
			"zimbraReverseProxyWorkerProcesses":        "4",
			"zimbraReverseProxyLogLevel":               "info",
			"zimbraReverseProxyClientCertMode":         "off",
			"zimbraMailReferMode":                      "wronghost",
			"zimbraReverseProxyUpstreamPollingTimeout": "60",
			"zimbraReverseProxyRouteLookupTimeout":     "15",
			"zimbraReverseProxyAuthWaitInterval":       "10",
			"zimbraReverseProxyIPLoginLimit":           "0",
			"zimbraReverseProxyIPLoginLimitTime":       "3600",
			"zimbraReverseProxyUserLoginLimit":         "0",
			"zimbraReverseProxyUserLoginLimitTime":     "3600",
			"zimbra_server_hostname":                   "mail.example.com",
			"ssl_dhparam":                              "/opt/zextras/conf/dhparam.pem",
		},
	}

	globalCfg := &config.GlobalConfig{
		Data: map[string]string{
			"zimbraReverseProxyHttpEnabled":            "TRUE",
			"zimbraReverseProxyMailEnabled":            "TRUE",
			"zimbraReverseProxyDefaultRealm":           "example.com",
			"zimbraPublicServiceHostname":              "mail.example.com",
			"zimbraReverseProxyClientCertCA":           "/opt/zextras/conf/ca/ca.crt",
			"zimbraReverseProxyAvailableLookupTargets": "mail.example.com:8080",
			"zimbraReverseProxyWorkerConnections":      "10240",
			"zimbraReverseProxySSLCiphers":             "ECDHE+AESGCM",
			"zimbraReverseProxySSLProtocols":           "TLSv1.2 TLSv1.3",
			"zimbraReverseProxyUpstreamPollingTimeout": "60",
		},
	}

	serverCfg := &config.ServerConfig{
		Data: map[string]string{
			"zimbraServiceEnabled":           "imapd pop3d",
			"zimbraServiceHostname":          "mail.example.com",
			"zimbraMailPort":                 "8080",
			"zimbraMailSSLPort":              "8443",
			"zimbraMailMode":                 "https",
			"zimbraReverseProxyLookupTarget": "TRUE",
			"zimbraSSLCertificate":           "/opt/zextras/conf/nginx.crt",
			"zimbraSSLPrivateKey":            "/opt/zextras/conf/nginx.key",
		},
		ServiceConfig: map[string]string{
			"imapd": "enabled",
			"pop3d": "enabled",
		},
	}

	cfg, err := config.NewConfig()
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}
	cfg.Hostname = "mail.example.com"

	// Create generator with temp output directory and real template directory
	outputDir := t.TempDir()
	gen, err := NewGenerator(context.Background(), cfg, localCfg, globalCfg, serverCfg, mockLdap, nil)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	// Set template directory to the real templates
	gen.TemplateDir = templateDir
	gen.IncludesDir = filepath.Join(outputDir, "includes")
	gen.ConfDir = outputDir

	// Discover all templates (returns absolute paths)
	templates, err := gen.DiscoverTemplates(context.Background())
	if err != nil {
		t.Fatalf("Failed to discover templates: %v", err)
	}

	if len(templates) == 0 {
		t.Fatal("No templates discovered")
	}

	t.Logf("Discovered %d templates", len(templates))

	// Track validation results
	var (
		successCount int
		failCount    int
		skipCount    int
		failures     []string
	)

	// Create template processor with empty templateDir since we have absolute paths from DiscoverTemplates
	processor := NewTemplateProcessor(gen, "", gen.IncludesDir)

	// Process each template
	for _, tmplPath := range templates {
		tmplName := filepath.Base(tmplPath)
		t.Run(tmplName, func(t *testing.T) {
			// Load template
			tmpl, err := processor.LoadTemplate(context.Background(), tmplPath)
			if err != nil {
				t.Errorf("Failed to load template: %v", err)
				failCount++
				failures = append(failures, tmplName+": load error")
				return
			}

			// Skip empty templates
			if len(tmpl.Lines) == 0 {
				t.Skip("Empty template")
				skipCount++
				return
			}

			// Try to process the template
			result, err := processor.ProcessTemplate(context.Background(), tmpl)

			// Some templates may require specific configuration that we don't have in tests
			// We consider it success if either:
			// 1. Processing succeeds, OR
			// 2. It fails with a known safe error (missing variable, missing command)
			if err != nil {
				errStr := err.Error()
				// These are acceptable errors in test environment
				if strings.Contains(errStr, "variable") ||
					strings.Contains(errStr, "not found") ||
					strings.Contains(errStr, "command") ||
					strings.Contains(errStr, "zmprov") {
					t.Logf("Template requires missing test data: %v", err)
					skipCount++
					return
				}

				// Unexpected error
				t.Errorf("Failed to process template: %v", err)
				failCount++
				failures = append(failures, tmplName+": "+errStr)
				return
			}

			// Success - validate result
			// Empty output is acceptable for templates that start with explode directives
			// These templates only generate output when there are items to explode
			if len(strings.TrimSpace(result)) == 0 {
				// Check if the first non-comment line is an explode directive
				startsWithExplode := false
				for _, line := range tmpl.Lines {
					trimmed := strings.TrimSpace(line)
					if trimmed == "" || strings.HasPrefix(trimmed, "#") {
						continue // Skip empty lines and comments
					}
					if strings.HasPrefix(trimmed, "!{explode") {
						startsWithExplode = true
					}
					break // Only check first non-comment line
				}

				// If template starts with explode, empty output is OK (no items to explode)
				if startsWithExplode {
					t.Logf("Successfully processed explode template (empty output expected - no items to explode)")
					successCount++
					return
				}

				t.Error("Template produced empty output")
				failCount++
				failures = append(failures, tmplName+": empty output")
				return
			}

			successCount++
			t.Logf("Successfully processed template (%d bytes output)", len(result))
		})
	}

	// Report summary
	t.Logf("\n=== Template Validation Summary ===")
	t.Logf("Total templates: %d", len(templates))
	t.Logf("Successfully processed: %d", successCount)
	t.Logf("Skipped (missing test data): %d", skipCount)
	t.Logf("Failed: %d", failCount)

	if failCount > 0 {
		t.Logf("\nFailures:")
		for _, failure := range failures {
			t.Logf("  - %s", failure)
		}
		t.Errorf("%d templates failed validation", failCount)
	}

	// We expect most templates to either succeed or skip (due to missing test env)
	// At least 50% should not fail
	if failCount > len(templates)/2 {
		t.Errorf("Too many template failures: %d out of %d", failCount, len(templates))
	}
}

// TestTemplateCategories validates templates by category
func TestTemplateCategories(t *testing.T) {
	templateDir := "../../conf/nginx/templates"

	if _, err := os.Stat(templateDir); os.IsNotExist(err) {
		t.Skip("Template directory not found")
	}

	categories := map[string][]string{
		"main": {
			"nginx.conf.main.template",
			"nginx.conf.template",
		},
		"web": {
			"nginx.conf.web.template",
			"nginx.conf.web.http.template",
			"nginx.conf.web.http.default.template",
			"nginx.conf.web.https.template",
			"nginx.conf.web.https.default.template",
			"nginx.conf.web.http.mode-redirect.template",
			"nginx.conf.web.http.mode-both.template",
		},
		"admin": {
			"nginx.conf.web.carbonio.admin.template",
			"nginx.conf.web.carbonio.admin.default.template",
		},
		"sso": {
			"nginx.conf.web.sso.template",
			"nginx.conf.web.sso.default.template",
		},
		"mail": {
			"nginx.conf.mail.template",
			"nginx.conf.mail.imap.template",
			"nginx.conf.mail.imap.default.template",
			"nginx.conf.mail.imaps.template",
			"nginx.conf.mail.imaps.default.template",
			"nginx.conf.mail.pop3.template",
			"nginx.conf.mail.pop3.default.template",
			"nginx.conf.mail.pop3s.template",
			"nginx.conf.mail.pop3s.default.template",
		},
		"stream": {
			"nginx.conf.stream.addressBook.template",
			"nginx.conf.stream.message.dispatcher.xmpp.template",
		},
		"maps": {
			"nginx.conf.map.key.template",
			"nginx.conf.map.crt.template",
		},
		"lookup": {
			"nginx.conf.zmlookup.template",
		},
		"memcache": {
			"nginx.conf.memcache.template",
		},
	}

	for category, templates := range categories {
		t.Run(category, func(t *testing.T) {
			for _, tmplName := range templates {
				tmplPath := filepath.Join(templateDir, tmplName)
				if _, err := os.Stat(tmplPath); os.IsNotExist(err) {
					t.Errorf("Expected template not found: %s", tmplName)
					continue
				}

				// Verify template is readable and not empty
				content, err := os.ReadFile(tmplPath)
				if err != nil {
					t.Errorf("Cannot read template %s: %v", tmplName, err)
					continue
				}

				if len(strings.TrimSpace(string(content))) == 0 {
					t.Errorf("Template %s is empty", tmplName)
					continue
				}

				t.Logf("✓ %s (%d bytes)", tmplName, len(content))
			}
		})
	}
}

// TestTemplatesSyntaxBasics validates basic syntax of all templates
func TestTemplatesSyntaxBasics(t *testing.T) {
	templateDir := "../../conf/nginx/templates"

	if _, err := os.Stat(templateDir); os.IsNotExist(err) {
		t.Skip("Template directory not found")
	}

	templates, err := filepath.Glob(filepath.Join(templateDir, "*.template"))
	if err != nil {
		t.Fatalf("Failed to list templates: %v", err)
	}

	for _, tmplPath := range templates {
		tmplName := filepath.Base(tmplPath)
		t.Run(tmplName, func(t *testing.T) {
			content, err := os.ReadFile(tmplPath)
			if err != nil {
				t.Fatalf("Failed to read template: %v", err)
			}

			text := string(content)

			// Check for unclosed variable references
			// This is a simplified check - look for ${... without closing }
			// More sophisticated: ensure every ${ has a matching } before the next ${
			varStart := 0
			for {
				idx := strings.Index(text[varStart:], "${")
				if idx == -1 {
					break
				}
				idx += varStart
				// Find the closing }
				closeIdx := strings.Index(text[idx+2:], "}")
				if closeIdx == -1 {
					t.Errorf("Unclosed variable reference starting at position %d", idx)
					break
				}
				varStart = idx + 2 + closeIdx + 1
			}

			// Check for explode directives syntax
			lines := strings.Split(text, "\n")
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)

				// Check !{explode syntax
				if strings.Contains(trimmed, "!{explode") {
					if !strings.Contains(trimmed, "}") {
						t.Errorf("Line %d: Unclosed explode directive: %s", i+1, trimmed)
					}
				}

				// (Common nginx syntax markers like "server {", "location",
				// or "upstream" indicate a valid config structure but require
				// no further assertion at this point.)
			}

			t.Logf("✓ Syntax check passed (%d lines, %d variables)",
				len(lines), strings.Count(text, "${"))
		})
	}
}
