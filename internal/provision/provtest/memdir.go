// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package provtest is an in-memory directory for the tests of the provisioning:
// it stores entries as the LDAP server would, answers the searches the
// provisioning makes and records what it was asked to write, so a test checks
// the entries a command leaves (DN, object classes, attributes) rather than the
// calls that made them.
package provtest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/zextras/carbonio-configd/internal/provision"
)

// Base is the base DN of the configuration branch the directory serves.
const Base = "cn=zimbra"

// ModifyCall is one ModifyValues call.
type ModifyCall struct {
	DN                string
	Add, Del, Replace map[string][]string
}

// Dir is a provision.Directory in memory. The zero value is not usable: use New.
type Dir struct {
	mu      sync.Mutex
	entries map[string]*stored
	// Calls are the ModifyValues calls, in order.
	Calls []ModifyCall
	// Passwords are the passwords set by SetPassword, by DN.
	Passwords map[string]string
	// Deleted are the DNs deleted, in order.
	Deleted []string
	// FailModify makes ModifyValues of the DN fail with the error.
	FailModify map[string]error
	// FailAdd makes Add of the DN fail with the error.
	FailAdd map[string]error
	// FailPassword makes SetPassword of the DN fail with the error.
	FailPassword map[string]error
}

// stored is an entry: its DN as written and its attributes by lower-case name,
// keeping the case of the first name written.
type stored struct {
	dn    string
	attrs map[string][]string
}

// New returns an empty directory.
func New() *Dir {
	return &Dir{
		entries:      map[string]*stored{},
		Passwords:    map[string]string{},
		FailModify:   map[string]error{},
		FailAdd:      map[string]error{},
		FailPassword: map[string]error{},
	}
}

// normalize is the comparison form of a DN.
func normalize(dn string) string {
	parts := strings.Split(dn, ",")
	for i, p := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(p))
	}

	return strings.Join(parts, ",")
}

// BaseDN implements provision.Directory.
func (d *Dir) BaseDN() string { return Base }

// Close does nothing.
func (d *Dir) Close() error { return nil }

// Set gives the entry the values of the attribute, creating the entry; no
// value removes the attribute. It is how a test arranges the directory.
func (d *Dir) Set(dn, attr string, values ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	e := d.entry(dn)
	if len(values) == 0 {
		delete(e.attrs, strings.ToLower(attr))

		return
	}

	e.attrs[strings.ToLower(attr)] = slices.Clone(values)
}

func (d *Dir) entry(dn string) *stored {
	key := normalize(dn)
	if e, ok := d.entries[key]; ok {
		return e
	}

	e := &stored{dn: dn, attrs: map[string][]string{}}
	d.entries[key] = e

	return e
}

// Values returns the values of the attribute of the entry.
func (d *Dir) Values(dn, attr string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if e, ok := d.entries[normalize(dn)]; ok {
		return slices.Clone(e.attrs[strings.ToLower(attr)])
	}

	return nil
}

// First returns the first value of the attribute, "" when there is none.
func (d *Dir) First(dn, attr string) string {
	if v := d.Values(dn, attr); len(v) > 0 {
		return v[0]
	}

	return ""
}

// Exists reports whether the entry exists.
func (d *Dir) Exists(dn string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, ok := d.entries[normalize(dn)]

	return ok
}

// DNS lists the DNs of the entries, sorted.
func (d *Dir) DNS() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	dns := make([]string, 0, len(d.entries))
	for _, e := range d.entries {
		dns = append(dns, e.dn)
	}

	slices.Sort(dns)

	return dns
}

// Attrs returns a copy of the attributes of the entry by lower-case name; nil
// when the entry does not exist.
func (d *Dir) Attrs(dn string) map[string][]string {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, ok := d.entries[normalize(dn)]
	if !ok {
		return nil
	}

	out := make(map[string][]string, len(e.attrs))
	for k, v := range e.attrs {
		out[k] = slices.Clone(v)
	}

	return out
}

// Add implements provision.Directory.
func (d *Dir) Add(dn string, attrs map[string][]string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.FailAdd[dn]; err != nil {
		return err
	}

	if _, ok := d.entries[normalize(dn)]; ok {
		return provision.ErrEntryExists
	}

	e := d.entry(dn)
	for k, v := range attrs {
		e.attrs[strings.ToLower(k)] = slices.Clone(v)
	}

	return nil
}

// Delete implements provision.Directory.
func (d *Dir) Delete(dn string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.entries[normalize(dn)]; !ok {
		return fmt.Errorf("no such object: %s", dn)
	}

	delete(d.entries, normalize(dn))
	d.Deleted = append(d.Deleted, dn)

	return nil
}

// SetPassword implements provision.Directory: it records the password, as the
// server would hash it.
func (d *Dir) SetPassword(dn, password string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.FailPassword[dn]; err != nil {
		return err
	}

	if _, ok := d.entries[normalize(dn)]; !ok {
		return fmt.Errorf("no such object: %s", dn)
	}

	d.Passwords[dn] = password

	return nil
}

// ModifyValues implements provision.Directory with the semantics of the real
// client: values added are not duplicated, values deleted that are not there are
// ignored, no value deletes the attribute, replace sets the values. An entry that
// does not exist is created, which a real directory would refuse: the tests that
// modify an entry they have not arranged are not made to fail on it.
func (d *Dir) ModifyValues(dn string, add, del, replace map[string][]string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.Calls = append(d.Calls, ModifyCall{dn, add, del, replace})

	if err := d.FailModify[dn]; err != nil {
		return err
	}

	e := d.entry(dn)

	for attr, values := range del {
		key := strings.ToLower(attr)
		if len(values) == 0 {
			delete(e.attrs, key)

			continue
		}

		drop := func(v string) bool { return slices.Contains(values, v) }

		e.attrs[key] = slices.DeleteFunc(slices.Clone(e.attrs[key]), drop)
		if len(e.attrs[key]) == 0 {
			delete(e.attrs, key)
		}
	}

	for attr, values := range add {
		key := strings.ToLower(attr)

		for _, v := range values {
			if !slices.Contains(e.attrs[key], v) {
				e.attrs[key] = append(e.attrs[key], v)
			}
		}
	}

	for attr, values := range replace {
		e.attrs[strings.ToLower(attr)] = slices.Clone(values)
	}

	return nil
}

// Search implements provision.Directory for the filters the provisioning makes:
// (attr=value), (attr=*), and & and | of them. An empty base is the whole
// directory.
func (d *Dir) Search(base string, scope provision.Scope, filter string, _ ...string) ([]provision.Entry, error) {
	f, rest, err := parseFilter(filter)
	if err != nil || rest != "" {
		return nil, errors.Join(err, fmt.Errorf("unsupported filter %q", filter))
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	var out []provision.Entry

	for _, e := range d.entries {
		if !inScope(base, scope, e.dn) || !f(e.attrs) {
			continue
		}

		attrs := make(map[string][]string, len(e.attrs))
		for k, v := range e.attrs {
			attrs[k] = slices.Clone(v)
		}

		out = append(out, provision.Entry{DN: e.dn, Attrs: attrs})
	}

	slices.SortFunc(out, func(a, b provision.Entry) int { return strings.Compare(a.DN, b.DN) })

	return out, nil
}

// inScope reports whether dn is in the scope of the search at base.
func inScope(base string, scope provision.Scope, dn string) bool {
	b, n := normalize(base), normalize(dn)

	switch scope {
	case provision.ScopeBase:
		return n == b
	case provision.ScopeOne:
		rdn := n

		if b != "" {
			var found bool

			if rdn, found = strings.CutSuffix(n, ","+b); !found {
				return false
			}
		}

		return rdn != "" && !strings.Contains(rdn, ",")
	default:
		return b == "" || n == b || strings.HasSuffix(n, ","+b)
	}
}

// matcher tests the attributes of an entry.
type matcher func(attrs map[string][]string) bool

// parseFilter parses one filter and returns what follows it.
func parseFilter(s string) (matcher, string, error) {
	if !strings.HasPrefix(s, "(") {
		return nil, s, errors.New("filter does not start with (")
	}

	if len(s) > 1 && (s[1] == '&' || s[1] == '|') {
		return parseList(s)
	}

	end := strings.Index(s, ")")
	if end < 0 {
		return nil, s, errors.New("filter is not closed")
	}

	attr, value, ok := strings.Cut(s[1:end], "=")
	if !ok {
		return nil, s, errors.New("filter has no =")
	}

	attr = strings.ToLower(attr)

	if value == "*" {
		return func(a map[string][]string) bool { return true }, s[end+1:], nil
	}

	want := unescape(value)

	return func(a map[string][]string) bool {
		return slices.ContainsFunc(a[attr], func(v string) bool { return strings.EqualFold(v, want) })
	}, s[end+1:], nil
}

// parseList parses (&...) and (|...).
func parseList(s string) (matcher, string, error) {
	and := s[1] == '&'
	rest := s[2:]

	var subs []matcher

	for strings.HasPrefix(rest, "(") {
		m, r, err := parseFilter(rest)
		if err != nil {
			return nil, s, err
		}

		subs, rest = append(subs, m), r
	}

	if !strings.HasPrefix(rest, ")") {
		return nil, s, errors.New("filter list is not closed")
	}

	return func(a map[string][]string) bool {
		for _, m := range subs {
			if m(a) != and {
				return !and
			}
		}

		return and
	}, rest[1:], nil
}

// unescape reverses the \XX escapes of a filter value.
func unescape(v string) string {
	var b strings.Builder

	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+2 < len(v) {
			var c byte

			if _, err := fmt.Sscanf(v[i+1:i+3], "%02x", &c); err == nil {
				b.WriteByte(c)

				i += 2

				continue
			}
		}

		b.WriteByte(v[i])
	}

	return b.String()
}
