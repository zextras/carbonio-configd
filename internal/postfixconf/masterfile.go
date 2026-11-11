// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfixconf

import (
	"fmt"
	"regexp"
	"strings"
)

// master.cf is parsed by Postfix master (root) with ASCII space/tab field
// separators, logical lines made of an entry plus indented continuation lines,
// and a fixed column grammar. The validator mirrors that grammar strictly and
// rejects everything it cannot classify, because a service whose command
// resolves outside daemon_directory (or that gets -D / a dangerous -o) is a
// root exec for whoever staged the file.
var (
	serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.:\[\]@-]+$`)
	wakeupRe      = regexp.MustCompile(`^(-|\d+\??)$`)
	maxprocRe     = regexp.MustCompile(`^(-|\d+)$`)
)

var (
	masterTypes = map[string]struct{}{
		"inet": {}, "unix": {}, "unix-dgram": {}, "fifo": {}, "pass": {},
	}
	masterFlags = map[string]struct{}{"y": {}, "n": {}, "-": {}}

	// masterCommands are the real Postfix daemons (resolved under
	// daemon_directory). Anything else - post-install, postfix-script, master,
	// or a path - is refused.
	masterCommands = map[string]struct{}{
		"anvil": {}, "bounce": {}, "cleanup": {}, "discard": {}, "dnsblog": {},
		"error": {}, "flush": {}, "local": {}, "lmtp": {}, "oqmgr": {},
		"pickup": {}, "pipe": {}, "postlogd": {}, "postscreen": {},
		"proxymap": {}, "qmgr": {}, "scache": {}, "showq": {}, "smtp": {},
		"smtpd": {}, "spawn": {}, "tlsmgr": {}, "tlsproxy": {},
		"trivial-rewrite": {}, "verify": {}, "virtual": {},
	}

	// deniedOverrides cannot be set by "-o" in master.cf.
	deniedOverrides = map[string]struct{}{
		"debugger_command": {}, "tls_config_file": {}, "tls_config_name": {},
		"default_privs": {}, "virtual_minimum_uid": {}, "virtual_uid_maps": {},
		"virtual_gid_maps": {}, KeyMailOwner: {}, KeySetgidGroup: {},
		"home_mailbox": {}, "virtual_mailbox_base": {}, "sendmail_path": {},
		"newaliases_path": {}, "mailq_path": {}, "import_environment": {},
		"export_environment": {}, "multi_instance_wrapper": {},
	}
	deniedOverrideSuffixes = []string{"_directory", "_directories", "_path", "_environment", "_wrapper"}
)

// ValidateOverrideName checks the parameter name of a master.cf "-o".
func ValidateOverrideName(name string) error {
	if !keyRe.MatchString(name) {
		return validationf("invalid -o parameter name (%d bytes)", len(name))
	}

	lower := strings.ToLower(name)
	if _, bad := deniedOverrides[lower]; bad {
		return validationf("-o %s is not allowed", name)
	}

	for _, suffix := range deniedOverrideSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return validationf("-o %s is not allowed", name)
		}
	}

	return nil
}

func isMasterSpace(r rune) bool { return r == ' ' || r == '\t' }

// logicalLine is one master.cf entry with its continuation lines joined.
type logicalLine struct {
	text string
	line int // first physical line, 1-based
}

// ValidateMasterCf checks master.cf content.
func ValidateMasterCf(data []byte) error {
	for i, b := range data {
		if b != '\t' && b != '\n' && (b < 0x20 || b > 0x7e) {
			return validationf("master.cf contains a byte outside printable ASCII, TAB and LF at offset %d", i)
		}
	}

	entries, err := splitLogicalLines(string(data))
	if err != nil {
		return err
	}

	for _, e := range entries {
		if err := validateEntry(e.text); err != nil {
			return fmt.Errorf("master.cf line %d: %w", e.line, err)
		}
	}

	return nil
}

// splitLogicalLines joins continuation lines. Column-0 comments are skipped
// without ending an entry (the shipped template relies on it). An indented
// line must continue an entry: after a blank line, at the start of the file or
// as an indented comment its meaning would depend on the parser, so it is
// refused.
func splitLogicalLines(text string) ([]logicalLine, error) {
	var out []logicalLine

	open := false

	for i, l := range strings.Split(text, "\n") {
		n := i + 1
		trimmed := strings.TrimLeft(l, " \t")

		switch {
		case strings.HasPrefix(l, "#"):
			continue
		case trimmed == "":
			open = false
		case strings.HasPrefix(trimmed, "#"):
			return nil, validationf("master.cf line %d: indented comment", n)
		case l[0] == ' ' || l[0] == '\t':
			if !open {
				return nil, validationf("master.cf line %d: indented line does not continue a service entry", n)
			}

			out[len(out)-1].text += " " + trimmed
		default:
			out = append(out, logicalLine{text: l, line: n})
			open = true
		}
	}

	return out, nil
}

func validateEntry(text string) error {
	f := strings.FieldsFunc(text, isMasterSpace)
	if len(f) < 8 {
		return validationf("service entry has %d fields, need at least 8", len(f))
	}

	if !serviceNameRe.MatchString(f[0]) || strings.Contains(f[0], "..") {
		return validationf("invalid service name")
	}

	if _, ok := masterTypes[f[1]]; !ok {
		return validationf("service %s: invalid type", f[0])
	}

	for _, col := range f[2:5] {
		if _, ok := masterFlags[col]; !ok {
			return validationf("service %s: private/unpriv/chroot must be y, n or -", f[0])
		}
	}

	if !wakeupRe.MatchString(f[5]) {
		return validationf("service %s: invalid wakeup column", f[0])
	}

	if !maxprocRe.MatchString(f[6]) {
		return validationf("service %s: invalid maxproc column", f[0])
	}

	if _, ok := masterCommands[f[7]]; !ok {
		return validationf("service %s: command is not a Postfix daemon", f[0])
	}

	return validateArgs(f[0], f[7], f[8:])
}

// validateArgs checks the daemon arguments: -o overrides, option clusters,
// brace forms and user= attributes.
func validateArgs(service, command string, args []string) error {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		wrapped := false

		switch {
		case tok == "{" && i+1 < len(args) && strings.HasPrefix(args[i+1], "-o"):
			wrapped = true
			i++
			tok = args[i]
		case strings.HasPrefix(tok, "{-o"):
			wrapped = true
			tok = tok[1:]
		}

		switch {
		case strings.HasPrefix(tok, "-o"):
			next, err := validateOverrideArg(args, i, tok[2:], wrapped)
			if err != nil {
				return err
			}

			i = next
		case wrapped:
			return validationf("service %s: malformed { -o ... } group", service)
		case strings.HasPrefix(tok, "-"):
			// Any other cluster or attached argument that contains o or D is
			// ambiguous (-vo name=value, -vD, -nfoo): refuse.
			if strings.ContainsAny(tok[1:], "oD") {
				return validationf("service %s: option %q is not allowed", service, optionDisplay(tok))
			}
		default:
			if strings.ContainsAny(stripMacros(command, tok), "{}") {
				return validationf("service %s: '{' or '}' outside a -o override", service)
			}

			if runsAsRoot(tok) {
				return validationf("service %s must not run as root", service)
			}
		}
	}

	return nil
}

// pipeMacroRe is a pipe(8)/spawn(8) argv macro such as ${recipient}, which the
// shipped master.cf uses.
var pipeMacroRe = regexp.MustCompile(`\$\{[A-Za-z_]+\}`)

// stripMacros removes ${name} macros from tok for the brace check when the
// service is a pipe or spawn (their argv= attribute is the delivery command line).
func stripMacros(command, tok string) string {
	if command != "pipe" && command != "spawn" {
		return tok
	}

	return pipeMacroRe.ReplaceAllString(tok, "")
}

// optionDisplay shows at most the option letters, never attached values.
func optionDisplay(tok string) string {
	if len(tok) > 3 {
		return tok[:3] + "..."
	}

	return tok
}

func runsAsRoot(tok string) bool {
	user, ok := strings.CutPrefix(tok, "user=")
	if !ok {
		return false
	}

	user, _, _ = strings.Cut(user, ":")

	return user == "root" || user == "0"
}

// validateOverrideArg validates the argument of the "-o" at args[i] (attached
// is the text glued to -o) and returns the index of the last token consumed.
func validateOverrideArg(args []string, i int, attached string, wrapped bool) (int, error) {
	arg := attached
	if arg == "" {
		if i+1 >= len(args) {
			return i, validationf("-o without an argument")
		}

		i++
		arg = args[i]
	}

	arg, i, err := unwrapOverride(args, i, arg, wrapped)
	if err != nil {
		return i, err
	}

	if strings.ContainsAny(arg, "{}") {
		return i, validationf("'{' or '}' inside a -o value")
	}

	name, value, ok := strings.Cut(arg, "=")
	if !ok {
		return i, validationf("-o argument is not NAME=VALUE")
	}

	name = strings.TrimSpace(name)
	if err := ValidateOverrideName(name); err != nil {
		return i, err
	}

	return i, validateValue(name, strings.TrimSpace(value))
}

// unwrapOverride resolves the brace forms around an override argument: the
// "{ name = value }" argument form, or the "{ -o name=value }" wrapper whose
// closing brace may stick to the value or stand alone.
func unwrapOverride(args []string, i int, arg string, wrapped bool) (value string, last int, err error) {
	switch {
	case strings.HasPrefix(arg, "{") && wrapped:
		return "", i, validationf("nested '{' in -o")
	case strings.HasPrefix(arg, "{"):
		return braceArg(arg, args, i)
	case wrapped:
		if inner, closed := strings.CutSuffix(arg, "}"); closed {
			return inner, i, nil
		}

		if i+1 >= len(args) || args[i+1] != "}" {
			return "", i, validationf("unterminated '{' before -o")
		}

		return arg, i + 1, nil
	}

	return arg, i, nil
}

// braceArg collects a "{ name = value }" argument starting at toks[i] and
// returns its inner text and the index of the token holding the closing brace.
func braceArg(first string, toks []string, i int) (inner string, last int, err error) {
	parts := []string{first}

	for !strings.HasSuffix(parts[len(parts)-1], "}") {
		i++
		if i >= len(toks) {
			return "", i, validationf("unterminated '{' after -o")
		}

		parts = append(parts, toks[i])
	}

	joined := strings.Join(parts, " ")
	inner = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(joined, "{"), "}"))

	return inner, i, nil
}
