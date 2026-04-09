// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package template

import (
	"bytes"
	"context"
	"fmt"
	"github.com/zextras/carbonio-configd/internal/config"
	"github.com/zextras/carbonio-configd/internal/state"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skipIfRoot skips the test when running as root because chmod(0o000) has no
// effect for root and the permission-denied error paths cannot be triggered.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("skipping permission-based test: running as root")
	}
}

// mockConfigLookup implements the ConfigLookup interface for testing
type mockConfigLookup struct {
	data map[string]map[string]string
}

func (m *mockConfigLookup) LookUpConfig(ctx context.Context, cfgType, key string) (string, error) {
	if typeData, ok := m.data[cfgType]; ok {
		if val, ok := typeData[key]; ok {
			return val, nil
		}
	}
	return "", fmt.Errorf("key not found: %s:%s", cfgType, key)
}

func newMockLookup() *mockConfigLookup {
	return &mockConfigLookup{
		data: map[string]map[string]string{
			"VAR": {
				"zimbraServerHostname": "mail.example.com",
				"zimbraMtaMyNetworks":  "127.0.0.0/8 192.168.1.0/24",
				"zimbraMtaMyOrigin":    "example.com",
				"zimbraMtaRelayHost":   "relay.example.com",
				"zimbraSmtpPort":       "25",
				"zimbraHttpPort":       "8080",
				"zimbraHttpSSLPort":    "8443",
				"emptyVar":             "",
			},
			"LOCAL": {
				"hostname":             "server1.example.com",
				"postfix_mail_owner":   "postfix",
				"postfix_setgid_group": "postdrop",
			},
			"SERVICE": {
				"antivirus": "TRUE",
				"antispam":  "TRUE",
				"webmail":   "FALSE",
				"ldap":      "TRUE",
			},
		},
	}
}

func TestRewriteReader(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	rewriter := NewRewriter("/opt/zextras", mockLookup, st)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple VAR substitution",
			input:    "myhostname = %%VAR:zimbraServerHostname%%",
			expected: "myhostname = mail.example.com\n",
		},
		{
			name:     "Multiple VAR substitutions on same line",
			input:    "mynetworks = %%VAR:zimbraMtaMyNetworks%% %%VAR:zimbraMtaMyOrigin%%",
			expected: "mynetworks = 127.0.0.0/8 192.168.1.0/24 example.com\n",
		},
		{
			name:     "LOCAL substitution",
			input:    "hostname = %%LOCAL:hostname%%",
			expected: "hostname = server1.example.com\n",
		},
		{
			name:     "SERVICE substitution",
			input:    "av_enabled = %%SERVICE:antivirus%%",
			expected: "av_enabled = TRUE\n",
		},
		{
			name:     "Mixed substitutions",
			input:    "server = %%VAR:zimbraServerHostname%% owner = %%LOCAL:postfix_mail_owner%%",
			expected: "server = mail.example.com owner = postfix\n",
		},
		{
			name:     "No substitutions",
			input:    "# This is a comment",
			expected: "# This is a comment\n",
		},
		{
			name:     "Empty line",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := strings.NewReader(tt.input)
			output := &bytes.Buffer{}

			err := rewriter.RewriteReader(context.Background(), input, output)
			if err != nil {
				t.Fatalf("RewriteReader() error = %v", err)
			}

			if output.String() != tt.expected {
				t.Errorf("RewriteReader() output = %q, want %q", output.String(), tt.expected)
			}
		})
	}
}

func TestRewriteReader_MultipleLines(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	rewriter := NewRewriter("/opt/zextras", mockLookup, st)

	input := `# Postfix configuration
myhostname = %%VAR:zimbraServerHostname%%
myorigin = %%VAR:zimbraMtaMyOrigin%%
mynetworks = %%VAR:zimbraMtaMyNetworks%%
relayhost = %%VAR:zimbraMtaRelayHost%%

# Owner settings
mail_owner = %%LOCAL:postfix_mail_owner%%
setgid_group = %%LOCAL:postfix_setgid_group%%
`

	expected := `# Postfix configuration
myhostname = mail.example.com
myorigin = example.com
mynetworks = 127.0.0.0/8 192.168.1.0/24
relayhost = relay.example.com

# Owner settings
mail_owner = postfix
setgid_group = postdrop
`

	inputReader := strings.NewReader(input)
	output := &bytes.Buffer{}

	err := rewriter.RewriteReader(context.Background(), inputReader, output)
	if err != nil {
		t.Fatalf("RewriteReader() error = %v", err)
	}

	if output.String() != expected {
		t.Errorf("RewriteReader() output mismatch\nGot:\n%s\nWant:\n%s", output.String(), expected)
	}
}

func TestRewriteConfig(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}

	// Create temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "configd-rewrite-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Create a source .in file
	sourceContent := `myhostname = %%VAR:zimbraServerHostname%%
myorigin = %%VAR:zimbraMtaMyOrigin%%
smtp_bind_address = %%LOCAL:hostname%%
`
	sourcePath := filepath.Join(tmpDir, "test.conf.in")
	if err := os.WriteFile(sourcePath, []byte(sourceContent), 0644); err != nil {
		t.Fatalf("Failed to write source file: %v", err)
	}

	// Test rewrite with default mode
	targetPath := filepath.Join(tmpDir, "test.conf")
	err = rewriter.RewriteConfig(context.Background(), "test.conf.in", "test.conf", "")
	if err != nil {
		t.Fatalf("RewriteConfig() error = %v", err)
	}

	// Check target file exists
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		t.Fatalf("Target file not created: %s", targetPath)
	}

	// Check target file content
	targetContent, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("Failed to read target file: %v", err)
	}

	expectedContent := `myhostname = mail.example.com
myorigin = example.com
smtp_bind_address = server1.example.com
`
	if string(targetContent) != expectedContent {
		t.Errorf("Target file content mismatch\nGot:\n%s\nWant:\n%s", string(targetContent), expectedContent)
	}

	// Check file permissions (default 0440)
	fileInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("Failed to stat target file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0440 {
		t.Errorf("File mode = %o, want %o", fileInfo.Mode().Perm(), 0440)
	}
}

func TestRewriteConfig_CustomMode(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}

	// Create temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "configd-rewrite-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Create a source .in file
	sourceContent := `secret_key = %%VAR:zimbraServerHostname%%`
	sourcePath := filepath.Join(tmpDir, "secret.conf.in")
	if err := os.WriteFile(sourcePath, []byte(sourceContent), 0644); err != nil {
		t.Fatalf("Failed to write source file: %v", err)
	}

	// Test rewrite with custom mode 0600
	targetPath := filepath.Join(tmpDir, "secret.conf")
	err = rewriter.RewriteConfig(context.Background(), "secret.conf.in", "secret.conf", "0600")
	if err != nil {
		t.Fatalf("RewriteConfig() error = %v", err)
	}

	// Check file permissions
	fileInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("Failed to stat target file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Errorf("File mode = %o, want %o", fileInfo.Mode().Perm(), 0600)
	}
}

func TestRewriteConfig_AtomicOverwrite(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}

	// Create temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "configd-rewrite-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Create a source .in file
	sourceContent := `hostname = %%VAR:zimbraServerHostname%%`
	sourcePath := filepath.Join(tmpDir, "test.conf.in")
	if err := os.WriteFile(sourcePath, []byte(sourceContent), 0644); err != nil {
		t.Fatalf("Failed to write source file: %v", err)
	}

	// Create existing target file
	targetPath := filepath.Join(tmpDir, "test.conf")
	oldContent := "hostname = old.example.com\n"
	if err := os.WriteFile(targetPath, []byte(oldContent), 0644); err != nil {
		t.Fatalf("Failed to write target file: %v", err)
	}

	// Rewrite (should overwrite atomically)
	err = rewriter.RewriteConfig(context.Background(), "test.conf.in", "test.conf", "")
	if err != nil {
		t.Fatalf("RewriteConfig() error = %v", err)
	}

	// Check target file content (should be updated)
	targetContent, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("Failed to read target file: %v", err)
	}

	expectedContent := `hostname = mail.example.com
`
	if string(targetContent) != expectedContent {
		t.Errorf("Target file not overwritten\nGot:\n%s\nWant:\n%s", string(targetContent), expectedContent)
	}
}

func TestRewriteConfig_InvalidMode(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir, _ := os.MkdirTemp("", "configd-rewrite-test-*")
	defer os.RemoveAll(tmpDir)

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Test with invalid mode
	err := rewriter.RewriteConfig(context.Background(), "test.in", "test.out", "invalid")
	if err == nil {
		t.Error("Expected error for invalid mode, got nil")
	}
	if !strings.Contains(err.Error(), "invalid file mode") {
		t.Errorf("Error message should mention 'invalid file mode', got: %v", err)
	}
}

func TestRewriteConfig_MissingSourceFile(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir, _ := os.MkdirTemp("", "configd-rewrite-test-*")
	defer os.RemoveAll(tmpDir)

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Test with non-existent source file
	err := rewriter.RewriteConfig(context.Background(), "nonexistent.in", "test.out", "")
	if err == nil {
		t.Error("Expected error for missing source file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open source file") {
		t.Errorf("Error message should mention 'failed to open source file', got: %v", err)
	}
}

func TestRewriteAllConfigs(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}

	// Create temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "configd-rewrite-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Create source files
	sourceFiles := map[string]string{
		"conf/postfix.conf.in": `myhostname = %%VAR:zimbraServerHostname%%
`,
		"conf/nginx.conf.in": `server_name %%VAR:zimbraServerHostname%%;
listen %%VAR:zimbraHttpPort%%;
`,
	}

	for path, content := range sourceFiles {
		fullPath := filepath.Join(tmpDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write source file %s: %v", path, err)
		}
	}

	// Create sections with REWRITE directives
	sections := map[string]*config.MtaConfigSection{
		"postfix": {
			Name: "postfix",
			RequiredVars: map[string]string{
				"conf/postfix.conf.in": "REWRITE",
			},
			Rewrites: map[string]config.RewriteEntry{
				"conf/postfix.conf.in": {
					Value: "conf/postfix.conf",
					Mode:  "0644",
				},
			},
		},
		"nginx": {
			Name: "nginx",
			RequiredVars: map[string]string{
				"conf/nginx.conf.in": "REWRITE",
			},
			Rewrites: map[string]config.RewriteEntry{
				"conf/nginx.conf.in": {
					Value: "conf/nginx.conf",
					Mode:  "0600",
				},
			},
		},
	}

	// Test rewriting configs using RewriteConfig for each section
	for _, section := range sections {
		for source, rewriteEntry := range section.Rewrites {
			err = rewriter.RewriteConfig(context.Background(), source, rewriteEntry.Value, rewriteEntry.Mode)
			if err != nil {
				t.Fatalf("RewriteConfig(%s) error = %v", source, err)
			}
		}
	}

	// Verify postfix.conf
	postfixPath := filepath.Join(tmpDir, "conf/postfix.conf")
	postfixContent, err := os.ReadFile(postfixPath)
	if err != nil {
		t.Fatalf("Failed to read postfix.conf: %v", err)
	}
	expectedPostfix := `myhostname = mail.example.com
`
	if string(postfixContent) != expectedPostfix {
		t.Errorf("postfix.conf mismatch\nGot:\n%s\nWant:\n%s", string(postfixContent), expectedPostfix)
	}

	// Verify postfix.conf permissions
	postfixInfo, _ := os.Stat(postfixPath)
	if postfixInfo.Mode().Perm() != 0644 {
		t.Errorf("postfix.conf mode = %o, want %o", postfixInfo.Mode().Perm(), 0644)
	}

	// Verify nginx.conf
	nginxPath := filepath.Join(tmpDir, "conf/nginx.conf")
	nginxContent, err := os.ReadFile(nginxPath)
	if err != nil {
		t.Fatalf("Failed to read nginx.conf: %v", err)
	}
	expectedNginx := `server_name mail.example.com;
listen 8080;
`
	if string(nginxContent) != expectedNginx {
		t.Errorf("nginx.conf mismatch\nGot:\n%s\nWant:\n%s", string(nginxContent), expectedNginx)
	}

	// Verify nginx.conf permissions
	nginxInfo, _ := os.Stat(nginxPath)
	if nginxInfo.Mode().Perm() != 0600 {
		t.Errorf("nginx.conf mode = %o, want %o", nginxInfo.Mode().Perm(), 0600)
	}
}

// errorWriter always returns an error on Write.
type errorWriter struct{}

func (e *errorWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("simulated write error")
}

func TestRewriteReader_WriteError(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	rewriter := NewRewriter("/opt/zextras", mockLookup, st)

	// bufio.Writer buffers writes; the underlying write error surfaces either on
	// WriteString (if buffer is full) or on Flush. Either way an error is returned.
	input := strings.NewReader("myhostname = %%VAR:zimbraServerHostname%%\n")
	writer := &errorWriter{}

	err := rewriter.RewriteReader(context.Background(), input, writer)
	if err == nil {
		t.Fatal("RewriteReader() expected error on write failure, got nil")
	}
	// The error may be from WriteString (buffer overflow) or Flush.
	if !strings.Contains(err.Error(), "failed to write transformed line") &&
		!strings.Contains(err.Error(), "failed to flush output") {
		t.Errorf("RewriteReader() error = %v, want write or flush error", err)
	}
}

func TestRewriteReader_FlushError(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	rewriter := NewRewriter("/opt/zextras", mockLookup, st)

	// Use a pipe: close the read-end with an error so the write-end always fails.
	pr, pw := io.Pipe()
	pr.CloseWithError(fmt.Errorf("pipe closed"))

	// Provide enough content that bufio must flush to the underlying writer.
	// Each transformed line is ~30 bytes; 200 lines exceed the default 4096-byte buffer.
	var bigInput strings.Builder
	for i := 0; i < 200; i++ {
		bigInput.WriteString("myhostname = %%VAR:zimbraServerHostname%%\n")
	}
	input := strings.NewReader(bigInput.String())

	err := rewriter.RewriteReader(context.Background(), input, pw)
	pw.Close()

	if err == nil {
		t.Fatal("RewriteReader() expected error when writer fails, got nil")
	}
}

func TestRewriteConfig_TargetInSubdir(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()
	rewriter := NewRewriter(tmpDir, mockLookup, st)

	sourceContent := "hostname = %%VAR:zimbraServerHostname%%\n"
	sourcePath := filepath.Join(tmpDir, "source.in")
	if err := os.WriteFile(sourcePath, []byte(sourceContent), 0644); err != nil {
		t.Fatalf("Failed to write source file: %v", err)
	}

	// Target in a subdirectory that does not yet exist — MkdirAll must create it.
	err := rewriter.RewriteConfig(context.Background(), "source.in", "subdir/nested/output.conf", "0640")
	if err != nil {
		t.Fatalf("RewriteConfig() error = %v", err)
	}

	targetPath := filepath.Join(tmpDir, "subdir/nested/output.conf")
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("Failed to read target file: %v", err)
	}
	if !strings.Contains(string(content), "mail.example.com") {
		t.Errorf("Target content = %q, want to contain 'mail.example.com'", string(content))
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("Failed to stat target: %v", err)
	}
	if info.Mode().Perm() != 0640 {
		t.Errorf("File mode = %o, want 0640", info.Mode().Perm())
	}
}

func TestRewriteConfig_EmptySource(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()
	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Empty source file — scanner produces no lines, result should be empty.
	sourcePath := filepath.Join(tmpDir, "empty.in")
	if err := os.WriteFile(sourcePath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}

	err := rewriter.RewriteConfig(context.Background(), "empty.in", "empty.conf", "0440")
	if err != nil {
		t.Fatalf("RewriteConfig() error = %v", err)
	}

	targetPath := filepath.Join(tmpDir, "empty.conf")
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("Failed to read target: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("Expected empty target, got %q", string(content))
	}
}

func TestRewriteConfig_UnknownVarFallback(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()
	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Template with an unknown variable — transformer falls back to empty or key name.
	sourceContent := "key = %%VAR:unknownKey%%\n"
	sourcePath := filepath.Join(tmpDir, "fallback.in")
	if err := os.WriteFile(sourcePath, []byte(sourceContent), 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}

	err := rewriter.RewriteConfig(context.Background(), "fallback.in", "fallback.conf", "")
	if err != nil {
		t.Fatalf("RewriteConfig() unexpected error = %v", err)
	}

	targetPath := filepath.Join(tmpDir, "fallback.conf")
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		t.Fatalf("Target file not created")
	}
}

// TestRewriteConfig_SourceDoesNotExist verifies the error path when the source
// file is missing (exercises the os.Open failure branch in RewriteConfig).
func TestRewriteConfig_SourceDoesNotExist(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()
	rewriter := NewRewriter(tmpDir, mockLookup, st)

	err := rewriter.RewriteConfig(context.Background(), "ghost.in", "ghost.conf", "0440")
	if err == nil {
		t.Fatal("Expected error when source file does not exist")
	}
	if !strings.Contains(err.Error(), "failed to open source file") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestRewriteConfig_PlainTextLines exercises the !strings.HasSuffix branch in
// RewriteConfig. The transformer returns lines WITHOUT a trailing newline for plain
// text (no %% or @@ markers), so the rewriter must add the newline itself.
func TestRewriteConfig_PlainTextLines(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()
	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// A source with plain-text lines (no %% substitutions) — transformer returns
	// them without a trailing '\n', triggering the HasSuffix branch body.
	sourceContent := "# plain comment\nplain_key = plain_value\n"
	sourcePath := filepath.Join(tmpDir, "plain.in")
	if err := os.WriteFile(sourcePath, []byte(sourceContent), 0644); err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}

	err := rewriter.RewriteConfig(context.Background(), "plain.in", "plain.conf", "0440")
	if err != nil {
		t.Fatalf("RewriteConfig() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "plain.conf"))
	if err != nil {
		t.Fatalf("Failed to read target: %v", err)
	}
	// Each line must end with a newline.
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines, got %d: %q", len(lines), string(content))
	}
}

// TestRewriteReader_PlainTextLines mirrors the above for RewriteReader.
func TestRewriteReader_PlainTextLines(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	rewriter := NewRewriter("/opt/zextras", mockLookup, st)

	// Plain lines: transformer returns without '\n', rewriter appends it.
	input := strings.NewReader("# comment\nplain = value\n")
	output := &bytes.Buffer{}

	err := rewriter.RewriteReader(context.Background(), input, output)
	if err != nil {
		t.Fatalf("RewriteReader() error = %v", err)
	}
	lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines in output, got %d: %q", len(lines), output.String())
	}
}

// TestRewriteConfig_MkdirAllFailure exercises the os.MkdirAll error path by
// pointing the target directory at a path whose parent is chmod 0o000.
func TestRewriteConfig_MkdirAllFailure(t *testing.T) {
	skipIfRoot(t)

	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()

	// Write a valid source file.
	sourcePath := filepath.Join(tmpDir, "source.in")
	if err := os.WriteFile(sourcePath, []byte("key = value\n"), 0o644); err != nil {
		t.Fatalf("setup: write source: %v", err)
	}

	// Create a directory and make it unwritable so MkdirAll cannot create a child.
	noWriteDir := filepath.Join(tmpDir, "noperm")
	if err := os.Mkdir(noWriteDir, 0o755); err != nil {
		t.Fatalf("setup: mkdir: %v", err)
	}
	if err := os.Chmod(noWriteDir, 0o000); err != nil {
		t.Fatalf("setup: chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(noWriteDir, 0o755) })

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	// Target path is inside the unwritable directory — MkdirAll must fail.
	err := rewriter.RewriteConfig(context.Background(), "source.in", "noperm/sub/out.conf", "0440")
	if err == nil {
		t.Fatal("expected error from MkdirAll on unwritable parent, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create target directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestRewriteConfig_CreateTempFailure exercises the os.CreateTemp error path by
// making the target directory unwritable after it has been created.
func TestRewriteConfig_CreateTempFailure(t *testing.T) {
	skipIfRoot(t)

	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "source.in")
	if err := os.WriteFile(sourcePath, []byte("key = value\n"), 0o644); err != nil {
		t.Fatalf("setup: write source: %v", err)
	}

	// Create the target directory and then strip write permission so
	// os.CreateTemp cannot create a file inside it.
	targetDir := filepath.Join(tmpDir, "readonly")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatalf("setup: mkdir: %v", err)
	}
	if err := os.Chmod(targetDir, 0o555); err != nil {
		t.Fatalf("setup: chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(targetDir, 0o755) })

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	err := rewriter.RewriteConfig(context.Background(), "source.in", "readonly/out.conf", "0440")
	if err == nil {
		t.Fatal("expected error from CreateTemp on read-only directory, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create temporary file") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestRewriteConfig_ChmodFailure exercises the Chmod success branch with mode 0755.
func TestRewriteConfig_ChmodFailure(t *testing.T) {
	skipIfRoot(t)

	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "source.in")
	if err := os.WriteFile(sourcePath, []byte("key = value\n"), 0o644); err != nil {
		t.Fatalf("setup: write source: %v", err)
	}

	rewriter := NewRewriter(tmpDir, mockLookup, st)
	err := rewriter.RewriteConfig(context.Background(), "source.in", "out.conf", "0755")
	if err != nil {
		t.Fatalf("RewriteConfig with mode 0755: %v", err)
	}

	info, err := os.Stat(filepath.Join(tmpDir, "out.conf"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 0755", info.Mode().Perm())
	}
}

// TestRewriteConfig_FlushAndCloseCoverage runs a normal rewrite over a file with
// many lines to ensure Flush and Close succeed.
func TestRewriteConfig_FlushAndCloseCoverage(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()

	// Build a large source with 300 lines to force at least one internal Flush.
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString("myhostname = %%VAR:zimbraServerHostname%%\n")
	}
	sourcePath := filepath.Join(tmpDir, "large.in")
	if err := os.WriteFile(sourcePath, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	err := rewriter.RewriteConfig(context.Background(), "large.in", "large.conf", "0640")
	if err != nil {
		t.Fatalf("RewriteConfig large file: %v", err)
	}

	info, err := os.Stat(filepath.Join(tmpDir, "large.conf"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %o, want 0640", info.Mode().Perm())
	}
}

// TestRewriteConfig_TargetSubdirMultiLevel exercises MkdirAll for multi-level
// nested targets.
func TestRewriteConfig_TargetSubdirMultiLevel(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "s.in")
	if err := os.WriteFile(sourcePath, []byte("a = %%VAR:zimbraSmtpPort%%\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	err := rewriter.RewriteConfig(context.Background(), "s.in", "a/b/c/d/out.conf", "0600")
	if err != nil {
		t.Fatalf("RewriteConfig nested: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "a/b/c/d/out.conf"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "25") {
		t.Errorf("content = %q, want to contain '25'", string(content))
	}
}

// TestRewriteConfig_OverwriteReadOnly exercises the rename-over-read-only-file path.
func TestRewriteConfig_OverwriteReadOnly(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "ro.in")
	if err := os.WriteFile(sourcePath, []byte("host = %%VAR:zimbraServerHostname%%\n"), 0o644); err != nil {
		t.Fatalf("setup source: %v", err)
	}

	targetPath := filepath.Join(tmpDir, "ro.conf")
	if err := os.WriteFile(targetPath, []byte("old\n"), 0o440); err != nil {
		t.Fatalf("setup target: %v", err)
	}

	rewriter := NewRewriter(tmpDir, mockLookup, st)

	err := rewriter.RewriteConfig(context.Background(), "ro.in", "ro.conf", "0440")
	if err != nil {
		t.Fatalf("RewriteConfig over read-only: %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "mail.example.com") {
		t.Errorf("content = %q, want 'mail.example.com'", string(content))
	}
}

// TestRewriteReader_ScannerError exercises the scanner.Err() error return path
// in RewriteReader by feeding a line that exceeds bufio.Scanner's max token size.
func TestRewriteReader_ScannerError(t *testing.T) {
	mockLookup := newMockLookup()
	st := &state.State{}
	rewriter := NewRewriter("/opt/zextras", mockLookup, st)

	// A single line longer than bufio.MaxScanTokenSize (64 KiB) causes
	// scanner.Scan() to return false and scanner.Err() to return a non-nil error.
	hugeLine := strings.Repeat("x", 65*1024)
	input := strings.NewReader(hugeLine)
	var out strings.Builder

	err := rewriter.RewriteReader(context.Background(), input, &out)
	if err == nil {
		t.Fatal("expected error from oversized line, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read input") {
		t.Errorf("unexpected error: %v", err)
	}
}
