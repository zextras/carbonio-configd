// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/config"
)

// TestRealTemplatesWithExplode tests processing of actual nginx templates
// that use explode directives with mock domain/server data
func TestRealTemplatesWithExplode(t *testing.T) {
	// Define test cases for different template files
	tests := []struct {
		name           string
		templateFile   string
		setupVars      func(*Generator)
		setupDomains   func() []DomainInfo
		expectedChecks []string // strings that should appear in output
	}{
		{
			name:         "web.https with single domain",
			templateFile: "nginx.conf.web.https.template",
			setupVars: func(gen *Generator) {
				// Set required variables for web.https template
				gen.Variables["core.ipboth.enabled"] = &Variable{Keyword: "core.ipboth.enabled", ValueType: ValueTypeString, Value: ""}
				gen.Variables["core.ipv4only.enabled"] = &Variable{Keyword: "core.ipv4only.enabled", ValueType: ValueTypeString, Value: ""}
				gen.Variables["core.ipv6only.enabled"] = &Variable{Keyword: "core.ipv6only.enabled", ValueType: ValueTypeString, Value: "#"}
				gen.Variables["web.https.port"] = &Variable{Keyword: "web.https.port", ValueType: ValueTypeString, Value: ":443"}
				gen.Variables["web.ssl.protocols"] = &Variable{Keyword: "web.ssl.protocols", ValueType: ValueTypeString, Value: "TLSv1.2 TLSv1.3"}
				gen.Variables["web.ssl.preferserverciphers"] = &Variable{Keyword: "web.ssl.preferserverciphers", ValueType: ValueTypeString, Value: "on"}
				gen.Variables["ssl.session.cachesize"] = &Variable{Keyword: "ssl.session.cachesize", ValueType: ValueTypeString, Value: "shared:SSL:10m"}
				gen.Variables["ssl.session.timeout"] = &Variable{Keyword: "ssl.session.timeout", ValueType: ValueTypeString, Value: "10m"}
				gen.Variables["web.ssl.ciphers"] = &Variable{Keyword: "web.ssl.ciphers", ValueType: ValueTypeString, Value: "ECDHE-RSA-AES128-GCM-SHA256"}
				gen.Variables["web.ssl.ecdh.curve"] = &Variable{Keyword: "web.ssl.ecdh.curve", ValueType: ValueTypeString, Value: "auto"}
				gen.Variables["ssl.clientcertmode"] = &Variable{Keyword: "ssl.clientcertmode", ValueType: ValueTypeString, Value: "optional"}
				gen.Variables["web.ssl.dhparam.enabled"] = &Variable{Keyword: "web.ssl.dhparam.enabled", ValueType: ValueTypeString, Value: "#"}
				gen.Variables["web.ssl.dhparam.file"] = &Variable{Keyword: "web.ssl.dhparam.file", ValueType: ValueTypeString, Value: "/opt/zextras/conf/dhparam.pem"}
				gen.Variables["proxy.http.compression"] = &Variable{Keyword: "proxy.http.compression", ValueType: ValueTypeString, Value: "gzip on;"}
				gen.Variables["web.add.headers.vhost"] = &Variable{Keyword: "web.add.headers.vhost", ValueType: ValueTypeString, Value: ""}
				gen.Variables["web.upstream.login.target"] = &Variable{Keyword: "web.upstream.login.target", ValueType: ValueTypeString, Value: "https://upstream"}
				gen.Variables["web.upstream.webclient.target"] = &Variable{Keyword: "web.upstream.webclient.target", ValueType: ValueTypeString, Value: "https://webclient"}
				gen.Variables["web.login.upstream.url"] = &Variable{Keyword: "web.login.upstream.url", ValueType: ValueTypeString, Value: "/zx/auth"}
				gen.Variables["web.carbonio.webui.login.url.vhost"] = &Variable{Keyword: "web.carbonio.webui.login.url.vhost", ValueType: ValueTypeString, Value: "/"}
				// Set global SSL defaults
				gen.Variables["ssl.crt"] = &Variable{Keyword: "ssl.crt", ValueType: ValueTypeString, Value: "/opt/zextras/ssl/carbonio/commercial.crt"}
				gen.Variables["ssl.key"] = &Variable{Keyword: "ssl.key", ValueType: ValueTypeString, Value: "/opt/zextras/ssl/carbonio/commercial.key"}
			},
			setupDomains: func() []DomainInfo {
				return []DomainInfo{
					{
						Name:             "example.com",
						VirtualHostname:  "mail.example.com",
						VirtualIPAddress: "192.168.1.10",
					},
				}
			},
			expectedChecks: []string{
				"server_name             mail.example.com",
				"listen                  192.168.1.10:443 ssl",
				"ssl_protocols           TLSv1.2 TLSv1.3",
				"ssl_certificate         /opt/zextras/ssl/carbonio/commercial.crt",
				"ssl_certificate_key     /opt/zextras/ssl/carbonio/commercial.key",
			},
		},
		{
			name:         "mail.imap with multiple domains",
			templateFile: "nginx.conf.mail.imap.template",
			setupVars: func(gen *Generator) {
				gen.Variables["core.ipboth.enabled"] = &Variable{Keyword: "core.ipboth.enabled", ValueType: ValueTypeString, Value: ""}
				gen.Variables["core.ipv4only.enabled"] = &Variable{Keyword: "core.ipv4only.enabled", ValueType: ValueTypeString, Value: "#"}
				gen.Variables["core.ipv6only.enabled"] = &Variable{Keyword: "core.ipv6only.enabled", ValueType: ValueTypeString, Value: "#"}
				gen.Variables["mail.imap.port"] = &Variable{Keyword: "mail.imap.port", ValueType: ValueTypeString, Value: ":143"}
				gen.Variables["mail.imap.timeout"] = &Variable{Keyword: "mail.imap.timeout", ValueType: ValueTypeString, Value: "60s"}
				gen.Variables["mail.imap.proxytimeout"] = &Variable{Keyword: "mail.imap.proxytimeout", ValueType: ValueTypeString, Value: "60s"}
				gen.Variables["mail.imap.tls"] = &Variable{Keyword: "mail.imap.tls", ValueType: ValueTypeString, Value: "on"}
				gen.Variables["ssl.crt"] = &Variable{Keyword: "ssl.crt", ValueType: ValueTypeString, Value: "/opt/zextras/ssl/carbonio/commercial.crt"}
				gen.Variables["ssl.key"] = &Variable{Keyword: "ssl.key", ValueType: ValueTypeString, Value: "/opt/zextras/ssl/carbonio/commercial.key"}
			},
			setupDomains: func() []DomainInfo {
				return []DomainInfo{
					{
						Name:             "domain1.com",
						VirtualHostname:  "mail.domain1.com",
						VirtualIPAddress: "10.0.0.1",
					},
					{
						Name:             "domain2.com",
						VirtualHostname:  "mail.domain2.com",
						VirtualIPAddress: "10.0.0.2",
					},
				}
			},
			expectedChecks: []string{
				"server_name             mail.domain1.com",
				"listen                  10.0.0.1:143",
				"server_name             mail.domain2.com",
				"listen                  10.0.0.2:143",
				"protocol                imap",
			},
		},
		{
			name:         "map.crt generates domain to cert mapping",
			templateFile: "nginx.conf.map.crt.template",
			setupVars: func(gen *Generator) {
				gen.Variables["ssl.crt"] = &Variable{Keyword: "ssl.crt", ValueType: ValueTypeString, Value: "/default.crt"}
			},
			setupDomains: func() []DomainInfo {
				return []DomainInfo{
					{
						Name:            "example.com",
						VirtualHostname: "mail.example.com",
						SSLCertificate:  "/ssl/example.crt",
					},
					{
						Name:            "test.com",
						VirtualHostname: "mail.test.com",
						SSLCertificate:  "/ssl/test.crt",
					},
				}
			},
			expectedChecks: []string{
				"mail.example.com /ssl/example.crt",
				"mail.test.com /ssl/test.crt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup directories
			tempDir := t.TempDir()
			outputDir := filepath.Join(tempDir, "output")

			// Use real template directory
			templateDir := filepath.Join("..", "..", "conf", "nginx", "templates")

			// Check if template file exists
			templatePath := filepath.Join(templateDir, tt.templateFile)
			if _, err := os.Stat(templatePath); os.IsNotExist(err) {
				t.Skipf("Template file not found: %s", templatePath)
			}

			// Create generator with config
			cfg := &config.Config{BaseDir: tempDir}
			gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
			if err != nil {
				t.Fatalf("Failed to create generator: %v", err)
			}

			// Setup variables
			tt.setupVars(gen)

			// Create processor with domain provider
			proc := NewTemplateProcessor(gen, templateDir, outputDir)
			proc.domainProvider = tt.setupDomains

			// Load and process template
			tmpl, err := proc.LoadTemplate(context.Background(), tt.templateFile)
			if err != nil {
				t.Fatalf("LoadTemplate failed: %v", err)
			}

			output, err := proc.ProcessTemplate(context.Background(), tmpl)
			if err != nil {
				t.Fatalf("ProcessTemplate failed: %v", err)
			}

			// Write output
			if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
				t.Fatalf("WriteOutput failed: %v", err)
			}

			// Verify expected content
			for _, expected := range tt.expectedChecks {
				if !strings.Contains(output, expected) {
					t.Errorf("Output missing expected content: %q", expected)
					t.Logf("Output:\n%s", output)
				}
			}

			// Verify no unsubstituted variables
			if strings.Contains(output, "${") {
				t.Errorf("Output contains unsubstituted variables")
				t.Logf("Output:\n%s", output)
			}
		})
	}
}
