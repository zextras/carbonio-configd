// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// optHelp is the name of the help option of the ported scripts.
const optHelp = "help"

// optPrefix starts the option errors, worded as Getopt::Long does.
const optPrefix = "Option "

// optKind says what a Getopt::Long option takes.
type optKind int

const (
	// optFlag takes nothing ("help").
	optFlag optKind = iota
	// optString takes a mandatory string ("backup=s").
	optString
	// optInt takes a mandatory integer ("sid=i").
	optInt
	// optOptInt takes an optional integer ("new:i"): a bare option takes the
	// next argument only when it is a number.
	optOptInt
)

// optSpec is one option of a Getopt::Long specification such as "s|sid=i":
// names[0] is the name the result is stored under.
type optSpec struct {
	names []string
	kind  optKind
}

// optResult is what parseGetoptLong read.
type optResult struct {
	// given holds the options that were present, by primary name.
	given map[string]bool
	strs  map[string]string
	ints  map[string]int
	// bare holds the options of optOptInt that came without a number.
	bare    map[string]bool
	rest    []string
	allGood bool
}

var optIntRE = regexp.MustCompile(`^[-+]?\d+$`)

// has reports whether the option was given.
func (r *optResult) has(name string) bool { return r.given[name] }

// parseGetoptLong reads args as Perl's Getopt::Long does with its defaults, for
// the options in specs: a single or double dash, any case, a unique
// abbreviation, "-name=value" or "-name value", "--" ends the options and the
// arguments that are not options are kept in rest, in order. What
// Getopt::Long complains about goes to stderr and clears allGood; the other
// options are still read, as it does.
func parseGetoptLong(args []string, specs []optSpec, stderr io.Writer) *optResult {
	r := &optResult{
		given: map[string]bool{}, bare: map[string]bool{}, strs: map[string]string{}, ints: map[string]int{}, allGood: true,
	}

	for i := 0; i < len(args); {
		a := args[i]
		i++

		switch {
		case a == "--":
			r.rest = append(r.rest, args[i:]...)

			return r
		case len(a) < 2 || a[0] != '-':
			r.rest = append(r.rest, a)
		default:
			var msg string

			if i, msg = r.option(a, args, i, specs); msg != "" {
				_, _ = fmt.Fprintln(stderr, msg)

				r.allGood = false
			}
		}
	}

	return r
}

// option applies the option arg and returns the index of the next argument,
// after any value it consumed, and what Getopt::Long would complain about.
func (r *optResult) option(arg string, args []string, next int, specs []optSpec) (after int, msg string) {
	name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")

	spec, msg := optMatch(name, specs)
	if msg != "" {
		return next, msg
	}

	primary := spec.names[0]

	if spec.kind == optFlag {
		if hasValue {
			return next, optPrefix + primary + " does not take an argument"
		}

		r.given[primary] = true

		return next, ""
	}

	return r.value(spec, value, hasValue, args, next)
}

// value reads the value of an option that takes one: the text after "=", else
// the next argument (for an optional integer, only when it is a number).
func (r *optResult) value(spec optSpec, value string, hasValue bool, args []string, next int) (after int, msg string) {
	primary := spec.names[0]

	if spec.kind == optOptInt && (!hasValue || value == "") {
		if !hasValue && next < len(args) && optIntRE.MatchString(args[next]) {
			value, next = args[next], next+1
		}
	} else if !hasValue {
		if next >= len(args) {
			return next, optPrefix + primary + " requires an argument"
		}

		value, next = args[next], next+1
	}

	switch {
	case spec.kind == optString:
		r.strs[primary] = value
	case value == "":
		r.bare[primary] = true
	default:
		n, err := strconv.Atoi(value)
		if err != nil || !optIntRE.MatchString(value) {
			return next, fmt.Sprintf("Value %q invalid for option %s (number expected)", value, primary)
		}

		r.ints[primary] = n
	}

	r.given[primary] = true

	return next, ""
}

// optMatch is the option that name stands for: an exact name, else the only
// option one of whose names it abbreviates; otherwise the complaint.
func optMatch(name string, specs []optSpec) (match optSpec, complaint string) {
	name = strings.ToLower(name)

	var found []optSpec

	for _, s := range specs {
		if slices.Contains(s.names, name) {
			return s, ""
		}
	}

	for _, s := range specs {
		if name != "" && optAbbreviates(name, s) {
			found = append(found, s)
		}
	}

	switch len(found) {
	case 0:
		return optSpec{}, "Unknown option: " + name
	case 1:
		return found[0], ""
	}

	names := make([]string, len(found))
	for i, s := range found {
		names[i] = s.names[0]
	}

	return optSpec{}, optPrefix + name + " is ambiguous (" + strings.Join(names, ", ") + ")"
}

func optAbbreviates(name string, s optSpec) bool {
	for _, n := range s.names {
		if strings.HasPrefix(n, name) {
			return true
		}
	}

	return false
}
