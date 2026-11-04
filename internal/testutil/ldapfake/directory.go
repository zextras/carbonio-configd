// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapfake

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Entry is one directory entry: its DN as written in the LDIF and its
// attributes in insertion order.
type Entry struct {
	DN    string
	attrs []*attribute
}

type attribute struct {
	name   string
	values []string
}

// Values returns the values of attr (case-insensitive name), or nil.
func (e *Entry) Values(attr string) []string {
	if a := e.find(attr); a != nil {
		return append([]string(nil), a.values...)
	}

	return nil
}

func (e *Entry) find(attr string) *attribute {
	for _, a := range e.attrs {
		if strings.EqualFold(a.name, attr) {
			return a
		}
	}

	return nil
}

func (e *Entry) add(attr string, values ...string) {
	if a := e.find(attr); a != nil {
		a.values = append(a.values, values...)

		return
	}

	e.attrs = append(e.attrs, &attribute{name: attr, values: append([]string(nil), values...)})
}

func (e *Entry) replace(attr string, values ...string) {
	if len(values) == 0 {
		e.remove(attr, nil)

		return
	}

	if a := e.find(attr); a != nil {
		a.values = append([]string(nil), values...)

		return
	}

	e.add(attr, values...)
}

// remove deletes the given values of attr, or the whole attribute when
// values is empty.
func (e *Entry) remove(attr string, values []string) {
	for i, a := range e.attrs {
		if !strings.EqualFold(a.name, attr) {
			continue
		}

		if len(values) == 0 {
			e.attrs = append(e.attrs[:i], e.attrs[i+1:]...)

			return
		}

		kept := a.values[:0:0]

		for _, v := range a.values {
			if !containsFold(values, v) {
				kept = append(kept, v)
			}
		}

		if len(kept) == 0 {
			e.attrs = append(e.attrs[:i], e.attrs[i+1:]...)
		} else {
			a.values = kept
		}

		return
	}
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}

	return false
}

// modOp is one change of a modify record.
type modOp struct {
	op     string // add, delete, replace
	attr   string
	values []string
}

// record is one LDIF record: a plain entry (changetype add), a delete, or a
// modify.
type record struct {
	dn         string
	changeType string
	attrs      [][2]string // attribute/value pairs of an add record
	mods       []modOp
}

// Directory is an in-memory LDAP directory loaded from LDIF.
type Directory struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	order   []string
}

// NewDirectory returns an empty directory.
func NewDirectory() *Directory {
	return &Directory{entries: map[string]*Entry{}}
}

// ApplyLDIF applies the records of an LDIF stream in order. A record without
// changetype adds an entry; changetype add, modify and delete are supported
// (the subset ldapmodify -a accepts that configd's fixtures use). A failing
// record aborts the load, like ldapmodify without -c.
func (d *Directory) ApplyLDIF(r io.Reader) error {
	recs, err := parseLDIF(r)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, rec := range recs {
		if err := d.apply(&rec); err != nil {
			return fmt.Errorf("ldif record %q: %w", rec.dn, err)
		}
	}

	return nil
}

func (d *Directory) apply(rec *record) error {
	key := normDN(rec.dn)

	switch rec.changeType {
	case "", opAdd:
		if _, ok := d.entries[key]; ok {
			return fmt.Errorf("entry already exists")
		}

		e := &Entry{DN: rec.dn}
		for _, kv := range rec.attrs {
			e.add(kv[0], kv[1])
		}

		d.entries[key] = e
		d.order = append(d.order, key)
	case opDelete:
		if _, ok := d.entries[key]; !ok {
			return fmt.Errorf("no such entry")
		}

		delete(d.entries, key)

		for i, k := range d.order {
			if k == key {
				d.order = append(d.order[:i], d.order[i+1:]...)

				break
			}
		}
	case "modify":
		e, ok := d.entries[key]
		if !ok {
			return fmt.Errorf("no such entry")
		}

		for _, m := range rec.mods {
			applyMod(e, m)
		}
	default:
		return fmt.Errorf("unsupported changetype %q", rec.changeType)
	}

	return nil
}

// LDIF modify operations.
const (
	opAdd     = "add"
	opDelete  = "delete"
	opReplace = "replace"
)

func applyMod(e *Entry, m modOp) {
	switch m.op {
	case opAdd:
		e.add(m.attr, m.values...)
	case opReplace:
		e.replace(m.attr, m.values...)
	case opDelete:
		e.remove(m.attr, m.values)
	}
}

// Modify applies one modify operation to dn, as an LDAP modifyRequest does.
func (d *Directory) Modify(dn string, mods []Mod) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, ok := d.entries[normDN(dn)]
	if !ok {
		return fmt.Errorf("no such entry %q", dn)
	}

	for _, m := range mods {
		applyMod(e, modOp{op: m.Op, attr: m.Attr, values: m.Values})
	}

	return nil
}

// Mod is one change of Directory.Modify: Op is "add", "delete" or "replace".
type Mod struct {
	Op     string
	Attr   string
	Values []string
}

// Get returns a copy of the entry at dn, or nil.
func (d *Directory) Get(dn string) *Entry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	e, ok := d.entries[normDN(dn)]
	if !ok {
		return nil
	}

	return e.clone()
}

func (e *Entry) clone() *Entry {
	c := &Entry{DN: e.DN}
	for _, a := range e.attrs {
		c.attrs = append(c.attrs, &attribute{name: a.name, values: append([]string(nil), a.values...)})
	}

	return c
}

// normDN lower-cases a DN and drops the blanks around its separators.
func normDN(dn string) string {
	parts := strings.Split(dn, ",")
	for i, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if ok {
			p = strings.TrimSpace(k) + "=" + strings.TrimSpace(v)
		}

		parts[i] = strings.ToLower(strings.TrimSpace(p))
	}

	return strings.Join(parts, ",")
}

// parseLDIF reads LDIF records (RFC 2849, without URL values or controls).
func parseLDIF(r io.Reader) ([]record, error) {
	var (
		recs  []record
		lines []string
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)

	flush := func() error {
		if len(lines) == 0 {
			return nil
		}

		rec, err := parseRecord(lines)
		lines = nil

		if err != nil {
			return err
		}

		if rec.dn != "" {
			recs = append(recs, rec)
		}

		return nil
	}

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")

		switch {
		case strings.HasPrefix(line, "#"):
			continue
		case line == "":
			if err := flush(); err != nil {
				return nil, err
			}
		case line[0] == ' ' && len(lines) > 0:
			lines[len(lines)-1] += line[1:]
		default:
			lines = append(lines, line)
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read ldif: %w", err)
	}

	if err := flush(); err != nil {
		return nil, err
	}

	return recs, nil
}

func splitLine(line string) (name, value string, err error) {
	name, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", fmt.Errorf("malformed ldif line %q", line)
	}

	if strings.HasPrefix(rest, ":") {
		raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[1:]))
		if derr != nil {
			return "", "", fmt.Errorf("ldif line %q: %w", name, derr)
		}

		return name, string(raw), nil
	}

	return name, strings.TrimPrefix(rest, " "), nil
}

func parseRecord(lines []string) (record, error) {
	var rec record

	pairs := make([][2]string, 0, len(lines))

	for _, l := range lines {
		if l == "-" {
			pairs = append(pairs, [2]string{"-", ""})

			continue
		}

		k, v, err := splitLine(l)
		if err != nil {
			return rec, err
		}

		if strings.EqualFold(k, "version") && rec.dn == "" && len(pairs) == 0 {
			continue
		}

		pairs = append(pairs, [2]string{k, v})
	}

	if len(pairs) == 0 {
		return rec, nil
	}

	if !strings.EqualFold(pairs[0][0], "dn") {
		return rec, fmt.Errorf("ldif record does not start with dn: %q", pairs[0][0])
	}

	rec.dn = pairs[0][1]
	pairs = pairs[1:]

	if len(pairs) > 0 && strings.EqualFold(pairs[0][0], "changetype") {
		rec.changeType = strings.ToLower(pairs[0][1])
		pairs = pairs[1:]
	}

	if rec.changeType != "modify" {
		rec.attrs = pairs

		return rec, nil
	}

	mods, err := parseMods(pairs)
	rec.mods = mods

	return rec, err
}

// parseMods groups the pairs of a modify record into its operations.
func parseMods(pairs [][2]string) ([]modOp, error) {
	var mods []modOp

	open := false

	for _, kv := range pairs {
		switch {
		case kv[0] == "-":
			open = false
		case !open:
			op := strings.ToLower(kv[0])
			if op != opAdd && op != opDelete && op != opReplace {
				return nil, fmt.Errorf("unsupported modify operation %q", kv[0])
			}

			mods = append(mods, modOp{op: op, attr: kv[1]})
			open = true
		default:
			last := &mods[len(mods)-1]
			last.values = append(last.values, kv[1])
		}
	}

	return mods, nil
}
