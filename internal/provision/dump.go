// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// shown is an attribute as ProvUtil dumps it: the name as the directory (or
// the schema) spells it and the values, in the order the directory returned
// them.
type shown struct {
	name   string
	values []string
}

// shownAttrs is the attribute map of an entry, by lower-case name: what
// Entry.getAttrs returns.
type shownAttrs map[string]shown

// knownNames spells the attributes the schema (attrs.xml) does not define the
// way the directory does, for a directory that does not tell.
var knownNames = map[string]string{
	"objectclass": "objectClass", "userpassword": "userPassword", "displayname": "displayName",
	"givenname": "givenName", "cn": "cn", "uid": "uid", "sn": "sn", "o": "o", "ou": "ou", "dc": "dc",
	"description": "description", "mail": "mail",
}

// attrName is the name an attribute of the entry is printed with: the one the
// directory returned, else the schema's.
func (p *Provisioner) attrName(e *Entry, lower string) string {
	if n, ok := e.Names[lower]; ok {
		return n
	}

	if a := p.Schema.Lookup(lower); a != nil {
		return a.Name
	}

	if n, ok := knownNames[lower]; ok {
		return n
	}

	return lower
}

// attrsOf is the attributes the entry has, as read.
func (p *Provisioner) attrsOf(e *Entry) shownAttrs {
	m := make(shownAttrs, len(e.Attrs))
	for lower, values := range e.Attrs {
		m[lower] = shown{p.attrName(e, lower), values}
	}

	return m
}

// inherited is what an entry takes from another one (Entry.getDefaults): the
// attributes with the flag that the source has.
func (p *Provisioner) inherited(flag string, source shownAttrs) shownAttrs {
	m := shownAttrs{}

	for _, name := range p.Schema.WithFlag(flag) {
		lower := strings.ToLower(name)
		if v, ok := source[lower]; ok && len(v.values) > 0 {
			m[lower] = shown{name, v.values}
		}
	}

	return m
}

// over returns the attributes of m with those of top put over them
// (Map.putAll).
func (m shownAttrs) over(top shownAttrs) shownAttrs {
	out := make(shownAttrs, len(m)+len(top))
	maps.Copy(out, m)
	maps.Copy(out, top)

	return out
}

// dumpFilter is the attribute names (and values) a command asks for, as
// ProvUtil.getArgNameSet and dumpAttrs read them.
type dumpFilter map[string]map[string]bool

// newDumpFilter reads the arguments after the entry name: nil for none (every
// attribute), else the names lower-cased, each with the values to show when it
// is given as name=value.
func newDumpFilter(args []string) (dumpFilter, error) {
	if len(args) == 0 {
		return nil, nil //nolint:nilnil // no arguments, no filter
	}

	f := dumpFilter{}

	for _, arg := range args {
		arg = strings.ToLower(arg) // getArgNameSet lower-cases the whole argument

		name, value, hasValue := strings.Cut(arg, "=")
		if hasValue && value == "" {
			return nil, invalidRequest("missing value for %s", arg)
		}

		if f[name] == nil {
			f[name] = map[string]bool{}
		}

		if hasValue {
			f[name][value] = true
		}
	}

	return f, nil
}

// wants reports whether the value of the attribute is printed.
func (f dumpFilter) wants(name, value string) bool {
	if f == nil {
		return true
	}

	values, ok := f[strings.ToLower(name)]

	return ok && (len(values) == 0 || values[value])
}

// dump is ProvUtilDumper.dumpAttrs: the attributes sorted by name, one
// "name: value" line per non-empty value; a binary attribute is
// "name:: " and its base64, in lines of 76 characters.
func (p *Provisioner) dump(w io.Writer, attrs shownAttrs, f dumpFilter) error {
	all := make([]shown, 0, len(attrs))
	for _, a := range attrs {
		all = append(all, a)
	}

	slices.SortFunc(all, func(a, b shown) int { return strings.Compare(a.name, b.name) })

	for _, a := range all {
		for _, v := range a.values {
			if v == "" || !f.wants(a.name, v) {
				continue
			}

			if err := p.printAttr(w, a.name, v); err != nil {
				return err
			}
		}
	}

	return nil
}

// printAttr is ProvUtilDumper.printAttr.
func (p *Provisioner) printAttr(w io.Writer, name, value string) error {
	var err error

	if p.Schema.Binary(name) {
		_, err = fmt.Fprintf(w, "%s:: %s\n", name, chunkedBase64([]byte(value)))
	} else {
		_, err = fmt.Fprintf(w, "%s: %s\n", name, value)
	}

	return err
}

// chunkedBase64 is Base64.encodeBase64Chunked of commons-codec: lines of 76
// characters ended by CRLF, the last one too, less the final LF that ProvUtil
// strips (the CR stays).
func chunkedBase64(data []byte) string {
	const lineLen = 76

	enc := base64.StdEncoding.EncodeToString(data)

	var b strings.Builder

	for len(enc) > lineLen {
		b.WriteString(enc[:lineLen])
		b.WriteString("\r\n")

		enc = enc[lineLen:]
	}

	b.WriteString(enc)
	b.WriteString("\r")

	return b.String()
}

// dumpEntry is the dump of a named entry: "# name <name>", its attributes and
// a blank line.
func (p *Provisioner) dumpEntry(w io.Writer, name string, attrs shownAttrs, f dumpFilter) error {
	if _, err := fmt.Fprintf(w, "# name %s\n", name); err != nil {
		return err
	}

	if err := p.dump(w, attrs, f); err != nil {
		return err
	}

	_, err := fmt.Fprintln(w)

	return err
}
