// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package tmpl is the pure core of the nginx proxy template language: the
// lexer, the line renderer and the explode directive, plus the formatter and
// linter built on them. It has no LDAP, filesystem, logger or Generator
// dependency, so the proxy generator (internal/proxy) and the template tools
// ("configd proxy template") share one definition of the language.
//
// The language, as the legacy ProxyConfGen defines it:
//
//   - ${name} is replaced by the variable's text.
//   - An enabler prefix, ${name} at the start of a line directly followed by
//     more text, switches the rest of the line on (prefix dropped) or off
//     (line commented out), when name is an enabler variable. Prefixes chain.
//   - A first line !{explode type(args)} renders the rest of the file once per
//     domain or server.
//   - A rendered line that is an indented directive with no value
//     ("    imap_id   ;") is commented out.
package tmpl

import (
	"fmt"
	"regexp"
	"strings"
)

// The patterns of the template language. The generator and the tools use
// exactly these.
var (
	// VarPattern matches ${name}.
	VarPattern = regexp.MustCompile(`\$\{([a-zA-Z0-9._:]+)\}`)
	// ExplodePattern matches the first-line directive !{explode type(args)}.
	ExplodePattern = regexp.MustCompile(`^!\{explode\s+(\w+)\(([^)]*)\)\}`)
	// EnablerPattern matches an enabler prefix: indent, ${name}, rest of the
	// line (non-empty; no space is required after the closing brace).
	EnablerPattern = regexp.MustCompile(`^(\s*)\$\{([^}]+)\}(.+)$`)
	// EmptyDirectivePattern matches a rendered indented directive without a
	// value ("    imap_id   ;").
	EmptyDirectivePattern = regexp.MustCompile(`^\s+[a-z0-9_]+\s+;`)
)

// ExplodeMarker introduces an explode directive.
const ExplodeMarker = "!{explode"

// Explode types.
const (
	ExplodeDomain = "domain"
	ExplodeServer = "server"
)

// Variables the explode directive defines for each block of the template body.
const (
	VarVHN            = "vhn"
	VarVIP            = "vip"
	VarSSLCrt         = "ssl.crt"
	VarSSLKey         = "ssl.key"
	VarSSLClientCA    = "ssl.clientcertca"
	VarServerID       = "server_id"
	VarServerHostname = "server_hostname"
)

// ConfPrefix is the default generated file prefix, the stem of nginx.conf.
const ConfPrefix = "nginx.conf"

// Kind classifies a variable for the tools: what a template may do with it.
type Kind int

const (
	// KindText is a variable substituted as text. It may also be "" or "#"
	// (the *.disable and web.strict.servername variables), which makes it a
	// line switch when written at the start of a line.
	KindText Kind = iota
	// KindScalar is a number, a duration or an on/off flag: it never renders
	// as "" or "#", so it cannot switch a line.
	KindScalar
	// KindEnabler is a variable that switches the line it prefixes on or off.
	// Substituted as a value it renders as "" (on) or "#" (off).
	KindEnabler
)

// Resolver supplies variable values to the renderer.
type Resolver interface {
	// Enabler reports whether name is an enabler variable (ok) and, if so,
	// whether it is on.
	Enabler(name string) (on, ok bool)
	// Value returns the text of the variable name; ok is false for an
	// unknown variable, which renders as the empty string.
	Value(name string) (text string, ok bool)
}

// SplitLines splits template content into lines the way Java's
// BufferedReader.readLine does: a trailing newline terminates the last line
// instead of starting an empty one, so the output (each line plus "\n") is not
// padded with an extra blank line.
func SplitLines(content string) []string {
	if content == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

// IsCommentLine reports whether line is a comment line (first non-blank
// character '#').
func IsCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

// ParseExplode checks whether line is an explode directive and returns its
// type and raw argument list.
func ParseExplode(line string) (typ, args string, ok bool) {
	m := ExplodePattern.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}

	return m[1], m[2], true
}

// DomainArgs parses the argument list of "explode domain(...)" ("vhn" or
// "vhn, sso").
func DomainArgs(argsStr string) ([]string, error) {
	argsStr = strings.TrimSpace(argsStr)
	if argsStr == "" {
		return nil, fmt.Errorf("explode domain directive requires at least one argument")
	}

	args := strings.Split(argsStr, ",")
	for i := range args {
		args[i] = strings.TrimSpace(args[i])
		if args[i] == "" {
			return nil, fmt.Errorf("explode domain directive has empty argument")
		}
	}

	return args, nil
}

// ServerService parses the argument of "explode server(service)".
func ServerService(argsStr string) (string, error) {
	service := strings.TrimSpace(argsStr)
	if service == "" {
		return "", fmt.Errorf("service name required for server explode directive")
	}

	return service, nil
}

// ExplodeVariables are the variables the explode directive of the given type
// defines for its body.
func ExplodeVariables(typ string) []string {
	switch typ {
	case ExplodeDomain:
		return []string{VarVHN, VarVIP, VarSSLCrt, VarSSLKey, VarSSLClientCA}
	case ExplodeServer:
		return []string{VarServerID, VarServerHostname}
	}

	return nil
}

// OutputName maps a template file name to the file it generates: the template
// prefix is replaced by the config prefix and the .template suffix dropped.
// With a non-default template prefix only the templates of that prefix are
// generated, as ProxyConfGen names them explicitly.
func OutputName(templateName, templatePrefix, confPrefix string) (string, bool) {
	name := strings.TrimSuffix(templateName, ".template")
	if templatePrefix == ConfPrefix && confPrefix == ConfPrefix {
		return name, true
	}

	if name != templatePrefix && !strings.HasPrefix(name, templatePrefix+".") {
		return "", false
	}

	return confPrefix + strings.TrimPrefix(name, templatePrefix), true
}

// RenderLine renders one template line: enabler prefixes, ${var} substitution
// and the commenting-out of empty directives. A multi-line expansion yields a
// multi-line result.
func RenderLine(line string, r Resolver) string {
	if m := EnablerPattern.FindStringSubmatch(line); m != nil {
		if out, ok := renderEnabler(m[1], m[2], m[3], r); ok {
			return out
		}
	}

	result := expandVars(line, r)

	// An indented directive left without arguments is commented out.
	if EmptyDirectivePattern.MatchString(result) {
		result = "    # " + strings.TrimSpace(result)
	}

	return result
}

// expandVars substitutes every ${var} of line. A multi-line value is indented
// to the placeholder's column: each non-empty continuation line is prefixed
// with the whitespace preceding the placeholder (or, when the placeholder
// follows other text, with the line's leading indentation), so the value lines
// up with the directive it replaces. This deliberately diverges from the legacy
// generator, which leaves continuation lines to the value itself (a fixed
// indentation baked into the resolver).
func expandVars(line string, r Resolver) string {
	locs := VarPattern.FindAllStringIndex(line, -1)
	if locs == nil {
		return line
	}

	var sb strings.Builder

	last := 0

	for _, loc := range locs {
		sb.WriteString(line[last:loc[0]])

		text, _ := r.Value(line[loc[0]+2 : loc[1]-1])
		sb.WriteString(indentContinuation(text, placeholderIndent(line[:loc[0]])))

		last = loc[1]
	}

	sb.WriteString(line[last:])

	return sb.String()
}

// placeholderIndent returns the indentation a multi-line value takes: the
// whole of before when it is only whitespace, else its leading whitespace.
func placeholderIndent(before string) string {
	return before[:len(before)-len(strings.TrimLeft(before, " \t"))]
}

// indentContinuation prefixes every non-empty line of text but the first with
// indent. Empty lines stay empty (no trailing whitespace).
func indentContinuation(text, indent string) string {
	if indent == "" || !strings.Contains(text, "\n") {
		return text
	}

	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			lines[i] = indent + lines[i]
		}
	}

	return strings.Join(lines, "\n")
}

// renderEnabler renders a line whose enabler prefix is name; ok is false when
// name is not an enabler variable, and the line is then rendered as text.
func renderEnabler(indent, name, rest string, r Resolver) (out string, ok bool) {
	on, isEnabler := r.Enabler(name)
	if !isEnabler {
		return "", false
	}

	// The rest of the line may carry other prefixes and variables.
	rendered := RenderLine(indent+rest, r)
	if on {
		return rendered, true
	}

	// Comment out every line of the expanded value (a multi-line expansion,
	// e.g. :servers, produces one directive per line; left alone, the
	// continuation lines would stay live inside a commented block). Legacy
	// StringUtil.fillTemplate substitutes the enabler with "#" in place, so
	// whitespace following the enabler is preserved verbatim; continuation
	// lines get the same treatment.
	lines := strings.Split(rendered, "\n")
	for i, l := range lines {
		if strings.TrimLeft(l, " \t") == "" {
			continue // preserve blank lines as-is
		}

		lines[i] = indent + "#" + strings.TrimPrefix(l, indent)
	}

	return strings.Join(lines, "\n"), true
}
