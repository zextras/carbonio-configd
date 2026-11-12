// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package getopt

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The behaviour of Getopt::Long's defaults (bundling off, abbreviations and
// ignore_case on, permute) that the ported scripts rely on.
func TestParse(t *testing.T) {
	specs := []Spec{
		{Names: []string{"h", "help"}},
		{Names: []string{"set"}, Kind: Flag},
		{Names: []string{"setmin"}, Kind: Flag},
		{Names: []string{"section"}, Kind: String},
		{Names: []string{"n", "num"}, Kind: Int},
	}

	cases := map[string]struct {
		args   []string
		good   bool
		given  []string
		strs   map[string]string
		ints   map[string]int
		rest   []string
		stderr string
	}{
		"short and long":     {args: []string{"-h", "--section", "a", "f"}, good: true, given: []string{"h", "section"}, strs: map[string]string{"section": "a"}, rest: []string{"f"}},
		"equals form":        {args: []string{"--section=a=b"}, good: true, given: []string{"section"}, strs: map[string]string{"section": "a=b"}},
		"single dash long":   {args: []string{"-section=a"}, good: true, given: []string{"section"}, strs: map[string]string{"section": "a"}},
		"alias":              {args: []string{"--num", "5"}, good: true, given: []string{"n"}, ints: map[string]int{"n": 5}},
		"case insensitive":   {args: []string{"--HELP"}, good: true, given: []string{"h"}},
		"abbreviation":       {args: []string{"--sec=x"}, good: true, given: []string{"section"}, strs: map[string]string{"section": "x"}},
		"exact beats prefix": {args: []string{"--set"}, good: true, given: []string{"set"}},
		"options after args": {args: []string{"f", "-h", "g"}, good: true, given: []string{"h"}, rest: []string{"f", "g"}},
		"double dash":        {args: []string{"--", "-h"}, good: true, rest: []string{"-h"}},
		"negative number":    {args: []string{"-n", "-3"}, good: true, given: []string{"n"}, ints: map[string]int{"n": -3}},
		"unknown":            {args: []string{"--zap"}, stderr: "Unknown option: zap\n"},
		"ambiguous":          {args: []string{"--se"}, stderr: "Option se is ambiguous (set, setmin, section)\n"},
		"missing value":      {args: []string{"--section"}, stderr: "Option section requires an argument\n"},
		"flag with value":    {args: []string{"--help=1"}, stderr: "Option h does not take an argument\n"},
		"not a number":       {args: []string{"-n", "x"}, stderr: "Value \"x\" invalid for option n (number expected)\n"},
	}

	for name, c := range cases {
		var errb bytes.Buffer

		r := Parse(c.args, specs, &errb)

		assert.Equal(t, c.good, r.AllGood, name)
		assert.Equal(t, c.stderr, errb.String(), name)

		for _, g := range c.given {
			assert.True(t, r.Has(g), "%s: %s", name, g)
		}

		for k, v := range c.strs {
			assert.Equal(t, v, r.Strs[k], name)
		}

		for k, v := range c.ints {
			assert.Equal(t, v, r.Ints[k], name)
		}

		assert.Equal(t, c.rest, r.Rest, name)
	}
}

// The optional integer of "new:i" and "debug:1" (dhparam): the next argument
// is its value only when it is a number.
func TestParse_OptionalInteger(t *testing.T) {
	specs := []Spec{{Names: []string{"new"}, Kind: OptInt}, {Names: []string{"in"}, Kind: String}}

	cases := map[string]struct {
		args  []string
		given bool
		bare  bool
		n     int
		rest  []string
		err   string
	}{
		"bare":                {args: []string{"-new"}, given: true, bare: true},
		"number follows":      {args: []string{"-new", "4096"}, given: true, n: 4096},
		"text follows":        {args: []string{"-new", "x"}, given: true, bare: true, rest: []string{"x"}},
		"equals":              {args: []string{"-new=2048"}, given: true, n: 2048},
		"equals empty":        {args: []string{"-new="}, given: true, bare: true},
		"negative":            {args: []string{"-new", "-1"}, given: true, n: -1},
		"equals not a number": {args: []string{"-new=big"}, err: "Value \"big\" invalid for option new (number expected)\n"},
	}

	for name, c := range cases {
		var errb bytes.Buffer

		r := Parse(c.args, specs, &errb)

		assert.Equal(t, c.given, r.Has("new"), name)
		assert.Equal(t, c.bare, r.Bare["new"], name)
		assert.Equal(t, c.n, r.Ints["new"], name)
		assert.Equal(t, c.rest, r.Rest, name)
		assert.Equal(t, c.err, errb.String(), name)
	}
}
