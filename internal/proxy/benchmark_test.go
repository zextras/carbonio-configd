// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/config"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkVariableRegistration benchmarks variable registration
func BenchmarkVariableRegistration(b *testing.B) {
	cfg := &config.Config{
		BaseDir:  "/opt/zextras",
		Hostname: "test.example.com",
	}

	for b.Loop() {
		gen := &Generator{
			Config:     cfg,
			Variables:  make(map[string]*Variable),
			Hostname:   cfg.Hostname,
			WorkingDir: cfg.BaseDir,
		}
		gen.RegisterVariables(context.Background())
	}
}

// BenchmarkVariableResolution benchmarks resolving all variables
func BenchmarkVariableResolution(b *testing.B) {
	cfg := &config.Config{
		BaseDir:  "/opt/zextras",
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		b.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	for b.Loop() {
		if err := gen.ResolveAllVariables(context.Background()); err != nil {
			b.Fatalf("ResolveAllVariables() failed: %v", err)
		}
	}
}

// BenchmarkTemplateProcessing benchmarks processing a single template
func BenchmarkTemplateProcessing(b *testing.B) {
	// Create temp directories
	tmpDir := b.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		b.Fatalf("Failed to create template dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		b.Fatalf("Failed to create output dir: %v", err)
	}

	// Create a test template with multiple variables
	templateContent := `# NGINX Configuration
worker_processes ${main.workers};
worker_connections ${main.workerConnections};
error_log ${main.logfile} ${main.logLevel};

# SSL Configuration
ssl_certificate ${ssl.crt.default};
ssl_certificate_key ${ssl.key.default};
ssl_protocols ${ssl.protocols};
ssl_ciphers ${ssl.ciphers};

# HTTP Configuration
upstream mail {
    server ${web.upstream.target}:${web.http.port};
}

# Timeouts
proxy_connect_timeout ${main.ctimeout};
proxy_read_timeout ${main.rtimeout};
proxy_send_timeout ${main.stimeout};
`
	templatePath := filepath.Join(templateDir, "nginx.conf.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		b.Fatalf("Failed to write template: %v", err)
	}

	// Create generator
	cfg := &config.Config{
		BaseDir:  tmpDir,
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		b.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.ConfDir = outputDir

	processor := NewTemplateProcessor(gen, templateDir, outputDir)
	tmpl, err := processor.LoadTemplate(context.Background(), "nginx.conf.template")
	if err != nil {
		b.Fatalf("LoadTemplate() failed: %v", err)
	}

	for b.Loop() {
		if _, err := processor.ProcessTemplate(context.Background(), tmpl); err != nil {
			b.Fatalf("ProcessTemplate() failed: %v", err)
		}
	}
}

// BenchmarkFullGeneration benchmarks end-to-end generation
func BenchmarkFullGeneration(b *testing.B) {
	// Create temp directories
	tmpDir := b.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		b.Fatalf("Failed to create template dir: %v", err)
	}

	// Create multiple test templates to simulate real workload
	templates := map[string]string{
		"nginx.conf.main.template": `worker_processes ${main.workers};
events { worker_connections ${main.workerConnections}; }
http { include ${core.includes}/http.conf; }`,
		"http.conf.template": `upstream backend { server ${web.upstream.target}:${web.http.port}; }
server { listen ${web.http.port}; ssl_certificate ${ssl.crt.default}; }`,
		"mail.conf.template": `mail {
    server_name ${main.servername};
    auth_http ${mail.authurl};
    proxy_pass_error_message on;
}`,
	}

	for name, content := range templates {
		path := filepath.Join(templateDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			b.Fatalf("Failed to write template %s: %v", name, err)
		}
	}

	for b.Loop() {
		// Recreate output dir for each iteration
		iterOutputDir := filepath.Join(outputDir, b.Name())
		if err := os.MkdirAll(iterOutputDir, 0755); err != nil {
			b.Fatalf("Failed to create output dir: %v", err)
		}

		cfg := &config.Config{
			BaseDir:  tmpDir,
			Hostname: "test.example.com",
		}

		gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
		if err != nil {
			b.Fatalf("NewGenerator(, nil) failed: %v", err)
		}

		gen.TemplateDir = templateDir
		gen.ConfDir = iterOutputDir

		processor := NewTemplateProcessor(gen, templateDir, iterOutputDir)
		if err := processor.ProcessAllTemplates(context.Background()); err != nil {
			b.Fatalf("ProcessAllTemplates() failed: %v", err)
		}

		// Cleanup for next iteration
		os.RemoveAll(iterOutputDir)
	}
}

// BenchmarkVariableLookup benchmarks individual variable lookups
func BenchmarkVariableLookup(b *testing.B) {
	cfg := &config.Config{
		BaseDir:  "/opt/zextras",
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		b.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	for b.Loop() {
		_, _ = gen.GetVariable("main.workers")
		_, _ = gen.GetVariable("ssl.crt.default")
		_, _ = gen.GetVariable("web.http.port")
	}
}

// BenchmarkVariableExpansion benchmarks expanding variables with ${} syntax
func BenchmarkVariableExpansion(b *testing.B) {
	cfg := &config.Config{
		BaseDir:  "/opt/zextras",
		Hostname: "test.example.com",
	}

	gen, err := NewGenerator(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		b.Fatalf("NewGenerator(, nil) failed: %v", err)
	}

	testStrings := []string{
		"worker_processes ${main.workers};",
		"ssl_certificate ${ssl.crt.default};",
		"proxy_connect_timeout ${main.ctimeout}s;",
	}

	processor := NewTemplateProcessor(gen, "", "")

	for b.Loop() {
		for _, str := range testStrings {
			_ = processor.varPattern.ReplaceAllStringFunc(str, func(match string) string {
				varName := match[2 : len(match)-1]
				value, _ := gen.ExpandVariable(context.Background(), varName)
				return value
			})
		}
	}
}
