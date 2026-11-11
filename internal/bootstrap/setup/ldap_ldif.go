// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ldapLDIFAttr is one attribute of an LDIF entry.
type ldapLDIFAttr struct {
	Name   string
	Values []string
}

// ldapLDIFEntry is one content record of an LDIF file: the entries the
// package ships (carbonio.ldif, zimbra_globalconfig.ldif, ...) that
// ldapinit.pm postLdapStart adds to a new directory.
type ldapLDIFEntry struct {
	DN    string
	Attrs []ldapLDIFAttr
}

// errLDAPLDIF is returned for an LDIF file that cannot be read.
var errLDAPLDIF = errors.New("invalid LDIF")

// ldapUnfold joins the continuation lines of an LDIF file (a line starting
// with one space continues the previous one) and drops comment lines.
func ldapUnfold(data []byte) []string {
	var lines []string

	for raw := range strings.SplitSeq(string(data), "\n") {
		raw = strings.TrimSuffix(raw, "\r")

		switch {
		case strings.HasPrefix(raw, " ") && len(lines) > 0:
			// A folded comment stays a comment.
			if lines[len(lines)-1] != "#" {
				lines[len(lines)-1] += raw[1:]
			}
		case strings.HasPrefix(raw, "#"):
			lines = append(lines, "#")
		default:
			lines = append(lines, raw)
		}
	}

	return lines
}

// ldapLDIFSplit splits one unfolded "name: value", "name:: base64" line.
func ldapLDIFSplit(line string) (name, value string, err error) {
	name, rest, ok := strings.Cut(line, ":")
	if !ok || name == "" {
		return "", "", fmt.Errorf("%w: %q is not an attribute line", errLDAPLDIF, line)
	}

	switch {
	case strings.HasPrefix(rest, ":"):
		dec, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[1:]))
		if derr != nil {
			return "", "", fmt.Errorf("%w: attribute %s: %w", errLDAPLDIF, name, derr)
		}

		return name, string(dec), nil
	case strings.HasPrefix(rest, "<"):
		return "", "", fmt.Errorf("%w: attribute %s: URL values are not supported", errLDAPLDIF, name)
	}

	return name, strings.TrimPrefix(rest, " "), nil
}

// ldapParseLDIF parses the content records of an LDIF file. Change records
// (changetype) are rejected: the shipped files only hold entries.
func ldapParseLDIF(data []byte) ([]ldapLDIFEntry, error) {
	var (
		entries []ldapLDIFEntry
		cur     *ldapLDIFEntry
	)

	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
		}
	}

	for _, line := range ldapUnfold(data) {
		switch line {
		case "":
			flush()

			continue
		case "#":
			continue
		}

		name, value, err := ldapLDIFSplit(line)
		if err != nil {
			return nil, err
		}

		switch {
		case strings.EqualFold(name, "version") && cur == nil:
			continue
		case strings.EqualFold(name, "dn"):
			flush()

			cur = &ldapLDIFEntry{DN: value}
		case cur == nil:
			return nil, fmt.Errorf("%w: attribute %s before a dn", errLDAPLDIF, name)
		case strings.EqualFold(name, "changetype"):
			return nil, fmt.Errorf("%w: change records are not supported (dn %s)", errLDAPLDIF, cur.DN)
		default:
			cur.add(name, value)
		}
	}

	flush()

	return entries, nil
}

func (e *ldapLDIFEntry) add(name, value string) {
	for i := range e.Attrs {
		if strings.EqualFold(e.Attrs[i].Name, name) {
			e.Attrs[i].Values = append(e.Attrs[i].Values, value)

			return
		}
	}

	e.Attrs = append(e.Attrs, ldapLDIFAttr{Name: name, Values: []string{value}})
}

// ldapNormDN compares DNs the way the shipped files are written: case and
// the space after a comma do not matter.
func ldapNormDN(dn string) string {
	parts := strings.Split(dn, ",")
	for i, p := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(p))
	}

	return strings.Join(parts, ",")
}

// ldapLDIFSetPassword sets the userPassword of the entry dn to value in an LDIF file, leaving
// every other byte of the file as it is: the attribute lines of the entry
// (with their continuation lines) are replaced by one line, or one is added
// at the end of the entry. It reports whether the entry was found.
func ldapLDIFSetPassword(data []byte, dn, value string) (out []byte, found bool, err error) {
	lines := strings.Split(string(data), "\n")
	want := ldapNormDN(dn)

	var res []string

	for start := 0; start < len(lines); {
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			end++
		}

		block := lines[start:end]

		if !found && len(block) > 0 {
			rep, ok, rerr := ldapLDIFSetBlock(block, want, value)
			if rerr != nil {
				return nil, false, rerr
			}

			if ok {
				found = true
				block = rep
			}
		}

		res = append(res, block...)

		// Keep the blank separator lines as they are.
		for end < len(lines) && strings.TrimSpace(lines[end]) == "" {
			res = append(res, lines[end])
			end++
		}

		start = end
	}

	return []byte(strings.Join(res, "\n")), found, nil
}

// ldapAttrUserPassword is the attribute the password hashes are kept in.
const ldapAttrUserPassword = "userPassword"

// ldapLDIFSetBlock rewrites one entry block when its dn is want.
func ldapLDIFSetBlock(block []string, want, value string) (out []string, ok bool, err error) {
	groups := ldapLDIFGroups(block)

	dn := ""

	for _, g := range groups {
		if strings.HasPrefix(g[0], "#") {
			continue
		}

		name, v, serr := ldapLDIFSplit(ldapJoinGroup(g))
		if serr != nil {
			return nil, false, serr
		}

		if strings.EqualFold(name, "dn") {
			dn = v

			break
		}
	}

	if dn == "" || ldapNormDN(dn) != want {
		return block, false, nil
	}

	replaced := false

	for _, g := range groups {
		if name, _, found := strings.Cut(g[0], ":"); found && strings.EqualFold(name, ldapAttrUserPassword) {
			if !replaced {
				out = append(out, ldapAttrUserPassword+": "+value)
				replaced = true
			}

			continue
		}

		out = append(out, g...)
	}

	if !replaced {
		out = append(out, ldapAttrUserPassword+": "+value)
	}

	return out, true, nil
}

// ldapJoinGroup unfolds one line group.
func ldapJoinGroup(g []string) string {
	var sb strings.Builder

	sb.WriteString(g[0])

	for _, l := range g[1:] {
		sb.WriteString(l[1:])
	}

	return sb.String()
}

// ldapLDIFGroups groups the lines of an entry: a line and its continuations.
func ldapLDIFGroups(block []string) [][]string {
	var groups [][]string

	for _, l := range block {
		if strings.HasPrefix(l, " ") && len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], l)

			continue
		}

		groups = append(groups, []string{l})
	}

	return groups
}

// ldapReplaceLine replaces the lines of a file that start with prefix (and
// their continuation lines) by repl. It returns the number of lines replaced.
func ldapReplaceLine(data []byte, prefix, repl string) (out []byte, n int) {
	var res []string

	inRepl := false

	for line := range strings.SplitSeq(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, prefix):
			res = append(res, repl)
			n++
			inRepl = true
		case inRepl && strings.HasPrefix(line, " "):
			// continuation of the replaced attribute: dropped
		default:
			inRepl = false

			res = append(res, line)
		}
	}

	return []byte(strings.Join(res, "\n")), n
}
