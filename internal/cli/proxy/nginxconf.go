// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zextras/carbonio-configd/internal/sys"
)

var (
	nginxWorkingDirRe = regexp.MustCompile(`^\s*working_directory\s+([^\s;]+)\s*;`)
	nginxIncludeRe    = regexp.MustCompile(`^\s*include\s+([^\s;]+)\s*;`)
)

// nginxConfOpts holds options for printNginxConf.
type nginxConfOpts struct {
	fs         sys.FS
	markers    bool
	indent     bool
	noComments bool
	noEmpty    bool
	workingDir string
}

// printNginxConf prints the nginx config at filename, expanding glob patterns and
// recursively following include directives — mirroring zmproxyconf behaviour.
func printNginxConf(w io.Writer, filename string, depth int, opts *nginxConfOpts) {
	matches, err := opts.fs.Glob(filename)
	if err != nil || len(matches) == 0 {
		matches = []string{filename}
	}

	for _, f := range matches {
		printNginxConfFile(w, f, depth, opts)
	}
}

func printNginxConfFile(w io.Writer, filename string, depth int, opts *nginxConfOpts) {
	prefix := ""
	if opts.indent {
		prefix = strings.Repeat("  ", depth)
	}

	if opts.markers {
		_, _ = fmt.Fprintf(w, "%s# begin:%s\n", prefix, filename)
	}

	f, err := opts.fs.Open(filename) //nolint:gosec // caller-supplied path
	if err != nil {
		_, _ = fmt.Fprintf(w, "%s# cannot open %s: %v\n", prefix, filename, err)

		if opts.markers {
			_, _ = fmt.Fprintf(w, "%s# end:%s\n", prefix, filename)
		}

		return
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()

		// Track working_directory — nginx uses it as base for relative include paths.
		if m := nginxWorkingDirRe.FindStringSubmatch(line); len(m) > 1 {
			opts.workingDir = m[1]
		}

		// Recursively follow include directives.
		if m := nginxIncludeRe.FindStringSubmatch(line); len(m) > 1 {
			printNginxInclude(w, m[1], prefix, depth, opts)
			continue
		}

		printNginxLine(w, line, prefix, opts)
	}

	if opts.markers {
		_, _ = fmt.Fprintf(w, "%s# end:%s\n", prefix, filename)
	}
}

// printNginxInclude handles a single nginx include directive, resolving the path
// relative to workingDir when needed and recursing into the included file.
func printNginxInclude(w io.Writer, includePath, prefix string, depth int, opts *nginxConfOpts) {
	if opts.workingDir == "" {
		_, _ = fmt.Fprintf(w, "%s# working directory not defined while including %s\n",
			prefix, includePath)

		return
	}

	if !filepath.IsAbs(includePath) {
		includePath = filepath.Join(opts.workingDir, includePath)
	}

	printNginxConf(w, includePath, depth+1, opts)
}

// printNginxLine writes a single config line to w, applying comment/empty filters.
func printNginxLine(w io.Writer, line, prefix string, opts *nginxConfOpts) {
	trimmed := strings.TrimSpace(line)

	if opts.noComments && strings.HasPrefix(trimmed, "#") {
		return
	}

	if opts.noEmpty && trimmed == "" {
		return
	}

	_, _ = fmt.Fprintf(w, "%s%s\n", prefix, line)
}
