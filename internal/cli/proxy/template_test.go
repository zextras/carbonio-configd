// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/sys"
)

const (
	messy = "server\n{\n  ${core.ipboth.enabled}listen   1;\n      b  2;\n}\n"
	clean = "server\n{\n    ${core.ipboth.enabled}listen 1;\n    b      2;\n}\n"
)

func runTemplateCmd(t *testing.T, action Action, opts *TemplateOptions) (code int, stdout, stderr string) {
	t.Helper()

	var out, errb bytes.Buffer

	code = Run(context.Background(), sys.Real(), &out, &errb, &Options{Action: action, Template: opts})

	return code, out.String(), errb.String()
}

func writeTemplate(t *testing.T, dir, name, content string) string {
	t.Helper()

	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func TestTemplateFmt_CheckListsAndDiffs(t *testing.T) {
	dir := t.TempDir()
	bad := writeTemplate(t, dir, "a.template", messy)
	writeTemplate(t, dir, "b.template", clean)
	writeTemplate(t, dir, "c.txt", messy) // not a template: skipped in a directory

	code, stdout, stderr := runTemplateCmd(t, ActionTemplateFmt, &TemplateOptions{Paths: []string{dir}, Check: true})

	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}

	if !strings.Contains(stdout, "--- "+bad) || !strings.Contains(stdout, "+    b      2;") {
		t.Errorf("no diff of the messy file: %q", stdout)
	}

	if strings.Contains(stdout, "b.template") || strings.Contains(stderr, "c.txt") {
		t.Errorf("clean or non-template file reported: %q %q", stdout, stderr)
	}

	if want := bad + ": needs formatting\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}

	if got, _ := os.ReadFile(bad); string(got) != messy {
		t.Error("--check modified the file")
	}
}

func TestTemplateFmt_CheckClean(t *testing.T) {
	p := writeTemplate(t, t.TempDir(), "a.template", clean)

	if code, out, _ := runTemplateCmd(t, ActionTemplateFmt, &TemplateOptions{Paths: []string{p}, Check: true}); code != 0 || out != "" {
		t.Errorf("code=%d out=%q, want 0 and nothing", code, out)
	}
}

func TestTemplateFmt_Write(t *testing.T) {
	p := writeTemplate(t, t.TempDir(), "a.template", messy)

	code, stdout, _ := runTemplateCmd(t, ActionTemplateFmt, &TemplateOptions{Paths: []string{p}, Write: true})
	if code != 0 || stdout != p+"\n" {
		t.Errorf("code=%d stdout=%q", code, stdout)
	}

	if got, _ := os.ReadFile(p); string(got) != clean {
		t.Errorf("file = %q, want %q", got, clean)
	}

	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestTemplateFmt_StdoutAndErrors(t *testing.T) {
	dir := t.TempDir()
	a := writeTemplate(t, dir, "a.template", messy)
	b := writeTemplate(t, dir, "b.template", messy)

	if code, out, _ := runTemplateCmd(t, ActionTemplateFmt, &TemplateOptions{Paths: []string{a}}); code != 0 || out != clean {
		t.Errorf("single file to stdout: code=%d out=%q", code, out)
	}

	for name, opts := range map[string]*TemplateOptions{
		"several files without a mode": {Paths: []string{a, b}},
		"check and write":              {Paths: []string{a}, Check: true, Write: true},
		"missing path":                 {Paths: []string{filepath.Join(dir, "nope")}},
		"no templates":                 {Paths: []string{t.TempDir()}},
	} {
		if code, _, stderr := runTemplateCmd(t, ActionTemplateFmt, opts); code != 1 || stderr == "" {
			t.Errorf("%s: code=%d stderr=%q, want 1 and a message", name, code, stderr)
		}
	}
}

func TestTemplateLint(t *testing.T) {
	dir := t.TempDir()
	good := writeTemplate(t, dir, "good.template", clean)
	bad := writeTemplate(t, dir, "bad.template", "a ${no.such.var};\nb; \n")

	code, stdout, stderr := runTemplateCmd(t, ActionTemplateLint, &TemplateOptions{Paths: []string{good}})
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("clean: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runTemplateCmd(t, ActionTemplateLint, &TemplateOptions{Paths: []string{dir}})
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}

	for _, want := range []string{
		bad + ":1:3: unknown variable ${no.such.var} [unknown-variable]",
		bad + ":2:3: trailing whitespace [whitespace]",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	if !strings.Contains(stderr, "2 finding(s) in 1 of 2 file(s)") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestTemplateRender(t *testing.T) {
	dir := t.TempDir()
	tdir := filepath.Join(dir, "templates")

	if err := os.Mkdir(tdir, 0o750); err != nil {
		t.Fatal(err)
	}

	writeTemplate(t, tdir, "nginx.conf.web.template", "x ${web.https.port};\n${core.ipv4only.enabled}v4;\n")
	values := writeTemplate(t, dir, "values.yaml", "global:\n  zimbraIPMode: ipv4\n  zimbraMailSSLPort: 9443\n")
	out := filepath.Join(dir, "out")

	code, stdout, stderr := runTemplateCmd(t, ActionTemplateRender, &TemplateOptions{Values: values, Templates: tdir, Out: out})
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}

	rendered := filepath.Join(out, "nginx", "includes", "nginx.conf.web")
	if stdout != rendered+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, rendered)
	}

	got, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(got), "\nv4;\n") {
		t.Errorf("ipv4only line not enabled:\n%s", got)
	}

	for name, opts := range map[string]*TemplateOptions{
		"missing flags":   {Values: values},
		"missing fixture": {Values: filepath.Join(dir, "nope.yaml"), Templates: tdir, Out: out},
		"bad fixture":     {Values: writeTemplate(t, dir, "bad.yaml", "bogus: 1\n"), Templates: tdir, Out: out},
		"no templates":    {Values: values, Templates: t.TempDir(), Out: out},
	} {
		if code, _, stderr := runTemplateCmd(t, ActionTemplateRender, opts); code != 1 || stderr == "" {
			t.Errorf("%s: code=%d stderr=%q, want 1 and a message", name, code, stderr)
		}
	}
}
