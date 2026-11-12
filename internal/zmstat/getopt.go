// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package zmstat

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

type optKind int

const (
	kindBool optKind = iota
	kindInt
	kindString
)

// optDef is an option as Getopt::Long's GetOptions spec "name", "name=i" or
// "name=s" declares it.
type optDef struct {
	name string
	kind optKind
}

// parsedOpts is what GetOptions stored: the values by option name and the
// arguments left in @ARGV.
type parsedOpts struct {
	ints  map[string]int
	strs  map[string]string
	bools map[string]bool
	rest  []string
}

// parseGetopt is Getopt::Long::GetOptions in its default configuration:
// options start with - or --, may be abbreviated to a unique prefix, ignore
// case, take their value as -name=value or as the next argument, and may be
// mixed with the arguments (which are kept in order). The warnings it prints
// go to warn; ok is false when there was one.
func parseGetopt(defs []optDef, args []string, warn io.Writer) (*parsedOpts, bool) {
	po := &parsedOpts{ints: map[string]int{}, strs: map[string]string{}, bools: map[string]bool{}}
	ok := true

	for i := 0; i < len(args); i++ {
		a := args[i]

		switch {
		case a == "--":
			po.rest = append(po.rest, args[i+1:]...)

			return po, ok
		case len(a) > 1 && a[0] == '-':
			used, good := po.option(defs, a, args[i+1:], warn)
			i += used
			ok = ok && good
		default:
			po.rest = append(po.rest, a)
		}
	}

	return po, ok
}

// option handles one option argument; next are the arguments after it. It
// returns how many of those it consumed as the value.
func (po *parsedOpts) option(defs []optDef, arg string, next []string, warn io.Writer) (int, bool) {
	name, val, hasVal := strings.Cut(strings.TrimLeft(arg, "-"), "=")

	def, ok := matchOption(defs, name, warn)
	if !ok {
		return 0, false
	}

	if def.kind == kindBool {
		if hasVal {
			_, _ = fmt.Fprintf(warn, "Option %s does not take an argument\n", def.name)

			return 0, false
		}

		po.bools[def.name] = true

		return 0, true
	}

	used := 0

	if !hasVal {
		if len(next) == 0 {
			_, _ = fmt.Fprintf(warn, "Option %s requires an argument\n", def.name)

			return 0, false
		}

		val, used = next[0], 1
	}

	if def.kind == kindString {
		po.strs[def.name] = val

		return used, true
	}

	n, err := strconv.Atoi(val)
	if err != nil || strings.TrimLeft(val, "+-0123456789") != "" {
		_, _ = fmt.Fprintf(warn, "Value \"%s\" invalid for option %s (number expected)\n", val, def.name)

		return used, false
	}

	po.ints[def.name] = n

	return used, true
}

// matchOption finds the option name stands for: an exact match, else the only
// option it is a prefix of.
func matchOption(defs []optDef, name string, warn io.Writer) (optDef, bool) {
	lower := strings.ToLower(name)

	var found []optDef

	for _, d := range defs {
		if d.name == lower {
			return d, true
		}

		if strings.HasPrefix(d.name, lower) {
			found = append(found, d)
		}
	}

	switch len(found) {
	case 0:
		_, _ = fmt.Fprintf(warn, "Unknown option: %s\n", name)

		return optDef{}, false
	case 1:
		return found[0], true
	}

	names := make([]string, len(found))
	for i, d := range found {
		names[i] = d.name
	}

	slices.Sort(names)
	_, _ = fmt.Fprintf(warn, "Option %s is ambiguous (%s)\n", name, strings.Join(names, ", "))

	return optDef{}, false
}
