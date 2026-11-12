// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"regexp"
	"slices"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// The verbs RunProv writes to LDAP.
const (
	provMCF = "mcf"
	provMS  = "ms"
	provMC  = "mc"
)

// provTarget is the entry a modify command changes.
type provTarget int

const (
	// provGlobal is the global config, "mcf".
	provGlobal provTarget = iota
	// provServer is a server entry, "ms host".
	provServer
	// provCOS is a class of service, "mc name".
	provCOS
)

// provChange is one mcf, ms or mc command as the LDAP modification it makes.
type provChange struct {
	target provTarget
	// name is the server or class of service (empty for the global config).
	name string
	// add, del and replace are the arguments of ModifyValues.
	add, del, replace map[string][]string
}

// dn is the entry the change applies to, under the directory base baseDN.
func (c provChange) dn(baseDN string) string {
	switch c.target {
	case provServer:
		return "cn=" + goldap.EscapeDN(c.name) + ",cn=servers," + baseDN
	case provCOS:
		return "cn=" + goldap.EscapeDN(c.name) + ",cn=cos," + baseDN
	default:
		return "cn=config," + baseDN
	}
}

var provIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)

// provParse turns args into the LDAP modification ModifyValues applies, with
// zmprov's argument semantics (StringUtil.keyValueArrayToMultiMap and
// ZLdapHelper.getModList):
//
//   - "attr v..." replaces the attribute with the values given, in order; one
//     empty value unsets it;
//   - "+attr v" adds the value, which is a success when the entry has it;
//   - "-attr v" removes the value, a success when it is not there.
//
// It reports false for what zmprov must run: another verb, an entry given by
// id, an attribute that is not in provAttrList, a value its checks reject
// (zmprov then reports the error), an odd number of arguments, +attr or -attr
// together with attr, an empty value among several, or a binary attribute
// whose file cannot be read.
func (e *Env) provParse(args []string) (provChange, bool) {
	var c provChange

	rest := args[1:]

	switch args[0] {
	case provMCF:
		c.target = provGlobal
	case provMS, provMC:
		c.target = provServer
		if args[0] == provMC {
			c.target = provCOS
		}

		if len(rest) == 0 || provIDRE.MatchString(rest[0]) {
			return c, false
		}

		c.name, rest = rest[0], rest[1:]
	default:
		return c, false
	}

	if len(rest) == 0 || len(rest)%2 != 0 {
		return c, false
	}

	c.add, c.del, c.replace = map[string][]string{}, map[string][]string{}, map[string][]string{}

	if !e.provFill(&c, rest) {
		return c, false
	}

	return c, true
}

// provFill adds the name/value pairs kv to c; false when zmprov must run them.
func (e *Env) provFill(c *provChange, kv []string) bool {
	plain, prefixed := map[string]bool{}, map[string]bool{}

	for i := 0; i < len(kv); i += 2 {
		name, value := kv[i], kv[i+1]
		if name == "" {
			return false
		}

		op := name[0]
		if op == '+' || op == '-' {
			name = name[1:]
			prefixed[strings.ToLower(name)] = true
		} else {
			op = 0
			plain[strings.ToLower(name)] = true
		}

		value, ok := e.provValue(name, op, value)
		if !ok {
			return false
		}

		provApply(c, op, name, value)
	}

	for name := range prefixed {
		if plain[name] {
			return false
		}
	}

	return provUnset(c)
}

// provValue is the value to store for the attribute name: the content of the
// file for a binary attribute. False when the attribute is not allowlisted,
// the file cannot be read, or the value fails the checks (removals are not
// checked: a value that is not there is not removed).
func (e *Env) provValue(name string, op byte, value string) (string, bool) {
	spec, ok := provAttrIndex()[strings.ToLower(name)]
	if !ok {
		return "", false
	}

	if value != "" && spec.kind == provBinary {
		data, err := e.Sys.FS.ReadFile(value)
		if err != nil {
			return "", false
		}

		value = string(data)
	}

	return value, op == '-' || spec.valid(value)
}

// provApply records value for the attribute in the list the operator names.
func provApply(c *provChange, op byte, name, value string) {
	switch op {
	case '+':
		c.add[name] = append(c.add[name], value)
	case '-':
		// Removing a value that is not there does nothing, whatever it is.
		if value != "" {
			c.del[name] = append(c.del[name], value)
		}
	default:
		c.replace[name] = append(c.replace[name], value)
	}
}

// provUnset turns the replacement of an attribute by one empty value into the
// removal of the attribute, as zmprov does; false for an empty value among
// several, which zmprov would send to LDAP as a value.
func provUnset(c *provChange) bool {
	for name, values := range c.replace {
		switch {
		case len(values) == 1 && values[0] == "":
			delete(c.replace, name)

			c.del[name] = nil
		case slices.Contains(values, ""):
			return false
		}
	}

	return true
}
