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
)

// TestExplodeDomainBasic tests basic domain explode functionality with mock data
func TestExplodeDomainBasic(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Create a simple template with domain explode
	templateContent := `!{explode domain(vhn)}
server {
    server_name ${vhn};
    listen ${vip}:443;
}`

	templatePath := filepath.Join(templateDir, "test.http.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create generator and processor
	gen := &Generator{
		Variables: make(map[string]*Variable),
	}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)

	// Inject mock domain provider
	proc.domainProvider = func() []DomainInfo {
		return []DomainInfo{
			{
				Name:             "example.com",
				VirtualHostname:  "mail.example.com",
				VirtualIPAddress: "192.168.1.10",
			},
			{
				Name:             "test.com",
				VirtualHostname:  "mail.test.com",
				VirtualIPAddress: "192.168.1.11",
			},
		}
	}

	// Load and process template
	tmpl, err := proc.LoadTemplate(context.Background(), "test.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	output, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate failed: %v", err)
	}

	// Write output to file
	if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
		t.Fatalf("WriteOutput failed: %v", err)
	}

	// Read output
	outputPath := filepath.Join(outputDir, "test.http")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := string(content)

	// Verify both domains are present
	if !strings.Contains(outputStr, "mail.example.com") {
		t.Errorf("Output missing first domain hostname")
	}
	if !strings.Contains(outputStr, "192.168.1.10:443") {
		t.Errorf("Output missing first domain IP")
	}
	if !strings.Contains(outputStr, "mail.test.com") {
		t.Errorf("Output missing second domain hostname")
	}
	if !strings.Contains(outputStr, "192.168.1.11:443") {
		t.Errorf("Output missing second domain IP")
	}

	// Verify blank line between domains
	lines := strings.Split(strings.TrimSpace(outputStr), "\n")
	foundBlankLine := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			foundBlankLine = true
			break
		}
	}
	if !foundBlankLine {
		t.Errorf("Expected blank line between domain blocks")
	}
}

// TestExplodeDomainWithSSO tests domain explode with SSO requirement
func TestExplodeDomainWithSSO(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Template with SSO requirement
	templateContent := `!{explode domain(vhn, sso)}
server {
    server_name ${vhn};
    ssl_verify_client ${ssl.clientcertmode};
}`

	templatePath := filepath.Join(templateDir, "sso.http.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{
		Variables: make(map[string]*Variable),
	}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)

	// Mock domains: one with SSO, two without
	proc.domainProvider = func() []DomainInfo {
		return []DomainInfo{
			{
				Name:             "example.com",
				VirtualHostname:  "mail.example.com",
				VirtualIPAddress: "192.168.1.10",
				ClientCertMode:   "optional", // Has SSO
			},
			{
				Name:             "test.com",
				VirtualHostname:  "mail.test.com",
				VirtualIPAddress: "192.168.1.11",
				ClientCertMode:   "off", // No SSO - should be filtered out
			},
			{
				Name:             "demo.com",
				VirtualHostname:  "mail.demo.com",
				VirtualIPAddress: "192.168.1.12",
				ClientCertMode:   "", // No SSO - should be filtered out
			},
		}
	}

	// Load and process template
	tmpl, err := proc.LoadTemplate(context.Background(), "sso.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	output, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate failed: %v", err)
	}

	if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
		t.Fatalf("WriteOutput failed: %v", err)
	}

	// Read output
	outputPath := filepath.Join(outputDir, "sso.http")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := string(content)

	// Only the first domain should be present
	if !strings.Contains(outputStr, "mail.example.com") {
		t.Errorf("Output missing SSO-enabled domain")
	}
	if strings.Contains(outputStr, "mail.test.com") {
		t.Errorf("Output should not contain domain with ClientCertMode=off")
	}
	if strings.Contains(outputStr, "mail.demo.com") {
		t.Errorf("Output should not contain domain with empty ClientCertMode")
	}
}

// TestExplodeDomainWithSSL tests domain explode with SSL certificate variables
func TestExplodeDomainWithSSL(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	templateContent := `!{explode domain(vhn)}
server {
    server_name ${vhn};
    ssl_certificate ${ssl.crt};
    ssl_certificate_key ${ssl.key};
}`

	templatePath := filepath.Join(templateDir, "ssl.http.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{
		Variables: make(map[string]*Variable),
	}

	// Set global default SSL paths
	gen.Variables["ssl.crt"] = &Variable{
		Keyword:   "ssl.crt",
		ValueType: ValueTypeString,
		Value:     "/opt/zextras/ssl/default.crt",
	}
	gen.Variables["ssl.key"] = &Variable{
		Keyword:   "ssl.key",
		ValueType: ValueTypeString,
		Value:     "/opt/zextras/ssl/default.key",
	}

	proc := NewTemplateProcessor(gen, templateDir, outputDir)

	// Mock domain with custom SSL cert
	proc.domainProvider = func() []DomainInfo {
		return []DomainInfo{
			{
				Name:             "example.com",
				VirtualHostname:  "mail.example.com",
				VirtualIPAddress: "192.168.1.10",
				SSLCertificate:   "/opt/zextras/ssl/example.com.crt",
				SSLPrivateKey:    "/opt/zextras/ssl/example.com.key",
			},
			{
				Name:             "test.com",
				VirtualHostname:  "mail.test.com",
				VirtualIPAddress: "192.168.1.11",
				// No custom SSL - should use global defaults
			},
		}
	}

	// Load and process template
	tmpl, err := proc.LoadTemplate(context.Background(), "ssl.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	output, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate failed: %v", err)
	}

	if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
		t.Fatalf("WriteOutput failed: %v", err)
	}

	// Read output
	outputPath := filepath.Join(outputDir, "ssl.http")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := string(content)

	// Verify first domain uses custom SSL
	if !strings.Contains(outputStr, "example.com.crt") {
		t.Errorf("Output missing custom SSL certificate for first domain")
	}
	if !strings.Contains(outputStr, "example.com.key") {
		t.Errorf("Output missing custom SSL key for first domain")
	}

	// Verify second domain uses default SSL
	// The second block should use global defaults
	lines := strings.Split(outputStr, "\n")
	inSecondBlock := false
	foundDefaultCert := false
	foundDefaultKey := false

	for _, line := range lines {
		if strings.Contains(line, "mail.test.com") {
			inSecondBlock = true
		}
		if inSecondBlock {
			if strings.Contains(line, "ssl_certificate ") && strings.Contains(line, "default.crt") {
				foundDefaultCert = true
			}
			if strings.Contains(line, "ssl_certificate_key ") && strings.Contains(line, "default.key") {
				foundDefaultKey = true
			}
		}
	}

	if !foundDefaultCert {
		t.Errorf("Second domain should use default SSL certificate")
	}
	if !foundDefaultKey {
		t.Errorf("Second domain should use default SSL key")
	}
}

// TestExplodeServerBasic tests basic server explode functionality
func TestExplodeServerBasic(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	templateContent := `!{explode server(mailbox)}
upstream ${server_id}_backend {
    server ${server_hostname}:7071;
}`

	templatePath := filepath.Join(templateDir, "upstream.http.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{
		Variables: make(map[string]*Variable),
	}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)

	// Mock servers with mailbox service
	proc.serverProvider = func(serviceName string) []ServerInfo {
		if serviceName != "mailbox" {
			return []ServerInfo{}
		}
		return []ServerInfo{
			{
				ID:       "server1-id",
				Hostname: "mailbox1.example.com",
				Services: []string{"mailbox", "ldap"},
			},
			{
				ID:       "server2-id",
				Hostname: "mailbox2.example.com",
				Services: []string{"mailbox", "mta"},
			},
		}
	}

	// Load and process template
	tmpl, err := proc.LoadTemplate(context.Background(), "upstream.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	output, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate failed: %v", err)
	}

	if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
		t.Fatalf("WriteOutput failed: %v", err)
	}

	// Read output
	outputPath := filepath.Join(outputDir, "upstream.http")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := string(content)

	// Verify both servers are present
	if !strings.Contains(outputStr, "server1-id_backend") {
		t.Errorf("Output missing first server upstream")
	}
	if !strings.Contains(outputStr, "mailbox1.example.com:7071") {
		t.Errorf("Output missing first server hostname")
	}
	if !strings.Contains(outputStr, "server2-id_backend") {
		t.Errorf("Output missing second server upstream")
	}
	if !strings.Contains(outputStr, "mailbox2.example.com:7071") {
		t.Errorf("Output missing second server hostname")
	}
}

// TestExplodeServerSkipsComments tests that server explode skips comment lines
func TestExplodeServerSkipsComments(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	templateContent := `!{explode server(docs)}
# This is a comment with ${server_id}
upstream ${server_id}_docs {
    # Another comment
    server ${server_hostname}:8080;
}`

	templatePath := filepath.Join(templateDir, "docs.http.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{
		Variables: make(map[string]*Variable),
	}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)

	// Mock single docs server
	proc.serverProvider = func(serviceName string) []ServerInfo {
		if serviceName != "docs" {
			return []ServerInfo{}
		}
		return []ServerInfo{
			{
				ID:       "docs-server-1",
				Hostname: "docs1.example.com",
				Services: []string{"docs"},
			},
		}
	}

	// Load and process template
	tmpl, err := proc.LoadTemplate(context.Background(), "docs.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	output, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate failed: %v", err)
	}

	if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
		t.Fatalf("WriteOutput failed: %v", err)
	}

	// Read output
	outputPath := filepath.Join(outputDir, "docs.http")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := string(content)

	// Verify comments are NOT in output (server explode skips them)
	if strings.Contains(outputStr, "# This is a comment") {
		t.Errorf("Output should not contain comment lines from exploded template")
	}
	if strings.Contains(outputStr, "# Another comment") {
		t.Errorf("Output should not contain comment lines from exploded template")
	}

	// Verify actual config lines ARE present
	if !strings.Contains(outputStr, "upstream docs-server-1_docs") {
		t.Errorf("Output missing upstream directive")
	}
	if !strings.Contains(outputStr, "server docs1.example.com:8080") {
		t.Errorf("Output missing server directive")
	}
}

// TestExplodeNoDomains tests behavior when no domains are available
func TestExplodeNoDomains(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	templateContent := `!{explode domain(vhn)}
server {
    server_name ${vhn};
}`

	templatePath := filepath.Join(templateDir, "empty.http.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{
		Variables: make(map[string]*Variable),
	}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)

	// No domain provider - uses default (empty list)
	// Load and process template
	tmpl, err := proc.LoadTemplate(context.Background(), "empty.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	output, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate should not fail with no domains: %v", err)
	}

	if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
		t.Fatalf("WriteOutput failed: %v", err)
	}

	// Read output
	outputPath := filepath.Join(outputDir, "empty.http")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := strings.TrimSpace(string(content))

	// Output should be empty or minimal
	if len(outputStr) > 0 {
		t.Errorf("Expected empty output when no domains available, got: %s", outputStr)
	}
}

func TestExplodeDomainDeterministicOrdering(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	templatePath := filepath.Join(templateDir, "deterministic-domain.http.template")
	if err := os.WriteFile(templatePath, []byte("!{explode domain(vhn)}\n${vhn}"), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{Variables: make(map[string]*Variable)}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)
	proc.domainProvider = func() []DomainInfo {
		return []DomainInfo{
			{Name: "z.example.com", VirtualHostname: "z.example.com"},
			{Name: "a.example.com", VirtualHostname: "a.example.com"},
			{Name: "m.example.com", VirtualHostname: "m.example.com"},
		}
	}

	tmpl, err := proc.LoadTemplate(context.Background(), "deterministic-domain.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	first, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("first ProcessTemplate failed: %v", err)
	}
	second, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("second ProcessTemplate failed: %v", err)
	}
	if first != second {
		t.Fatalf("domain output changed between runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Index(first, "a.example.com") > strings.Index(first, "m.example.com") || strings.Index(first, "m.example.com") > strings.Index(first, "z.example.com") {
		t.Fatalf("domain output not sorted by name (expected a < m < z): %q", first)
	}
	aIdx := strings.Index(first, "a.example.com")
	mIdx := strings.Index(first, "m.example.com")
	zIdx := strings.Index(first, "z.example.com")
	if aIdx == -1 || mIdx == -1 || zIdx == -1 {
		t.Fatalf("missing expected domains in output: %q", first)
	}
	if aIdx >= mIdx || mIdx >= zIdx {
		t.Fatalf("domain output not sorted by name (a=%d, m=%d, z=%d): %q", aIdx, mIdx, zIdx, first)
	}
}

func TestExplodeServerDeterministicOrdering(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	templatePath := filepath.Join(templateDir, "deterministic-server.http.template")
	if err := os.WriteFile(templatePath, []byte("!{explode server(mailbox)}\n${server_hostname}"), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{Variables: make(map[string]*Variable)}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)
	proc.serverProvider = func(serviceName string) []ServerInfo {
		return []ServerInfo{
			{ID: "3", Hostname: "z.example.com"},
			{ID: "1", Hostname: "a.example.com"},
			{ID: "2", Hostname: "m.example.com"},
		}
	}

	tmpl, err := proc.LoadTemplate(context.Background(), "deterministic-server.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	first, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("first ProcessTemplate failed: %v", err)
	}
	second, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("second ProcessTemplate failed: %v", err)
	}
	if first != second {
		t.Fatalf("server output changed between runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	aIdx := strings.Index(first, "a.example.com")
	mIdx := strings.Index(first, "m.example.com")
	zIdx := strings.Index(first, "z.example.com")
	if aIdx == -1 || mIdx == -1 || zIdx == -1 {
		t.Fatalf("missing expected servers in output: %q", first)
	}
	if aIdx >= mIdx || mIdx >= zIdx {
		t.Fatalf("server output not sorted by hostname (a=%d, m=%d, z=%d): %q", aIdx, mIdx, zIdx, first)
	}
}

// TestExplodeNoServers tests behavior when no servers with service are available
func TestExplodeNoServers(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	outputDir := filepath.Join(tempDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	templateContent := `!{explode server(nonexistent)}
upstream ${server_id}_backend {
    server ${server_hostname};
}`

	templatePath := filepath.Join(templateDir, "noservers.http.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	gen := &Generator{
		Variables: make(map[string]*Variable),
	}
	proc := NewTemplateProcessor(gen, templateDir, outputDir)

	// Mock returns empty server list for "nonexistent" service
	proc.serverProvider = func(serviceName string) []ServerInfo {
		return []ServerInfo{}
	}

	// Load and process template
	tmpl, err := proc.LoadTemplate(context.Background(), "noservers.http.template")
	if err != nil {
		t.Fatalf("LoadTemplate failed: %v", err)
	}

	output, err := proc.ProcessTemplate(context.Background(), tmpl)
	if err != nil {
		t.Fatalf("ProcessTemplate should not fail with no servers: %v", err)
	}

	if err := proc.WriteOutput(context.Background(), tmpl.Name, output); err != nil {
		t.Fatalf("WriteOutput failed: %v", err)
	}

	// Read output
	outputPath := filepath.Join(outputDir, "noservers.http")
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	outputStr := strings.TrimSpace(string(content))

	// Output should be empty
	if len(outputStr) > 0 {
		t.Errorf("Expected empty output when no servers available, got: %s", outputStr)
	}
}

// TestExplodeInvalidDirective tests error handling for malformed explode directives
func TestExplodeInvalidDirective(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantErr  bool
	}{
		{
			name:     "missing closing paren in domain",
			template: "!{explode domain(vhn}\nserver {}",
			wantErr:  false, // This won't match the pattern, so it's processed as normal template
		},
		{
			name:     "empty domain args",
			template: "!{explode domain()}\nserver {}",
			wantErr:  true,
		},
		{
			name:     "empty server args",
			template: "!{explode server()}\nserver {}",
			wantErr:  true,
		},
		{
			name:     "unknown explode type",
			template: "!{explode unknown(arg)}\nserver {}",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			templateDir := filepath.Join(tempDir, "templates")
			outputDir := filepath.Join(tempDir, "output")

			if err := os.MkdirAll(templateDir, 0755); err != nil {
				t.Fatalf("Failed to create template dir: %v", err)
			}

			templatePath := filepath.Join(templateDir, "invalid.http.template")
			if err := os.WriteFile(templatePath, []byte(tt.template), 0644); err != nil {
				t.Fatalf("Failed to write template: %v", err)
			}

			gen := &Generator{
				Variables: make(map[string]*Variable),
			}
			proc := NewTemplateProcessor(gen, templateDir, outputDir)

			tmpl, err := proc.LoadTemplate(context.Background(), "invalid.http.template")
			if err != nil {
				t.Fatalf("LoadTemplate failed: %v", err)
			}

			_, err = proc.ProcessTemplate(context.Background(), tmpl)
			if (err != nil) != tt.wantErr {
				t.Errorf("ProcessTemplate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
