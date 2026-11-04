// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapfake

import (
	"fmt"
	"strings"

	ber "github.com/go-asn1-ber/asn1-ber"
)

// LDAP search scopes and filter choices (RFC 4511 4.5.1).
const (
	scopeBase int64 = 0
	scopeOne  int64 = 1

	filterAnd        ber.Tag = 0
	filterOr         ber.Tag = 1
	filterNot        ber.Tag = 2
	filterEquality   ber.Tag = 3
	filterSubstrings ber.Tag = 4
	filterGE         ber.Tag = 5
	filterLE         ber.Tag = 6
	filterPresent    ber.Tag = 7
	filterApprox     ber.Tag = 8

	resultNoSuchObject int64 = 32
)

// search answers a search request from the directory.
func (ss *session) search(id int64, req *ber.Packet) bool {
	if len(req.Children) < 8 {
		return writePacket(ss.conn, result(id, opSearchResultDone, resultProtocolError)) == nil
	}

	base, _ := req.Children[0].Value.(string)
	scope, _ := req.Children[1].Value.(int64)
	filter := req.Children[6]

	var wanted []string

	for _, a := range req.Children[7].Children {
		if s, ok := a.Value.(string); ok {
			wanted = append(wanted, s)
		}
	}

	dir := ss.s.opts.Directory

	entries, found := dir.searchEntries(base, scope)
	if !found {
		return writePacket(ss.conn, result(id, opSearchResultDone, resultNoSuchObject)) == nil
	}

	for _, e := range entries {
		if !matchFilter(e, filter) {
			continue
		}

		if writePacket(ss.conn, entryPacket(id, e, wanted)) != nil {
			return false
		}
	}

	return writePacket(ss.conn, result(id, opSearchResultDone, resultSuccess)) == nil
}

// searchEntries returns copies of the entries in scope of base, and whether
// the base entry exists.
func (d *Directory) searchEntries(base string, scope int64) ([]*Entry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	bk := normDN(base)
	if _, ok := d.entries[bk]; !ok && bk != "" {
		return nil, false
	}

	var out []*Entry

	for _, k := range d.order {
		if !inScope(k, bk, scope) {
			continue
		}

		out = append(out, d.entries[k].clone())
	}

	return out, true
}

func inScope(key, base string, scope int64) bool {
	switch scope {
	case scopeBase:
		return key == base
	case scopeOne:
		if base == "" {
			return !strings.Contains(key, ",")
		}

		rest, ok := strings.CutSuffix(key, ","+base)

		return ok && !strings.Contains(rest, ",")
	default:
		return base == "" || key == base || strings.HasSuffix(key, ","+base)
	}
}

func entryPacket(id int64, e *Entry, wanted []string) *ber.Packet {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))

	op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, opSearchResultEntry, nil, "SearchResultEntry")
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.DN, "objectName"))

	attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")

	for _, a := range e.attrs {
		if !wantsAttr(wanted, a.name) {
			continue
		}

		pa := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "PartialAttribute")
		pa.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, a.name, "type"))

		vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "vals")
		for _, v := range a.values {
			vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "value"))
		}

		pa.AppendChild(vals)
		attrs.AppendChild(pa)
	}

	op.AppendChild(attrs)
	msg.AppendChild(op)

	return msg
}

// wantsAttr applies the requested attribute list: empty or "*" selects every
// user attribute, "1.1" none.
func wantsAttr(wanted []string, name string) bool {
	if len(wanted) == 0 {
		return true
	}

	for _, w := range wanted {
		if w == "*" || strings.EqualFold(w, name) {
			return true
		}
	}

	return false
}

// matchFilter evaluates a BER-encoded search filter against an entry. Value
// comparison is case-insensitive, as for the caseIgnoreMatch attributes the
// configd queries filter on.
func matchFilter(e *Entry, f *ber.Packet) bool {
	switch f.Tag {
	case filterAnd:
		for _, c := range f.Children {
			if !matchFilter(e, c) {
				return false
			}
		}

		return true
	case filterOr:
		for _, c := range f.Children {
			if matchFilter(e, c) {
				return true
			}
		}

		return false
	case filterNot:
		return len(f.Children) == 1 && !matchFilter(e, f.Children[0])
	case filterEquality, filterApprox:
		attr, val := attrValue(f)

		return containsFold(e.Values(attr), val)
	case filterGE, filterLE:
		return matchOrdered(e, f)
	case filterPresent:
		return e.find(f.Data.String()) != nil
	case filterSubstrings:
		return matchSubstrings(e, f)
	default:
		return false
	}
}

// matchOrdered evaluates a >= or <= filter, comparing case-insensitively.
func matchOrdered(e *Entry, f *ber.Packet) bool {
	attr, val := attrValue(f)
	val = strings.ToLower(val)

	for _, v := range e.Values(attr) {
		v = strings.ToLower(v)
		if (f.Tag == filterGE && v >= val) || (f.Tag == filterLE && v <= val) {
			return true
		}
	}

	return false
}

func attrValue(f *ber.Packet) (attr, val string) {
	if len(f.Children) < 2 {
		return "", ""
	}

	attr, _ = f.Children[0].Value.(string)
	val, _ = f.Children[1].Value.(string)

	return attr, val
}

func matchSubstrings(e *Entry, f *ber.Packet) bool {
	if len(f.Children) < 2 {
		return false
	}

	attr, _ := f.Children[0].Value.(string)

	for _, v := range e.Values(attr) {
		if substringsMatch(strings.ToLower(v), f.Children[1].Children) {
			return true
		}
	}

	return false
}

// substringsMatch checks initial ([0]), any ([1]) and final ([2]) parts.
func substringsMatch(v string, parts []*ber.Packet) bool {
	for _, p := range parts {
		sub := strings.ToLower(p.Data.String())

		switch p.Tag {
		case 0:
			if !strings.HasPrefix(v, sub) {
				return false
			}

			v = v[len(sub):]
		case 1:
			i := strings.Index(v, sub)
			if i < 0 {
				return false
			}

			v = v[i+len(sub):]
		case 2:
			if !strings.HasSuffix(v, sub) {
				return false
			}
		}
	}

	return true
}

// modify applies a modify request to the directory.
func (ss *session) modify(id int64, req *ber.Packet) bool {
	code := resultSuccess

	if len(req.Children) < 2 {
		code = resultProtocolError
	} else {
		dn, _ := req.Children[0].Value.(string)

		var mods []Mod

		for _, ch := range req.Children[1].Children {
			if len(ch.Children) < 2 || len(ch.Children[1].Children) < 1 {
				continue
			}

			op, _ := ch.Children[0].Value.(int64)
			attr, _ := ch.Children[1].Children[0].Value.(string)

			m := Mod{Op: [...]string{opAdd, opDelete, opReplace}[op%3], Attr: attr}

			if len(ch.Children[1].Children) > 1 {
				for _, v := range ch.Children[1].Children[1].Children {
					s, _ := v.Value.(string)
					m.Values = append(m.Values, s)
				}
			}

			mods = append(mods, m)
		}

		if err := ss.s.opts.Directory.Modify(dn, mods); err != nil {
			code = resultNoSuchObject
		}
	}

	return writePacket(ss.conn, result(id, opModifyResponse, code)) == nil
}

// String renders an entry as LDIF, for failure messages.
func (e *Entry) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "dn: %s\n", e.DN)

	for _, a := range e.attrs {
		for _, v := range a.values {
			fmt.Fprintf(&b, "%s: %s\n", a.name, v)
		}
	}

	return b.String()
}
