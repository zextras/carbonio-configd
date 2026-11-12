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
	// NegFlag takes nothing and may be negated ("verbose!"): "-noverbose" and
	// "-no-verbose" are given with Negated set.
	NegFlag
	// StringList takes a mandatory string each time it is given, and
	// collects them ("deploy=s@").
	StringList
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
	// Lists holds the values of the options of StringList, in order.
	Lists map[string][]string
	// Negated holds the options of NegFlag that were given negated.
	Negated map[string]bool
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
		Given: map[string]bool{}, Bare: map[string]bool{}, Strs: map[string]string{}, Ints: map[string]int{},
		Lists: map[string][]string{}, Negated: map[string]bool{}, AllGood: true,
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

	spec, neg, msg := optMatch(name, specs)
	if msg != "" {
		return next, msg
	}

	primary := spec.Names[0]

	if spec.Kind == Flag || spec.Kind == NegFlag {
		if hasValue {
			return next, prefix + primary + " does not take an argument"
		}

		r.Given[primary] = true
		r.Negated[primary] = neg

		return next, ""
	}

	return r.value(spec, value, hasValue, args, next)
}

// operand is the value of an option that takes one: the text after "=", else
// the next argument (for an optional integer, only when it is a number).
// "--opt=" gives a mandatory option no value at all.
func operand(spec Spec, value string, hasValue bool, args []string, next int) (val string, after int, msg string) {
	optional := spec.Kind == OptInt
	argMissing := prefix + spec.Names[0] + " requires an argument"

	switch {
	case optional && (hasValue || next >= len(args) || !optIntRE.MatchString(args[next])):
		return value, next, ""
	case optional:
		return args[next], next + 1, ""
	case hasValue && value == "":
		return "", next, argMissing
	case hasValue:
		return value, next, ""
	case next >= len(args):
		return "", next, argMissing
	}

	return args[next], next + 1, ""
}

// value reads the value of an option that takes one and records it.
func (r *Result) value(spec Spec, value string, hasValue bool, args []string, next int) (after int, msg string) {
	primary := spec.Names[0]

	value, next, msg = operand(spec, value, hasValue, args, next)
	if msg != "" {
		return next, msg
	}

	switch {
	case spec.Kind == String:
		r.Strs[primary] = value
	case spec.Kind == StringList:
		r.Lists[primary] = append(r.Lists[primary], value)
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

// candidate is an option a name may stand for; neg marks its negated spelling.
type candidate struct {
	spec Spec
	neg  bool
}

// optMatch is the option that name stands for: an exact name, else the only
// option one of whose names it abbreviates; otherwise the complaint. neg is
// set when the name is the negated spelling of a NegFlag.
func optMatch(name string, specs []Spec) (match Spec, neg bool, complaint string) {
	name = strings.ToLower(name)

	exact, found := candidates(name, specs)
	if exact != nil {
		return exact.spec, exact.neg, ""
	}

	switch len(found) {
	case 0:
		return Spec{}, false, "Unknown option: " + name
	case 1:
		return found[0].spec, found[0].neg, ""
	}

	names := make([]string, len(found))

	for i, c := range found {
		names[i] = c.spec.Names[0]
		if c.neg {
			names[i] = "no" + names[i]
		}
	}

	return Spec{}, false, prefix + name + " is ambiguous (" + strings.Join(names, ", ") + ")"
}

// candidates are the options name spells exactly (the first one) or
// abbreviates, each once.
func candidates(name string, specs []Spec) (exact *candidate, found []candidate) {
	for _, s := range specs {
		for _, sp := range spellings(s) {
			c := candidate{s, sp.neg}

			switch {
			case sp.name == name:
				return &c, nil
			case name != "" && strings.HasPrefix(sp.name, name):
				found = append(found, c)
			}
		}
	}

	return nil, slices.CompactFunc(found, func(a, b candidate) bool {
		return a.spec.Names[0] == b.spec.Names[0] && a.neg == b.neg
	})
}

// spelling is a name an option answers to; neg marks a negated one.
type spelling struct {
	name string
	neg  bool
}

// spellings are the names of an option: those it has and, for a NegFlag, the
// "no" and "no-" forms of each.
func spellings(s Spec) []spelling {
	var out []spelling

	for _, n := range s.Names {
		out = append(out, spelling{n, false})

		if s.Kind == NegFlag {
			out = append(out, spelling{"no" + n, true}, spelling{"no-" + n, true})
		}
	}

	return out
}
