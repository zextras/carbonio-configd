// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tmpl

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// indentUnit is one level of indentation.
const indentUnit = "    "

// FormatOptions tune Format.
type FormatOptions struct {
	// IsSwitch reports whether name is a line switch: a variable that renders
	// as "" or "#" and so switches on or off the line it prefixes (an enabler
	// variable, or a text variable such as web.strict.servername). Only the
	// prefixes that are switches are separated from the text they guard; any
	// other ${name} at the start of a line is text, whose whitespace is
	// significant. Nil means no variable is a switch.
	IsSwitch func(name string) bool

	// LeadingSpace reports whether the value of name renders with one leading
	// space (web.ssl.protocols renders " TLSv1.2 TLSv1.3"). A directive whose
	// value starts with such a variable gets one space less when aligned, so
	// that the rendered values line up. Nil means no variable has one.
	LeadingSpace func(name string) bool
}

// vanishingDirective matches a directive whose values are all variables: when
// they render empty, the generator comments the line out, but only if it is
// indented (EmptyDirectivePattern), so the indentation of such a line is
// significant.
var vanishingDirective = regexp.MustCompile(`^(?:\$\{[^}]+\})*[a-z0-9_]+(?:\s+\$\{[^}]+\})+\s*;`)

// onlyVariables matches a line made of variables alone.
var onlyVariables = regexp.MustCompile(`^(?:\$\{[^}]+\})+$`)

// fline is one formatted line.
type fline struct {
	raw      string // the original line, trailing whitespace trimmed
	verbatim bool   // inside a multi-line quote: left as is

	prefixes []string // line-switch prefixes
	col0     bool     // the first prefix is at column 0: the indent follows it
	body     string   // the text after the prefixes, trimmed
	level    int      // indentation level

	// Set for a directive ("name value;") that takes part in alignment.
	directive  bool
	name, rest string
	gap        int  // columns between name and rest in the source
	lead       bool // the value renders with one leading space
}

// Format re-indents and aligns a template. The result is the rendered nginx
// configuration laid out cleanly:
//
//   - Text is indented by brace depth, four spaces a level. Braces in quotes,
//     in comments, escaped, or inside ${...} do not count.
//   - Line-switch prefixes (${on}listen ..., see FormatOptions.IsSwitch) stay
//     where the source has them: a prefix at column 0 stays at column 0 and
//     the indent follows it; an indented prefix is preceded by the indent.
//   - Directives are aligned by group, see the rules on alignRuns.
//
// Only whitespace changes, so the rendered configuration is the same up to
// whitespace. The first-line explode directive is left untouched. Format is
// idempotent.
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

// maxPasses bounds the passes of formatLines; a template settles in two or
// three.
const maxPasses = 16

// formatLines formats lines until a pass changes nothing: aligning a group can
// bring a neighbour into its reach (see alignRuns).
func formatLines(lines []string, opts FormatOptions) []string {
	for range maxPasses {
		out := formatPass(lines, opts)
		if slices.Equal(out, lines) {
			return out
		}

		lines = out
	}

	return lines
}

func formatPass(lines []string, opts FormatOptions) []string {
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

	indent := strings.Repeat(indentUnit, f.level)

	if !f.col0 {
		b.WriteString(indent)
	}

	for i, p := range f.prefixes {
		b.WriteString("${" + p + "}")

		// The indent goes after a prefix left at column 0, unless nothing
		// follows it.
		if i == 0 && f.col0 && (f.body != "" || len(f.prefixes) > 1) {
			b.WriteString(indent)
		}
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

	f.prefixes, f.body, hadWS, f.col0 = splitPrefixes(line, opts)
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
	continued := c.pending && continuesStatement(code, isComment, ls)

	if continued {
		level++
	}

	f.level = level

	if !isComment && code != "" {
		c.pending = !finished(code)
	}

	c.depth = max(c.depth+ls.opens-ls.closes, 0)

	if f.keepsColumn(hadWS) {
		f.level = -1
	}

	if !continued && isDirective(code, isComment, ls) && f.level >= 0 {
		f.setDirective(code, opts)
	}
}

// keepsColumn reports whether the line must stay where it is. hadWS reports
// whether it had whitespace before its text.
func (f *fline) keepsColumn(hadWS bool) bool {
	// An indented value-less directive is commented out when rendered, an
	// unindented one is not: keep the indentation's presence.
	if vanishingDirective.MatchString(f.body) && (f.level > 0) != hadWS {
		return true
	}

	// A line of variables alone written at column 0 gets its indentation from
	// its expansion (${listen.:addresses} expands to indented lines that
	// start with a switch of their own): it stays at column 0.
	return f.level > 0 && !hadWS && len(f.prefixes) == 0 && onlyVariables.MatchString(f.body)
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

// splitPrefixes splits the line-switch prefixes off a line. hadWS reports
// whether the line had whitespace before its text, col0 whether it has a
// prefix and the first is at column 0.
func splitPrefixes(line string, opts FormatOptions) (prefixes []string, body string, hadWS, col0 bool) {
	rest := strings.TrimLeft(line, " \t")
	hadWS = rest != line
	col0 = !hadWS

	for opts.IsSwitch != nil {
		end := varEnd(rest, 0)
		if end < 0 || !opts.IsSwitch(rest[2:end-1]) {
			break
		}

		prefixes = append(prefixes, rest[2:end-1])

		after := strings.TrimLeft(rest[end:], " \t")
		hadWS = hadWS || len(after) != len(rest[end:])
		rest = after
	}

	return prefixes, rest, hadWS, col0 && len(prefixes) > 0
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
func (f *fline) setDirective(code string, opts FormatOptions) {
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
	f.lead = startsWithLeadingSpace(rest, opts)
}

// startsWithLeadingSpace reports whether text starts with a variable whose
// value renders with a leading space.
func startsWithLeadingSpace(text string, opts FormatOptions) bool {
	if opts.LeadingSpace == nil {
		return false
	}

	end := varEnd(text, 0)

	return end > 0 && opts.LeadingSpace(text[2:end-1])
}

// width is the number of columns a directive's name takes.
func (f *fline) width() int { return utf8.RuneCountInString(f.name) }

// natural is the column of the value of a directive written with a single
// space after its name, counted on the rendered line: the width of the name,
// one space, and the space a leading-space value brings along.
func (f *fline) natural() int {
	n := f.width() + 1
	if f.lead {
		n++
	}

	return n
}

// col is the column of the value in the source, counted the same way.
func (f *fline) col() int { return f.natural() - 1 + f.gap }

// padded reports whether the source puts more than one space after the name.
func (f *fline) padded() bool { return f.gap > 1 }

// alignRuns aligns the directives of each run of consecutive directive lines
// at the same level (a comment, a blank line, a block brace or any other line
// ends a run). The source decides which lines are aligned, so nothing is
// aligned that the author did not align:
//
//   - A line is padded when its value is separated from its name by more than
//     one space.
//   - Two neighbours are linked when both are padded, or when one is padded
//     and the other is not but its value, with the single space it has, starts
//     at the column of the padded one or beyond: it is the overflowing line
//     of the group, the longest name, which could not have had more (the
//     ssl_prefer_server_ciphers line of the ssl_* lines). Two single-spaced
//     neighbours are never linked, and neither is a single-spaced line whose
//     value starts before the padded column ("http2 on;" below "listen"
//     lines written with a wide column).
//   - A chain of linked lines is a group. All of a group's values start in
//     the same column: right after the longest name and one space, minus one
//     for a value that renders with a leading space (see
//     FormatOptions.LeadingSpace). The name of a line is measured without
//     its switch prefix, which renders as "" or "#".
//   - Any other directive gets exactly one space after its name: a lone line
//     or a run written with single spaces keeps it, a lone padded line gets
//     it.
//
// Aligning a group can bring a neighbour into reach: a single-spaced line
// longer than the group's new column is its overflowing line too. So Format
// formats again, until the result no longer changes.
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

// linked reports whether the neighbours a and b of a run belong to one group.
func linked(a, b *fline) bool {
	switch {
	case a.padded() && b.padded():
		return true
	case a.padded():
		return b.natural() >= a.col()
	case b.padded():
		return a.natural() >= b.col()
	}

	return false
}

func alignRun(run []fline) {
	for i := 0; i < len(run); {
		j := i + 1
		for j < len(run) && linked(&run[j-1], &run[j]) {
			j++
		}

		alignGroup(run[i:j])

		i = j
	}
}

// alignGroup aligns a chain of linked lines; a single line is not aligned.
func alignGroup(group []fline) {
	col := 0

	for i := range group {
		col = max(col, group[i].natural())
	}

	for i := range group {
		f := &group[i]

		gap := 1
		if len(group) > 1 {
			gap = col - f.natural() + 1
		}

		f.body = f.name + strings.Repeat(" ", gap) + f.rest
	}
}
