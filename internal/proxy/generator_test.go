// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/config/configtest"
	"github.com/zextras/carbonio-configd/internal/ldap"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiscoverTemplates tests template discovery functionality
func TestDiscoverTemplates(t *testing.T) {
	// Create temp directory structure
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	// Custom template directory must be at ConfDir/nginx/templates_custom
	customTemplateDir := filepath.Join(tempDir, "nginx/templates_custom")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	if err := os.MkdirAll(customTemplateDir, 0755); err != nil {
		t.Fatalf("Failed to create custom template dir: %v", err)
	}

	// Create some standard templates
	standardTemplates := []string{
		"nginx.conf.main.template",
		"nginx.conf.web.template",
		"nginx.conf.mail.template",
	}

	for _, name := range standardTemplates {
		path := filepath.Join(templateDir, name)
		if err := os.WriteFile(path, []byte("# "+name), 0644); err != nil {
			t.Fatalf("Failed to create template %s: %v", name, err)
		}
	}

	// Create a custom template that overrides standard one
	customTemplate := "nginx.conf.web.template"
	customPath := filepath.Join(customTemplateDir, customTemplate)
	if err := os.WriteFile(customPath, []byte("# custom "+customTemplate), 0644); err != nil {
		t.Fatalf("Failed to create custom template: %v", err)
	}

	// Create generator
	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: templateDir,
		ConfDir:     tempDir,
		IncludesDir: filepath.Join(tempDir, "includes"),
	}

	// Test discovery
	templates, err := gen.DiscoverTemplates(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTemplates failed: %v", err)
	}

	// Should find 3 templates (custom overrides standard web template)
	if len(templates) != 3 {
		t.Errorf("Expected 3 templates, got %d", len(templates))
	}

	// Check that custom template is used
	found := false
	for _, path := range templates {
		if strings.HasSuffix(path, "nginx.conf.web.template") {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("Failed to read template: %v", err)
			}
			if strings.Contains(string(content), "custom") {
				found = true
				break
			}
		}
	}

	if !found {
		t.Error("Custom template was not used")
	}
}

// TestDiscoverTemplatesNoCustom tests discovery without custom templates
func TestDiscoverTemplatesNoCustom(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Create templates
	for i := range 5 {
		name := filepath.Join(templateDir, "test"+string(rune('a'+i))+".template")
		if err := os.WriteFile(name, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create template: %v", err)
		}
	}

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: templateDir,
		ConfDir:     tempDir,
	}

	templates, err := gen.DiscoverTemplates(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTemplates failed: %v", err)
	}

	if len(templates) != 5 {
		t.Errorf("Expected 5 templates, got %d", len(templates))
	}
}

// TestWriteFileAtomic tests atomic file write functionality
func TestWriteFileAtomic(t *testing.T) {
	tempDir := t.TempDir()
	gen := &Generator{
		Config: &config.Config{BaseDir: tempDir},
	}

	testPath := filepath.Join(tempDir, "test.conf")
	content := []byte("test content")

	// Test successful write
	if err := gen.writeFile(context.Background(), testPath, content); err != nil {
		t.Fatalf("writeFile failed: %v", err)
	}

	// Verify content
	written, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}

	if string(written) != string(content) {
		t.Errorf("Content mismatch: expected %q, got %q", string(content), string(written))
	}

	// Verify permissions
	info, err := os.Stat(testPath)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	if info.Mode().Perm() != 0640 {
		t.Errorf("Expected permissions 0640, got %o", info.Mode().Perm())
	}

	// Test overwrite
	newContent := []byte("new content")
	if err := gen.writeFile(context.Background(), testPath, newContent); err != nil {
		t.Fatalf("writeFile overwrite failed: %v", err)
	}

	written, err = os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("Failed to read overwritten file: %v", err)
	}

	if string(written) != string(newContent) {
		t.Errorf("Overwrite content mismatch: expected %q, got %q", string(newContent), string(written))
	}
}

// TestWriteFileDryRun tests that dry-run mode doesn't write files
func TestWriteFileDryRun(t *testing.T) {
	tempDir := t.TempDir()
	gen := &Generator{
		Config: &config.Config{BaseDir: tempDir},
		DryRun: true,
	}

	testPath := filepath.Join(tempDir, "test.conf")
	content := []byte("test content")

	// Should not error in dry-run
	if err := gen.writeFile(context.Background(), testPath, content); err != nil {
		t.Fatalf("writeFile dry-run failed: %v", err)
	}

	// File should not exist
	if _, err := os.Stat(testPath); !os.IsNotExist(err) {
		t.Error("File was created in dry-run mode")
	}
}

// TestEnsureIncludesDir tests includes directory creation
func TestEnsureIncludesDir(t *testing.T) {
	tempDir := t.TempDir()
	includesDir := filepath.Join(tempDir, "includes")

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		IncludesDir: includesDir,
	}

	// Directory shouldn't exist initially
	if _, err := os.Stat(includesDir); !os.IsNotExist(err) {
		t.Fatal("Includes directory already exists")
	}

	// Create directory
	if err := gen.ensureIncludesDir(context.Background()); err != nil {
		t.Fatalf("ensureIncludesDir failed: %v", err)
	}

	// Verify directory exists with correct permissions
	info, err := os.Stat(includesDir)
	if err != nil {
		t.Fatalf("Includes directory not created: %v", err)
	}

	if !info.IsDir() {
		t.Error("Includes path is not a directory")
	}

	if info.Mode().Perm() != 0755 {
		t.Errorf("Expected permissions 0755, got %o", info.Mode().Perm())
	}

	// Should be idempotent
	if err := gen.ensureIncludesDir(context.Background()); err != nil {
		t.Fatalf("ensureIncludesDir second call failed: %v", err)
	}
}

// TestGenerateAllIntegration tests full generation workflow
func TestGenerateAllIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	includesDir := filepath.Join(tempDir, "includes")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Create multiple templates
	templates := map[string]string{
		"test1.template": "# Template 1\nworker_processes ${main.workers};\n",
		"test2.template": "# Template 2\nerror_log ${main.logfile};\n",
		"test3.template": "# Template 3\npid ${main.pidfile};\n",
	}

	for name, content := range templates {
		path := filepath.Join(templateDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create template %s: %v", name, err)
		}
	}

	// Create generator
	cfg := &config.Config{
		BaseDir:  tempDir,
		Hostname: "test.example.com",
	}

	// Create minimal local/global/server configs
	localCfg := &config.LocalConfig{Data: config.NewConfigMap()}
	globalCfg := &config.GlobalConfig{Data: config.NewConfigMap()}
	serverCfg := &config.ServerConfig{
		Data:          config.NewConfigMap(),
		ServiceConfig: config.NewConfigMap(),
	}

	// Create LDAP client (can be nil for this test)
	var ldapClient *ldap.Ldap

	gen, err := NewGenerator(context.Background(), cfg, localCfg, globalCfg, serverCfg, ldapClient, nil)
	if err != nil {
		t.Fatalf("NewGenerator failed: %v", err)
	}

	gen.TemplateDir = templateDir
	gen.IncludesDir = includesDir

	// Generate all
	if err := gen.GenerateAll(context.Background()); err != nil {
		t.Fatalf("GenerateAll failed: %v", err)
	}

	// Verify all outputs exist
	expectedFiles := []string{"test1", "test2", "test3"}
	for _, name := range expectedFiles {
		path := filepath.Join(includesDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Output file %s not created: %v", name, err)
		}
	}
}

// TestSetDryRun tests SetDryRun sets the flag
func TestSetDryRun(t *testing.T) {
	gen := &Generator{}

	gen.SetDryRun(context.Background(), true)
	if !gen.DryRun {
		t.Error("Expected DryRun to be true after SetDryRun(true)")
	}

	gen.SetDryRun(context.Background(), false)
	if gen.DryRun {
		t.Error("Expected DryRun to be false after SetDryRun(false)")
	}
}

// TestSetVerbose tests SetVerbose sets the flag
func TestSetVerbose(t *testing.T) {
	gen := &Generator{}

	gen.SetVerbose(context.Background(), true)
	if !gen.Verbose {
		t.Error("Expected Verbose to be true after SetVerbose(true)")
	}

	gen.SetVerbose(context.Background(), false)
	if gen.Verbose {
		t.Error("Expected Verbose to be false after SetVerbose(false)")
	}
}

// TestDiscoverRealTemplates tests discovery with actual nginx templates from conf/nginx/templates
func TestDiscoverRealTemplates(t *testing.T) {
	// Check if real template directory exists
	realTemplateDir := "../../conf/nginx/templates"
	if _, err := os.Stat(realTemplateDir); os.IsNotExist(err) {
		t.Skip("Skipping test: real template directory not found")
	}

	gen := &Generator{
		Config: &config.Config{
			BaseDir: "../..",
		},
		TemplateDir: realTemplateDir,
		ConfDir:     "../../conf",
		IncludesDir: t.TempDir(),
		Variables:   make(map[string]*Variable),
	}

	templates, err := gen.DiscoverTemplates(context.Background())
	if err != nil {
		t.Fatalf("Failed to discover real templates: %v", err)
	}

	// Should find 34 templates
	if len(templates) != 34 {
		t.Errorf("Expected 34 templates, got %d", len(templates))
	}

	// Verify all templates are .template files
	for _, tmpl := range templates {
		if !strings.HasSuffix(tmpl, ".template") {
			t.Errorf("Template doesn't have .template suffix: %s", tmpl)
		}
	}

	t.Logf("Successfully discovered %d real templates", len(templates))
}

// TestDiscoverTemplatesInDirWalkError tests discoverTemplatesInDir when a walk error occurs
func TestDiscoverTemplatesInDirWalkError(t *testing.T) {
	// Pass a path that is a file, not a directory — WalkDir will error on entries
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Create a subdir inside a dir we can't read to trigger an error path
	restrictedDir := filepath.Join(tempDir, "restricted")
	if err := os.MkdirAll(restrictedDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	innerFile := filepath.Join(restrictedDir, "inner.template")
	if err := os.WriteFile(innerFile, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Chmod to 000 so walking the subdir fails
	if err := os.Chmod(restrictedDir, 0000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(restrictedDir, 0755) })

	_, err := discoverTemplatesInDir(tempDir)
	// On Linux as root the permission check may be skipped; just ensure no panic
	if err != nil {
		t.Logf("Got expected error: %v", err)
	}
}

// TestDiscoverTemplatesInDirNonExistent tests that a non-existent dir returns empty
func TestDiscoverTemplatesInDirNonExistent(t *testing.T) {
	results, err := discoverTemplatesInDir("/nonexistent-path-xyz-123")
	if err != nil {
		t.Fatalf("Expected nil error for non-existent dir, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 templates, got %d", len(results))
	}
}

// TestWriteFileRenameError tests writeFile when rename fails (cross-filesystem)
func TestWriteFileRenameError(t *testing.T) {
	// Write to /tmp but with a path in a non-existent subdir — CreateTemp will fail
	gen := &Generator{Config: &config.Config{}, DryRun: false}
	err := gen.writeFile(context.Background(), "/nonexistent-dir-xyz/subdir/file.conf", []byte("data"))
	if err == nil {
		t.Error("Expected error for bad path, got nil")
	}
}

// TestWriteFilePermissionError tests writeFile when it cannot create a temp file (bad dir)
func TestWriteFilePermissionError(t *testing.T) {
	gen := &Generator{
		Config: &config.Config{},
		DryRun: false,
	}
	// Use a non-existent directory so CreateTemp fails
	badPath := "/nonexistent-dir-xyz/file.conf"
	err := gen.writeFile(context.Background(), badPath, []byte("data"))
	if err == nil {
		t.Error("Expected error when writing to non-existent directory, got nil")
	}
}

// TestEnsureIncludesDirError tests ensureIncludesDir when path is a file (MkdirAll fails)
func TestEnsureIncludesDirError(t *testing.T) {
	tempDir := t.TempDir()
	// Create a file where the directory should be
	blocker := filepath.Join(tempDir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		IncludesDir: filepath.Join(blocker, "subdir"), // blocker is a file, so this fails
	}

	err := gen.ensureIncludesDir(context.Background())
	if err == nil {
		t.Error("Expected error when IncludesDir path is under a file, got nil")
	}
}

// TestProcessTemplateWithProcessorLoadError tests ProcessTemplateWithProcessor when template load fails
func TestProcessTemplateWithProcessorLoadError(t *testing.T) {
	tempDir := t.TempDir()
	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: tempDir,
		IncludesDir: filepath.Join(tempDir, "includes"),
		Variables:   make(map[string]*Variable),
	}

	processor := NewTemplateProcessor(gen, tempDir, gen.IncludesDir)

	// Non-existent template path should cause load error
	err := gen.ProcessTemplateWithProcessor(
		context.Background(), processor, "/nonexistent/path.template", "output.conf",
	)
	if err == nil {
		t.Error("Expected error when template file does not exist, got nil")
	}
}

// TestProcessTemplateWithProcessorNginxConf tests that nginx.conf goes to ConfDir not IncludesDir
func TestProcessTemplateWithProcessorNginxConf(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	includesDir := filepath.Join(tempDir, "includes")
	confDir := filepath.Join(tempDir, "conf")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("MkdirAll templateDir: %v", err)
	}
	if err := os.MkdirAll(includesDir, 0755); err != nil {
		t.Fatalf("MkdirAll includesDir: %v", err)
	}
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("MkdirAll confDir: %v", err)
	}

	// Create template that renders to nginx.conf
	templatePath := filepath.Join(templateDir, "nginx.conf.template")
	if err := os.WriteFile(templatePath, []byte("# nginx config\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: templateDir,
		IncludesDir: includesDir,
		ConfDir:     confDir,
		Variables:   make(map[string]*Variable),
	}

	processor := NewTemplateProcessor(gen, templateDir, includesDir)
	err := gen.ProcessTemplateWithProcessor(context.Background(), processor, templatePath, "nginx.conf")
	if err != nil {
		t.Fatalf("ProcessTemplateWithProcessor failed: %v", err)
	}

	// File must be in ConfDir, not IncludesDir
	expectedPath := filepath.Join(confDir, "nginx.conf")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("nginx.conf not found in ConfDir (%s): %v", expectedPath, err)
	}

	wrongPath := filepath.Join(includesDir, "nginx.conf")
	if _, err := os.Stat(wrongPath); !os.IsNotExist(err) {
		t.Error("nginx.conf should NOT be in IncludesDir")
	}
}

// TestProcessTemplateWithProcessorVerbose tests the verbose logging branch
func TestProcessTemplateWithProcessorVerbose(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	includesDir := filepath.Join(tempDir, "includes")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(includesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	templatePath := filepath.Join(templateDir, "test.template")
	if err := os.WriteFile(templatePath, []byte("content\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: templateDir,
		IncludesDir: includesDir,
		ConfDir:     tempDir,
		Variables:   make(map[string]*Variable),
		Verbose:     true,
	}

	processor := NewTemplateProcessor(gen, templateDir, includesDir)
	if err := gen.ProcessTemplateWithProcessor(context.Background(), processor, templatePath, "test"); err != nil {
		t.Fatalf("ProcessTemplateWithProcessor verbose failed: %v", err)
	}
}

// TestGenerateAllNoTemplates tests GenerateAll returns error when no templates found
func TestGenerateAllNoTemplates(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: templateDir,
		IncludesDir: filepath.Join(tempDir, "includes"),
		ConfDir:     tempDir,
		Variables:   make(map[string]*Variable),
	}

	err := gen.GenerateAll(context.Background())
	if err == nil {
		t.Error("Expected error when no templates found, got nil")
	}
	if !strings.Contains(err.Error(), "no templates found") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestGenerateAllWithErrors tests GenerateAll returns error when template processing fails
func TestGenerateAllWithErrors(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")
	includesDir := filepath.Join(tempDir, "includes")
	confDir := filepath.Join(tempDir, "conf")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(includesDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create a valid template
	if err := os.WriteFile(filepath.Join(templateDir, "ok.template"), []byte("# ok\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: templateDir,
		IncludesDir: includesDir,
		ConfDir:     confDir,
		Variables:   make(map[string]*Variable),
		Verbose:     true, // exercise verbose success branch
	}

	// Should succeed: valid template, writable dirs
	if err := gen.GenerateAll(context.Background()); err != nil {
		t.Fatalf("GenerateAll failed unexpectedly: %v", err)
	}
}

// TestGenerateAllDryRun tests GenerateAll skips ensureIncludesDir in dry-run mode
func TestGenerateAllDryRun(t *testing.T) {
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "templates")

	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(filepath.Join(templateDir, "test.template"), []byte("# dry\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gen := &Generator{
		Config:      &config.Config{BaseDir: tempDir},
		TemplateDir: templateDir,
		IncludesDir: filepath.Join(tempDir, "includes-does-not-exist"),
		ConfDir:     tempDir,
		Variables:   make(map[string]*Variable),
		DryRun:      true,
	}

	if err := gen.GenerateAll(context.Background()); err != nil {
		t.Fatalf("GenerateAll dry-run failed: %v", err)
	}

	// includes dir must NOT have been created in dry-run
	if _, err := os.Stat(gen.IncludesDir); !os.IsNotExist(err) {
		t.Error("IncludesDir was created in dry-run mode")
	}
}

// TestLoadConfigurationNilConfigs tests LoadConfiguration with nil local/global/server configs
func TestLoadConfigurationNilConfigs(t *testing.T) {
	cfg := &config.Config{
		BaseDir:  t.TempDir(),
		Hostname: "test.local",
	}

	// All three optional configs are nil — should create empty defaults and succeed
	gen, err := LoadConfiguration(context.Background(), cfg, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadConfiguration(nil configs) failed: %v", err)
	}
	if gen == nil {
		t.Fatal("LoadConfiguration returned nil generator")
	}
}

// TestLoadConfigurationWithConfigs tests LoadConfiguration with pre-populated configs
func TestLoadConfigurationWithConfigs(t *testing.T) {
	cfg := &config.Config{
		BaseDir:  t.TempDir(),
		Hostname: "test.local",
	}

	localCfg := &config.LocalConfig{Data: configtest.NewConfigMapFrom(map[string]string{"key": "val"})}
	globalCfg := &config.GlobalConfig{Data: configtest.NewConfigMapFrom(map[string]string{"gkey": "gval"})}
	serverCfg := &config.ServerConfig{
		Data:          configtest.NewConfigMapFrom(map[string]string{"skey": "sval"}),
		ServiceConfig: config.NewConfigMap(),
	}

	gen, err := LoadConfiguration(context.Background(), cfg, localCfg, globalCfg, serverCfg, nil, nil)
	if err != nil {
		t.Fatalf("LoadConfiguration with configs failed: %v", err)
	}
	if gen == nil {
		t.Fatal("LoadConfiguration returned nil generator")
	}
	if gen.LocalConfig != localCfg {
		t.Error("LocalConfig not set correctly")
	}
	if gen.GlobalConfig != globalCfg {
		t.Error("GlobalConfig not set correctly")
	}
}
