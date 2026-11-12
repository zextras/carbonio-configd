// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The behaviour of Getopt::Long's defaults (bundling off, abbreviations and
// ignore_case on, permute) that the ported scripts rely on.
func TestParseGetoptLong(t *testing.T) {
	specs := []optSpec{
		{names: []string{"h", "help"}},
		{names: []string{"set"}, kind: optFlag},
		{names: []string{"setmin"}, kind: optFlag},
		{names: []string{"section"}, kind: optString},
		{names: []string{"n", "num"}, kind: optInt},
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

		r := parseGetoptLong(c.args, specs, &errb)

		assert.Equal(t, c.good, r.allGood, name)
		assert.Equal(t, c.stderr, errb.String(), name)

		for _, g := range c.given {
			assert.True(t, r.has(g), "%s: %s", name, g)
		}

		for k, v := range c.strs {
			assert.Equal(t, v, r.strs[k], name)
		}

		for k, v := range c.ints {
			assert.Equal(t, v, r.ints[k], name)
		}

		assert.Equal(t, c.rest, r.rest, name)
	}
}
