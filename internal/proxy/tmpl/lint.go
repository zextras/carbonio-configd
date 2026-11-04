// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package tmpl

import (
	"fmt"
	"slices"
	"strings"
)

// Lint rule identifiers.
const (
	RuleUnknownVariable = "unknown-variable"
	RuleEnablerAsValue  = "enabler-as-value"
	RuleValueAsEnabler  = "value-as-enabler"
	RuleExplode         = "explode"
	RuleBraces          = "braces"
	RuleWhitespace      = "whitespace"
)

// Registry tells the linter which variables exist.
type Registry interface {
	// Kind returns the kind of the variable name; ok is false when unknown.
	Kind(name string) (kind Kind, ok bool)
}

// Finding is one lint finding. Line and Col are 1-based; Col is a byte column.
type Finding struct {
	File    string
	Line    int
	Col     int
	Rule    string
	Message string
}

// String is "file:line:col: message [rule]".
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d:%d: %s [%s]", f.File, f.Line, f.Col, f.Message, f.Rule)
}

// linter holds the state of one Lint run.
type linter struct {
	file     string
	reg      Registry
	scope    map[string]bool // variables defined by the explode directive
	explType string
	findings []Finding

	sc     scanner // quote state across lines
	depth  int     // open blocks
	groups map[string]*group
	order  []string // groups in order of appearance
}

func (l *linter) add(line, col int, rule, format string, args ...any) {
	l.findings = append(l.findings, Finding{
		File: l.file, Line: line, Col: col, Rule: rule, Message: fmt.Sprintf(format, args...),
	})
}

// group tracks the braces of the lines carrying one enabler prefix.
type group struct {
	net, line, col int
}

// Lint checks a template: unknown variables, enabler variables used as values
// and other variables used as enablers, explode directives that are not on the
// first line, unbalanced braces (per enabler group and per file) and trailing
// whitespace or tabs. Findings are ordered by position.
func Lint(file, src string, reg Registry) []Finding {
	l := &linter{file: file, reg: reg, groups: map[string]*group{}}
	lines := SplitLines(src)

	if len(lines) > 0 {
		l.checkExplodeLine(lines[0])
	}

	for i, line := range lines {
		l.checkLine(i, line)
	}

	l.checkBalance(len(lines))

	slices.SortStableFunc(l.findings, func(a, b Finding) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}

		return a.Col - b.Col
	})

	return l.findings
}

// checkLine runs the per-line rules on line i (0-based).
func (l *linter) checkLine(i int, line string) {
	n := i + 1

	l.checkWhitespace(n, line)

	if i == 0 && l.explType != "" {
		return
	}

	if i > 0 && strings.Contains(line, ExplodeMarker) {
		l.add(n, strings.Index(line, ExplodeMarker)+1, RuleExplode,
			"explode directive must be on the first line of the template")
	}

	ls, base := lineBraces(&l.sc, line)
	toks := Tokenize(line)

	l.checkTokens(n, toks)
	l.countGroups(n, toks, ls)

	if ls.leadClose > l.depth {
		l.add(n, base+ls.firstClose+1, RuleBraces, "unexpected '}': no block is open")
	}

	l.depth = max(l.depth+ls.opens-ls.closes, 0)
}

// countGroups adds the line's net braces to each enabler group it belongs to.
func (l *linter) countGroups(n int, toks []Token, ls lineScan) {
	for _, t := range toks {
		if t.Kind != Enabler {
			continue
		}

		g := l.groups[t.Text]
		if g == nil {
			g = &group{line: n, col: t.Offset + 1}
			l.groups[t.Text] = g
			l.order = append(l.order, t.Text)
		}

		g.net += ls.opens - ls.closes
	}
}

// checkBalance reports the enabler groups and the file left unbalanced.
func (l *linter) checkBalance(lineCount int) {
	for _, name := range l.order {
		if g := l.groups[name]; g.net != 0 {
			l.add(g.line, g.col, RuleBraces, "unbalanced braces in the lines enabled by ${%s}: %s", name, imbalance(g.net))
		}
	}

	if l.depth != 0 {
		l.add(max(lineCount, 1), 1, RuleBraces, "unbalanced braces in the file: %d block(s) left open", l.depth)
	}
}

// lineBraces counts the braces of the code after the enabler prefixes.
func lineBraces(sc *scanner, line string) (ls lineScan, base int) {
	for {
		m := EnablerPattern.FindStringSubmatchIndex(line[base:])
		if m == nil {
			break
		}

		base += m[5] + 1
	}

	return sc.scan(line[base:]), base
}

func imbalance(net int) string {
	if net > 0 {
		return fmt.Sprintf("%d more '{' than '}'", net)
	}

	return fmt.Sprintf("%d more '}' than '{'", -net)
}

// checkExplodeLine validates the first line when it is an explode directive.
func (l *linter) checkExplodeLine(first string) {
	typ, args, ok := ParseExplode(first)
	if !ok {
		if strings.HasPrefix(first, "!{") {
			l.add(1, 1, RuleExplode, "malformed explode directive, want !{explode type(args)}")
		}

		return
	}

	l.explType = typ

	var err error

	switch typ {
	case ExplodeDomain:
		_, err = DomainArgs(args)
	case ExplodeServer:
		_, err = ServerService(args)
	default:
		err = fmt.Errorf("unknown explode type %q, want %s or %s", typ, ExplodeDomain, ExplodeServer)
	}

	if err != nil {
		l.add(1, 1, RuleExplode, "%v", err)
	}

	l.scope = map[string]bool{}
	for _, v := range ExplodeVariables(typ) {
		l.scope[v] = true
	}
}

// checkWhitespace reports trailing whitespace and tabs.
func (l *linter) checkWhitespace(n int, line string) {
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		l.add(n, i+1, RuleWhitespace, "tab character, indent with spaces")
	}

	if trimmed := strings.TrimRight(line, " \t\r"); trimmed != line {
		l.add(n, len(trimmed)+1, RuleWhitespace, "trailing whitespace")
	}
}

// checkTokens validates the variables of a line.
func (l *linter) checkTokens(n int, toks []Token) {
	for _, t := range toks {
		if t.Kind == Text {
			continue
		}

		col := t.Offset + 1

		kind, ok := l.reg.Kind(t.Text)

		switch {
		case !ok && l.scope[t.Text]:
			continue
		case !ok:
			l.add(n, col, RuleUnknownVariable, "unknown variable ${%s}%s", t.Text, l.scopeHint(t.Text))
		case t.Kind == Enabler && kind == KindScalar:
			l.add(n, col, RuleValueAsEnabler,
				"${%s} is a number or on/off value, not an enabler, but is used as a line prefix", t.Text)
		case t.Kind == Var && kind == KindEnabler:
			l.add(n, col, RuleEnablerAsValue,
				"${%s} is an enabler variable but is used as a value (it renders as \"\" or \"#\")", t.Text)
		}
	}
}

// scopeHint explains an explode variable used outside its explode directive.
func (l *linter) scopeHint(name string) string {
	switch name {
	case VarVHN, VarVIP:
		return fmt.Sprintf(" (defined only by !{explode %s(...)})", ExplodeDomain)
	case VarServerID, VarServerHostname:
		return fmt.Sprintf(" (defined only by !{explode %s(...)})", ExplodeServer)
	}

	return ""
}
