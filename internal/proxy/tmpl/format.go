// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tmpl

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// indentUnit is one level of indentation.
const indentUnit = "    "

// FormatOptions tune Format.
type FormatOptions struct {
	// IsEnabler reports whether name is an enabler variable. Only the prefixes
	// of enabler variables are separated from the text they guard; any other
	// ${name} at the start of a line is text, whose whitespace is significant.
	// Nil means no variable is an enabler.
	IsEnabler func(name string) bool
}

// vanishingDirective matches a directive whose values are all variables: when
// they render empty, the generator comments the line out, but only if it is
// indented (EmptyDirectivePattern), so the indentation of such a line is
// significant.
var vanishingDirective = regexp.MustCompile(`^[a-z0-9_]+(?:\s+\$\{[^}]+\})+\s*;`)

// fline is one formatted line.
type fline struct {
	raw      string // the original line, trailing whitespace trimmed
	verbatim bool   // inside a multi-line quote: left as is

	prefixes []string // enabler prefixes
	body     string   // the text after the prefixes, trimmed
	level    int      // indentation level

	// Set for a directive ("name value;") that takes part in alignment.
	directive  bool
	name, rest string
	gap        int // columns between name and rest in the source
}

// Format re-indents and aligns a template: nginx text is indented by brace
// depth (four spaces), the values of consecutive directives are aligned, and
// enabler prefixes stay at the start of the text they guard. Only whitespace
// changes, and the rendered configuration is the same up to whitespace. The
// first-line explode directive is left untouched. Format is idempotent.
func Format(src string, opts FormatOptions) string {
	lines := SplitLines(src)
	if len(lines) == 0 {
		return ""
	}

	var out strings.Builder

	if _, _, ok := ParseExplode(lines[0]); ok {
		out.WriteString(lines[0])
		out.WriteByte('\n')

		lines = lines[1:]
	}

	for _, l := range formatLines(lines, opts) {
		out.WriteString(l)
		out.WriteByte('\n')
	}

	return out.String()
}

func formatLines(lines []string, opts FormatOptions) []string {
	fl := classify(lines, opts)
	alignRuns(fl)

	out := make([]string, len(fl))
	for i := range fl {
		out[i] = fl[i].String()
	}

	return out
}

// String is the formatted line.
func (f *fline) String() string {
	switch {
	case f.verbatim:
		return f.raw
	case f.body == "" && len(f.prefixes) == 0:
		return ""
	case f.level < 0: // keeps the source indentation (see classify)
		return f.raw
	}

	var b strings.Builder

	b.WriteString(strings.Repeat(indentUnit, f.level))

	for _, p := range f.prefixes {
		b.WriteString("${" + p + "}")
	}

	b.WriteString(f.body)

	return b.String()
}

// classifier is the state of classify across lines.
type classifier struct {
	sc      scanner
	depth   int
	pending bool // the previous code line is an unfinished statement
}

// classify parses every line and assigns its indentation level.
func classify(lines []string, opts FormatOptions) []fline {
	var (
		c   classifier
		out = make([]fline, len(lines))
	)

	for i, line := range lines {
		c.classifyLine(&out[i], line, opts)
	}

	return out
}

// classifyLine parses one line into f and advances the state.
func (c *classifier) classifyLine(f *fline, line string, opts FormatOptions) {
	f.raw = strings.TrimRight(line, " \t\r")

	if c.sc.quote != 0 {
		ls := c.sc.scan(line)
		f.verbatim = true
		f.raw = strings.TrimSuffix(line, "\r")
		c.depth = max(c.depth+ls.opens-ls.closes, 0)

		return
	}

	var hadWS bool

	f.prefixes, f.body, hadWS = splitPrefixes(line, opts)
	ls := c.sc.scan(f.body)

	if c.sc.quote != 0 {
		// The quote runs on: trailing whitespace belongs to the string.
		f.body = strings.TrimLeft(f.body, " \t")
	} else {
		f.body = strings.TrimRight(f.body, " \t\r")
	}

	code := strings.TrimRight(ls.code, " \t\r")
	isComment := strings.HasPrefix(f.body, "#")
	level := max(c.depth-ls.leadClose, 0)

	if c.pending && continuesStatement(code, isComment, ls) {
		level++
	}

	f.level = level

	if !isComment && code != "" {
		c.pending = !finished(code)
	}

	c.depth = max(c.depth+ls.opens-ls.closes, 0)

	// An indented value-less directive is commented out when rendered, an
	// unindented one is not: keep the indentation's presence.
	if vanishingDirective.MatchString(f.body) && (level > 0) != hadWS {
		f.level = -1
	}

	if isDirective(code, isComment, ls) && f.level >= 0 {
		f.setDirective(code)
	}
}

// continuesStatement reports whether a line that follows an unfinished
// statement is its continuation: code that does not open or close a block.
func continuesStatement(code string, isComment bool, ls lineScan) bool {
	return !isComment && code != "" && !strings.HasPrefix(code, "{") && ls.leadClose == 0
}

// isDirective reports whether the line is a complete "name value;" directive.
func isDirective(code string, isComment bool, ls lineScan) bool {
	return code != "" && !isComment && ls.opens+ls.closes == 0 && strings.HasSuffix(code, ";")
}

// splitPrefixes splits the enabler prefixes off a line. hadWS reports whether
// the line had whitespace before its text.
func splitPrefixes(line string, opts FormatOptions) (prefixes []string, body string, hadWS bool) {
	rest := line

	for opts.IsEnabler != nil {
		m := EnablerPattern.FindStringSubmatch(rest)
		if len(m) == 0 || !opts.IsEnabler(m[2]) {
			break
		}

		hadWS = hadWS || m[1] != ""
		prefixes = append(prefixes, m[2])
		rest = m[3]
		trimmed := strings.TrimLeft(rest, " \t")
		hadWS = hadWS || trimmed != rest
		rest = trimmed
	}

	if len(prefixes) == 0 {
		hadWS = rest != "" && (rest[0] == ' ' || rest[0] == '\t')
		rest = strings.TrimLeft(rest, " \t")
	}

	return prefixes, rest, hadWS
}

// finished reports whether code ends a statement: ';', a brace, or nothing
// but variables (which expand to whole statements).
func finished(code string) bool {
	switch code[len(code)-1] {
	case ';', '{', '}':
		return true
	}

	return strings.TrimSpace(VarPattern.ReplaceAllString(code, "")) == ""
}

// setDirective records the name and the rest of a "name value;" line.
func (f *fline) setDirective(code string) {
	end := 0

	if code[0] == '"' || code[0] == '\'' {
		end = strings.IndexByte(code[1:], code[0])
		if end < 0 {
			return
		}

		end += 2
	}

	for end < len(code) && code[end] != ' ' && code[end] != '\t' {
		end++
	}

	name := code[:end]
	rest := strings.TrimLeft(f.body[end:], " \t")

	if end >= len(code) || rest == "" || rest == ";" || strings.Contains(name, "${") || strings.Contains(name, ";") {
		return
	}

	f.directive = true
	f.name = name
	f.rest = rest
	f.gap = len(f.body) - end - len(rest)
}

// alignRuns aligns the values of each run of consecutive directives at the
// same level. A run that is already aligned keeps its column, so authors may
// choose a wider one; otherwise the most used column wins when it fits the
// longest name, or else the column right after the longest name.
func alignRuns(fl []fline) {
	for i := 0; i < len(fl); {
		if !fl[i].directive {
			i++

			continue
		}

		j := i
		for j < len(fl) && fl[j].directive && fl[j].level == fl[i].level {
			j++
		}

		alignRun(fl[i:j])

		i = j
	}
}

func alignRun(run []fline) {
	minCol := 0
	counts := map[int]int{}

	for i := range run {
		f := &run[i]
		w := utf8.RuneCountInString(f.name)
		minCol = max(minCol, w+1)
		counts[w+f.gap]++
	}

	col := minCol
	best := 0

	for c, n := range counts {
		if c >= minCol && (n > best || (n == best && c < col)) {
			col, best = c, n
		}
	}

	for i := range run {
		f := &run[i]
		f.body = f.name + strings.Repeat(" ", col-utf8.RuneCountInString(f.name)) + f.rest
	}
}
