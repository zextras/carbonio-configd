// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zextras/carbonio-configd/internal/cli/cliutil"
	proxygen "github.com/zextras/carbonio-configd/internal/proxy"
	"github.com/zextras/carbonio-configd/internal/proxy/tmpl"
	"github.com/zextras/carbonio-configd/internal/sys"
)

// templateSuffix is the extension of a proxy template file.
const templateSuffix = ".template"

// TemplateOptions are the flags of "configd proxy template fmt|lint|render".
type TemplateOptions struct {
	// Paths are the template files or directories to format or lint.
	Paths []string
	// Check (fmt) lists and diffs the files that would change, writing nothing.
	Check bool
	// Write (fmt) rewrites the files that would change.
	Write bool
	// Values, Templates and Out (render) are the fixture, the template
	// directory and the directory the files are written to.
	Values, Templates, Out string
}

// runTemplate runs a "configd proxy template" action. None needs the zextras
// user: they read and write only the paths they are given.
func runTemplate(ctx context.Context, env sys.System, stdout, stderr io.Writer, opts *Options) int {
	cliutil.InitCLILogging(stderr)

	t := opts.Template
	if t == nil {
		t = &TemplateOptions{}
	}

	switch opts.Action {
	case ActionTemplateFmt:
		return templateFmt(env.FS, stdout, stderr, t)
	case ActionTemplateLint:
		return templateLint(env.FS, stdout, stderr, t)
	default:
		return templateRender(ctx, env, stdout, stderr, t)
	}
}

// templateFiles expands paths: a file is taken as is, a directory gives its
// *.template files, recursively, sorted.
func templateFiles(fsys sys.FS, paths []string) ([]string, error) {
	var files []string

	for _, p := range paths {
		info, err := fsys.Stat(p)
		if err != nil {
			return nil, err
		}

		if !info.IsDir() {
			files = append(files, p)

			continue
		}

		err = fsys.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, templateSuffix) {
				files = append(files, path)
			}

			return err
		})
		if err != nil {
			return nil, err
		}
	}

	return files, nil
}

func templateFmt(fsys sys.FS, stdout, stderr io.Writer, t *TemplateOptions) int {
	if t.Check && t.Write {
		_, _ = fmt.Fprintln(stderr, "Error: --check and --write are exclusive")

		return 1
	}

	files, err := templateFiles(fsys, t.Paths)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	if len(files) == 0 {
		_, _ = fmt.Fprintln(stderr, "Error: no template files")

		return 1
	}

	if !t.Check && !t.Write && len(files) != 1 {
		_, _ = fmt.Fprintln(stderr, "Error: formatting several files needs --check or --write")

		return 1
	}

	var changed []string

	for _, f := range files {
		didChange, err := fmtFile(fsys, stdout, f, t)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

			return 1
		}

		if didChange {
			changed = append(changed, f)
		}
	}

	if t.Check && len(changed) > 0 {
		for _, f := range changed {
			_, _ = fmt.Fprintf(stderr, "%s: needs formatting\n", f)
		}

		return 1
	}

	return 0
}

// fmtFile formats one file as t asks: its diff with --check, rewritten with
// --write, its formatted text on stdout with neither. changed reports whether
// the file needs formatting (and, with neither flag, is always false).
func fmtFile(fsys sys.FS, stdout io.Writer, f string, t *TemplateOptions) (changed bool, err error) {
	src, err := fsys.ReadFile(f)
	if err != nil {
		return false, err
	}

	formatted := tmpl.Format(string(src), tmpl.FormatOptions{IsEnabler: proxygen.IsEnabler})

	switch {
	case !t.Check && !t.Write:
		_, err = io.WriteString(stdout, formatted)

		return false, err
	case formatted == string(src):
		return false, nil
	case t.Check:
		_, err = io.WriteString(stdout, tmpl.Diff(f, string(src), formatted))

		return true, err
	}

	if err := writeKeepingMode(fsys, f, formatted); err != nil {
		return false, err
	}

	_, err = fmt.Fprintln(stdout, f)

	return true, err
}

// writeKeepingMode replaces a file's content, keeping its permissions.
func writeKeepingMode(fsys sys.FS, path, content string) error {
	info, err := fsys.Stat(path)
	if err != nil {
		return err
	}

	return fsys.WriteFileAtomic(path, []byte(content), info.Mode().Perm())
}

func templateLint(fsys sys.FS, stdout, stderr io.Writer, t *TemplateOptions) int {
	files, err := templateFiles(fsys, t.Paths)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	if len(files) == 0 {
		_, _ = fmt.Fprintln(stderr, "Error: no template files")

		return 1
	}

	findings, dirty := 0, 0

	for _, f := range files {
		src, err := fsys.ReadFile(f)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

			return 1
		}

		found := tmpl.Lint(f, string(src), proxygen.TemplateRegistry{})
		for _, finding := range found {
			_, _ = fmt.Fprintln(stdout, finding)
		}

		findings += len(found)

		if len(found) > 0 {
			dirty++
		}
	}

	if findings > 0 {
		_, _ = fmt.Fprintf(stderr, "%d finding(s) in %d of %d file(s)\n", findings, dirty, len(files))

		return 1
	}

	return 0
}

func templateRender(ctx context.Context, env sys.System, stdout, stderr io.Writer, t *TemplateOptions) int {
	if t.Values == "" || t.Templates == "" || t.Out == "" {
		_, _ = fmt.Fprintln(stderr, "Error: render needs --values, --templates and --out")

		return 1
	}

	data, err := env.FS.ReadFile(t.Values)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	fx, err := proxygen.ParseFixture(data)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %s: %v\n", t.Values, err)

		return 1
	}

	out, err := filepath.Abs(t.Out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	written, err := proxygen.RenderOffline(ctx, env, fx, t.Templates, out)
	slices.Sort(written)

	for _, f := range written {
		_, _ = fmt.Fprintln(stdout, filepath.Join(t.Out, f))
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)

		return 1
	}

	return 0
}
