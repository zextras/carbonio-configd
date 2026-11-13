// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"slices"
	"strings"
)

// Mods is the attribute map of a zmprov command, as ProvUtil builds it
// (StringUtil.keyValueArrayToMultiMap): the key is the attribute name as given,
// "attr" to replace, "+attr" to add a value, "-attr" to remove one; the values
// are those given for the key, in order.
type Mods map[string][]string

// ParseMods builds the attribute map of the name/value arguments kv. The
// value of a binary attribute is the path of the file that holds it and is
// replaced by the content, as ProvUtil getMapAndCheck does; readFile reads it.
func ParseMods(schema *Schema, readFile func(string) ([]byte, error), kv []string) (Mods, error) {
	if len(kv)%2 != 0 {
		return nil, invalidRequest("not enough arguments: every attribute needs a value")
	}

	m := Mods{}

	for i := 0; i < len(kv); i += 2 {
		key, value := kv[i], kv[i+1]
		if key == "" {
			return nil, fail(CodeInvalidAttrName, "empty attr name found")
		}

		_, name := split(key)
		if a := schema.Lookup(name); a != nil {
			// The directory names attributes in any case; the callbacks look
			// them up by their schema name.
			key = strings.TrimSuffix(key, name) + a.Name
		}

		if schema.Binary(name) && value != "" {
			data, err := readFile(value)
			if err != nil {
				return nil, failWith(err, "cannot read the file of %s", key)
			}

			value = string(data)
		}

		m[key] = append(m[key], value)
	}

	return m, nil
}

// split returns the operator of the key ('+', '-' or 0) and the attribute name.
func split(key string) (op byte, name string) {
	if key != "" && (key[0] == '+' || key[0] == '-') {
		return key[0], key[1:]
	}

	return 0, key
}

// has reports whether the map has the key as given.
func (m Mods) has(key string) bool {
	_, ok := m[key]

	return ok
}

// keys returns the keys in a fixed order.
func (m Mods) keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// modChange is the LDAP modification of a Mods, in the arguments of
// Directory.ModifyValues.
type modChange struct {
	add, del, replace map[string][]string
}

// change turns the map into the modification ZLdapHelper.getModList makes:
//
//   - "attr v..." replaces the attribute with the values given; one empty
//     value unsets it;
//   - "+attr v" adds the value;
//   - "-attr v" removes the value (an empty one removes nothing).
//
// An attribute both replaced and added or removed, or replaced with an empty
// value among others, is a request the directory would take for a value
// ("" is not one): refused.
func (m Mods) change() (modChange, error) {
	c := modChange{add: map[string][]string{}, del: map[string][]string{}, replace: map[string][]string{}}
	plain, prefixed := map[string]bool{}, map[string]bool{}

	for _, key := range m.keys() {
		op, name := split(key)

		if op == 0 {
			plain[strings.ToLower(name)] = true
		} else {
			prefixed[strings.ToLower(name)] = true
		}

		for _, v := range m[key] {
			c.record(op, name, v)
		}
	}

	for name := range prefixed {
		if plain[name] {
			return c, invalidRequest("%s is modified with and without +/-", name)
		}
	}

	err := c.unset()

	return c, err
}

// record puts one value in the list of its operator.
func (c *modChange) record(op byte, name, value string) {
	switch op {
	case '+':
		c.add[name] = append(c.add[name], value)
	case '-':
		if value != "" {
			c.del[name] = append(c.del[name], value)
		}
	default:
		c.replace[name] = append(c.replace[name], value)
	}
}

// unset turns the replacement by one empty value into the removal of the
// attribute.
func (c *modChange) unset() error {
	for name, values := range c.replace {
		switch {
		case len(values) == 1 && values[0] == "":
			delete(c.replace, name)

			c.del[name] = nil
		case slices.Contains(values, ""):
			return invalidRequest("%s: an empty value among several", name)
		}
	}

	return nil
}

// singleValue is AttributeCallback.singleValueMod: the value an attribute is
// being set to, ok false when it is being unset.
func (m Mods) singleValue(name string) (value string, ok bool, err error) {
	if len(m["-"+name]) > 0 {
		return "", false, nil
	}

	values := m[name]
	if values == nil {
		values = m["+"+name]
	}

	switch len(values) {
	case 0:
		return "", false, nil
	case 1:
		return values[0], values[0] != "", nil
	default:
		return "", false, invalidRequest("%s is a single-valued attribute", name)
	}
}

// multiKind is how a multi-valued attribute is being modified.
type multiKind int

const (
	multiNone multiKind = iota
	multiAdding
	multiRemoving
	multiReplacing
	multiDeleting
)

// multiValue is AttributeCallback.multiValueMod.
func (m Mods) multiValue(name string) (kind multiKind, values []string) {
	kind, values = multiNone, nil

	if v, ok := m[name]; ok {
		kind, values = multiReplacing, v
		if len(v) == 0 || (len(v) == 1 && v[0] == "") {
			kind, values = multiDeleting, nil
		}
	}

	if kind == multiNone {
		if v := m["+"+name]; v != nil {
			kind, values = multiAdding, v
		} else if v := m["-"+name]; v != nil {
			kind, values = multiRemoving, v
		}
	}

	return kind, values
}
