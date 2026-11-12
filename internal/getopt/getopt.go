// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package getopt reads command lines as Perl's Getopt::Long does with its
// defaults, for the commands that replace Perl scripts and must accept what
// the scripts accepted.
package getopt

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Help is the name of the help option of the ported scripts.
const Help = "help"

// prefix starts the option errors, worded as Getopt::Long does.
const prefix = "Option "

// Kind says what a Getopt::Long option takes.
type Kind int

const (
	// Flag takes nothing ("help").
	Flag Kind = iota
	// String takes a mandatory string ("backup=s").
	String
	// Int takes a mandatory integer ("sid=i").
	Int
	// OptInt takes an optional integer ("new:i"): a bare option takes the
	// next argument only when it is a number.
	OptInt
)

// Spec is one option of a Getopt::Long specification such as "s|sid=i":
// names[0] is the name the result is stored under.
type Spec struct {
	Names []string
	Kind  Kind
}

// Result is what Parse read.
type Result struct {
	// given holds the options that were present, by primary name.
	Given map[string]bool
	Strs  map[string]string
	Ints  map[string]int
	// bare holds the options of OptInt that came without a number.
	Bare    map[string]bool
	Rest    []string
	AllGood bool
}

var optIntRE = regexp.MustCompile(`^[-+]?\d+$`)

// Has reports whether the option was given.
func (r *Result) Has(name string) bool { return r.Given[name] }

// Parse reads args as Perl's Getopt::Long does with its defaults, for
// the options in specs: a single or double dash, any case, a unique
// abbreviation, "-name=value" or "-name value", "--" ends the options and the
// arguments that are not options are kept in rest, in order. What
// Getopt::Long complains about goes to stderr and clears AllGood; the other
// options are still read, as it does.
func Parse(args []string, specs []Spec, stderr io.Writer) *Result {
	r := &Result{
		Given: map[string]bool{}, Bare: map[string]bool{}, Strs: map[string]string{}, Ints: map[string]int{}, AllGood: true,
	}

	for i := 0; i < len(args); {
		a := args[i]
		i++

		switch {
		case a == "--":
			r.Rest = append(r.Rest, args[i:]...)

			return r
		case len(a) < 2 || a[0] != '-':
			r.Rest = append(r.Rest, a)
		default:
			var msg string

			if i, msg = r.option(a, args, i, specs); msg != "" {
				_, _ = fmt.Fprintln(stderr, msg)

				r.AllGood = false
			}
		}
	}

	return r
}

// option applies the option arg and returns the index of the next argument,
// after any value it consumed, and what Getopt::Long would complain about.
func (r *Result) option(arg string, args []string, next int, specs []Spec) (after int, msg string) {
	name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")

	spec, msg := optMatch(name, specs)
	if msg != "" {
		return next, msg
	}

	primary := spec.Names[0]

	if spec.Kind == Flag {
		if hasValue {
			return next, prefix + primary + " does not take an argument"
		}

		r.Given[primary] = true

		return next, ""
	}

	return r.value(spec, value, hasValue, args, next)
}

// value reads the value of an option that takes one: the text after "=", else
// the next argument (for an optional integer, only when it is a number).
func (r *Result) value(spec Spec, value string, hasValue bool, args []string, next int) (after int, msg string) {
	primary := spec.Names[0]

	if spec.Kind == OptInt && (!hasValue || value == "") {
		if !hasValue && next < len(args) && optIntRE.MatchString(args[next]) {
			value, next = args[next], next+1
		}
	} else if !hasValue {
		if next >= len(args) {
			return next, prefix + primary + " requires an argument"
		}

		value, next = args[next], next+1
	}

	switch {
	case spec.Kind == String:
		r.Strs[primary] = value
	case value == "":
		r.Bare[primary] = true
	default:
		n, err := strconv.Atoi(value)
		if err != nil || !optIntRE.MatchString(value) {
			return next, fmt.Sprintf("Value %q invalid for option %s (number expected)", value, primary)
		}

		r.Ints[primary] = n
	}

	r.Given[primary] = true

	return next, ""
}

// optMatch is the option that name stands for: an exact name, else the only
// option one of whose names it abbreviates; otherwise the complaint.
func optMatch(name string, specs []Spec) (match Spec, complaint string) {
	name = strings.ToLower(name)

	var found []Spec

	for _, s := range specs {
		if slices.Contains(s.Names, name) {
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
		return Spec{}, "Unknown option: " + name
	case 1:
		return found[0], ""
	}

	names := make([]string, len(found))
	for i, s := range found {
		names[i] = s.Names[0]
	}

	return Spec{}, prefix + name + " is ambiguous (" + strings.Join(names, ", ") + ")"
}

func optAbbreviates(name string, s Spec) bool {
	for _, n := range s.Names {
		if strings.HasPrefix(n, name) {
			return true
		}
	}

	return false
}
