// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapcmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// globalConfigDN is the entry "mcf" modifies (zmprov mcf).
const globalConfigDN = "cn=config,cn=zimbra"

// defaultSearchBase and defaultSearchFilter apply when --base / --filter are
// omitted.
const (
	defaultSearchBase   = "cn=zimbra"
	defaultSearchFilter = "(objectClass=*)"
)

// mcfChange is the parsed form of the mcf arguments.
type mcfChange struct {
	add, del, replace map[string][]string
}

// parseMcf reads zmprov's "[+|-]attr value" pairs. A "+" prefix adds the value
// to a multi-valued attribute, "-" removes it (an empty value removes the whole
// attribute), a bare attribute replaces; repeating an attribute accumulates its
// values.
func parseMcf(args []string) (*mcfChange, error) {
	if len(args) == 0 || len(args)%2 != 0 {
		return nil, errors.New("mcf needs [+|-]<attr> <value> pairs")
	}

	c := &mcfChange{
		add:     make(map[string][]string),
		del:     make(map[string][]string),
		replace: make(map[string][]string),
	}

	for i := 0; i < len(args); i += 2 {
		if err := c.addPair(args[i], args[i+1]); err != nil {
			return nil, err
		}
	}

	return c, nil
}

// addPair records one "[+|-]attr value" pair.
func (c *mcfChange) addPair(name, value string) error {
	switch {
	case strings.HasPrefix(name, "+"):
		return c.addValue(name, value)
	case strings.HasPrefix(name, "-"):
		return c.delValue(name, value)
	default:
		if name == "" {
			return errors.New("mcf: empty attribute name")
		}

		if value == "" {
			c.replace[name] = nil
		} else {
			c.replace[name] = append(c.replace[name], value)
		}

		return nil
	}
}

func (c *mcfChange) addValue(name, value string) error {
	attr := name[1:]
	if attr == "" || value == "" {
		return fmt.Errorf("mcf: %q needs an attribute and a non-empty value", name)
	}

	c.add[attr] = append(c.add[attr], value)

	return nil
}

// delValue records a "-attr value"; an empty value removes the attribute outright.
func (c *mcfChange) delValue(name, value string) error {
	attr := name[1:]
	if attr == "" {
		return fmt.Errorf("mcf: %q has no attribute", name)
	}

	if value == "" {
		c.del[attr] = nil
	} else if values, whole := c.del[attr]; !whole || len(values) > 0 {
		c.del[attr] = append(values, value)
	}

	return nil
}

func modifyConfig(conn Conn, args []string) error {
	change, err := parseMcf(args)
	if err != nil {
		return err
	}

	return conn.ModifyValues(globalConfigDN, change.add, change.del, change.replace)
}

// searchScope maps --scope to the LDAP scope; empty means sub.
func searchScope(scope string) (int, error) {
	switch scope {
	case ScopeBase:
		return goldap.ScopeBaseObject, nil
	case ScopeOne:
		return goldap.ScopeSingleLevel, nil
	case ScopeSub, "":
		return goldap.ScopeWholeSubtree, nil
	default:
		return 0, fmt.Errorf("unknown scope %q (base, one or sub)", scope)
	}
}

func search(stdout io.Writer, conn Conn, opts *Options) error {
	scope, err := searchScope(opts.Scope)
	if err != nil {
		return err
	}

	base, filter := opts.Base, opts.Filter
	if base == "" {
		base = defaultSearchBase
	}

	if filter == "" {
		filter = defaultSearchFilter
	}

	result, err := conn.Search(base, filter, opts.Attrs, scope)
	if err != nil {
		return err
	}

	return printEntries(stdout, result.Entries, opts.JSON)
}
