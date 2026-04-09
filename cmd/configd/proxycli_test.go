// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintNginxLine_Plain(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{}
	printNginxLine(&buf, "server_name example.com;", "", opts)
	got := buf.String()
	if got != "server_name example.com;\n" {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestPrintNginxLine_WithPrefix(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{}
	printNginxLine(&buf, "listen 80;", "  ", opts)
	got := buf.String()
	if got != "  listen 80;\n" {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestPrintNginxLine_SkipComment(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{noComments: true}
	printNginxLine(&buf, "# this is a comment", "", opts)
	if buf.Len() != 0 {
		t.Errorf("expected comment line to be skipped, got %q", buf.String())
	}
}

func TestPrintNginxLine_KeepComment(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{noComments: false}
	printNginxLine(&buf, "# keep me", "", opts)
	if !strings.Contains(buf.String(), "# keep me") {
		t.Errorf("expected comment to be kept, got %q", buf.String())
	}
}

func TestPrintNginxLine_SkipEmpty(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{noEmpty: true}
	printNginxLine(&buf, "   ", "", opts)
	if buf.Len() != 0 {
		t.Errorf("expected empty line to be skipped, got %q", buf.String())
	}
}

func TestPrintNginxLine_KeepEmpty(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{noEmpty: false}
	printNginxLine(&buf, "", "", opts)
	if buf.String() != "\n" {
		t.Errorf("expected empty line to produce newline, got %q", buf.String())
	}
}

func TestPrintNginxConfFile_FileNotFound(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{}
	// Non-existent file: should print an error comment, not panic
	printNginxConfFile(&buf, "/nonexistent/path/nginx.conf", 0, opts)
	got := buf.String()
	if !strings.Contains(got, "cannot open") {
		t.Errorf("expected error comment for missing file, got %q", got)
	}
}

func TestPrintNginxConfFile_WithMarkers(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "nginx.conf")
	_ = os.WriteFile(f, []byte("worker_processes 1;\n"), 0o600)

	var buf bytes.Buffer
	opts := &nginxConfOpts{markers: true}
	printNginxConfFile(&buf, f, 0, opts)
	got := buf.String()

	if !strings.Contains(got, "# begin:") {
		t.Errorf("expected begin marker, got %q", got)
	}
	if !strings.Contains(got, "# end:") {
		t.Errorf("expected end marker, got %q", got)
	}
	if !strings.Contains(got, "worker_processes 1;") {
		t.Errorf("expected file content, got %q", got)
	}
}

func TestPrintNginxConfFile_WithIndent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "nginx.conf")
	_ = os.WriteFile(f, []byte("server_name example.com;\n"), 0o600)

	var buf bytes.Buffer
	opts := &nginxConfOpts{indent: true}
	printNginxConfFile(&buf, f, 2, opts)
	got := buf.String()

	// depth=2 means 4 spaces prefix
	if !strings.Contains(got, "    server_name") {
		t.Errorf("expected indented content, got %q", got)
	}
}

func TestPrintNginxConfFile_WorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "nginx.conf")
	content := "working_directory " + dir + ";\n"
	_ = os.WriteFile(f, []byte(content), 0o600)

	var buf bytes.Buffer
	opts := &nginxConfOpts{}
	printNginxConfFile(&buf, f, 0, opts)

	// workingDir should be populated after parsing
	if opts.workingDir != dir {
		t.Errorf("expected workingDir=%q, got %q", dir, opts.workingDir)
	}
}

func TestPrintNginxInclude_NoWorkingDir(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{workingDir: ""}
	// Without workingDir, should print a comment
	printNginxInclude(&buf, "relative/path.conf", "", 0, opts)
	got := buf.String()
	if !strings.Contains(got, "working directory not defined") {
		t.Errorf("expected warning comment, got %q", got)
	}
}

func TestPrintNginxInclude_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	included := filepath.Join(dir, "included.conf")
	_ = os.WriteFile(included, []byte("include_content;\n"), 0o600)

	var buf bytes.Buffer
	opts := &nginxConfOpts{workingDir: dir}
	printNginxInclude(&buf, included, "", 0, opts)
	got := buf.String()
	if !strings.Contains(got, "include_content") {
		t.Errorf("expected included file content, got %q", got)
	}
}

func TestPrintNginxInclude_RelativePath(t *testing.T) {
	dir := t.TempDir()
	included := filepath.Join(dir, "sub.conf")
	_ = os.WriteFile(included, []byte("sub_content;\n"), 0o600)

	var buf bytes.Buffer
	opts := &nginxConfOpts{workingDir: dir}
	// Pass relative path — should be joined with workingDir
	printNginxInclude(&buf, "sub.conf", "", 0, opts)
	got := buf.String()
	if !strings.Contains(got, "sub_content") {
		t.Errorf("expected included file content via relative path, got %q", got)
	}
}

func TestPrintNginxConf_GlobNoMatch(t *testing.T) {
	var buf bytes.Buffer
	opts := &nginxConfOpts{}
	// Non-matching glob — falls back to the pattern itself, which won't open
	printNginxConf(&buf, "/nonexistent/*.conf", 0, opts)
	got := buf.String()
	// Should produce an error comment for the unmatched file
	if !strings.Contains(got, "cannot open") {
		t.Errorf("expected error comment for glob with no matches, got %q", got)
	}
}

func TestPrintNginxConf_GlobWithMatch(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.conf")
	f2 := filepath.Join(dir, "b.conf")
	_ = os.WriteFile(f1, []byte("content_a;\n"), 0o600)
	_ = os.WriteFile(f2, []byte("content_b;\n"), 0o600)

	var buf bytes.Buffer
	opts := &nginxConfOpts{}
	printNginxConf(&buf, filepath.Join(dir, "*.conf"), 0, opts)
	got := buf.String()

	if !strings.Contains(got, "content_a") {
		t.Errorf("expected content_a in output, got %q", got)
	}
	if !strings.Contains(got, "content_b") {
		t.Errorf("expected content_b in output, got %q", got)
	}
}

func TestProxyProtocols_AllPresent(t *testing.T) {
	expected := []string{"http", "https", "mail", "imap", "imaps", "pop3", "pop3s"}
	for _, proto := range expected {
		if _, ok := proxyProtocols[proto]; !ok {
			t.Errorf("expected protocol %q in proxyProtocols map", proto)
		}
	}
}

func TestProxyProtocols_Count(t *testing.T) {
	if len(proxyProtocols) != 7 {
		t.Errorf("expected 7 proxy protocols, got %d", len(proxyProtocols))
	}
}

func TestNginxWorkingDirRegex(t *testing.T) {
	tests := []struct {
		line    string
		wantDir string
	}{
		{"  working_directory /opt/zextras/conf;", "/opt/zextras/conf"},
		{"working_directory /var/log;", "/var/log"},
		{"worker_processes 1;", ""},
	}

	for _, tt := range tests {
		m := nginxWorkingDirRe.FindStringSubmatch(tt.line)
		if tt.wantDir == "" {
			if len(m) > 1 {
				t.Errorf("line %q: expected no match, got %q", tt.line, m[1])
			}
		} else {
			if len(m) < 2 || m[1] != tt.wantDir {
				t.Errorf("line %q: expected dir %q, got %v", tt.line, tt.wantDir, m)
			}
		}
	}
}

func TestNginxIncludeRegex(t *testing.T) {
	tests := []struct {
		line     string
		wantPath string
	}{
		{"  include /opt/zextras/conf/*.conf;", "/opt/zextras/conf/*.conf"},
		{"include conf.d/default;", "conf.d/default"},
		{"server_name example.com;", ""},
	}

	for _, tt := range tests {
		m := nginxIncludeRe.FindStringSubmatch(tt.line)
		if tt.wantPath == "" {
			if len(m) > 1 {
				t.Errorf("line %q: expected no match, got %q", tt.line, m[1])
			}
		} else {
			if len(m) < 2 || m[1] != tt.wantPath {
				t.Errorf("line %q: expected path %q, got %v", tt.line, tt.wantPath, m)
			}
		}
	}
}
