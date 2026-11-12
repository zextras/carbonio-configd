// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package stat

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

var testDefs = []optDef{
	{"interval", kindInt}, {"log", kindString}, {"console", kindBool},
	{"max-qsize", kindInt}, {"max-interval", kindInt},
}

// Getopt::Long::GetOptions with the default configuration, as every zmstat
// script calls it: unique-prefix abbreviations, any case, -name=value, options
// mixed with arguments, "--" ending the options, and its warning texts.
func TestParseGetopt(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		args     []string
		ok       bool
		ints     map[string]int
		strs     map[string]string
		console  bool
		rest     []string
		warnings string
	}{
		"short name, value in next argument": {args: []string{"-i", "10"}, ok: true, ints: map[string]int{"interval": 10}},
		"long name with =":                   {args: []string{"--interval=5"}, ok: true, ints: map[string]int{"interval": 5}},
		"unique prefix":                      {args: []string{"--int", "7"}, ok: true, ints: map[string]int{"interval": 7}},
		"any case":                           {args: []string{"--INTERVAL", "7"}, ok: true, ints: map[string]int{"interval": 7}},
		"negative number":                    {args: []string{"-i", "-3"}, ok: true, ints: map[string]int{"interval": -3}},
		"string and flag": {args: []string{"-l", "/tmp/x.csv", "-c"}, ok: true,
			strs: map[string]string{"log": "/tmp/x.csv"}, console: true},
		"arguments are kept in order": {args: []string{"mailbox", "-c", "ldap"}, ok: true, console: true,
			rest: []string{"mailbox", "ldap"}},
		"-- ends the options":        {args: []string{"--", "-c"}, ok: true, rest: []string{"-c"}},
		"a lone dash is an argument": {args: []string{"-"}, ok: true, rest: []string{"-"}},
		"missing value":              {args: []string{"-i"}, warnings: "Option interval requires an argument\n"},
		"not a number": {args: []string{"-i", "abc"}, ints: nil,
			warnings: "Value \"abc\" invalid for option interval (number expected)\n"},
		"unknown":                  {args: []string{"--zzz"}, warnings: "Unknown option: zzz\n"},
		"bundled value is unknown": {args: []string{"-i5"}, warnings: "Unknown option: i5\n"},
		"ambiguous": {args: []string{"--max", "1"}, rest: []string{"1"},
			warnings: "Option max is ambiguous (max-interval, max-qsize)\n"},
		"flag with a value": {args: []string{"--console=1"}, warnings: "Option console does not take an argument\n"},
	} {
		var warn bytes.Buffer

		po, ok := parseGetopt(testDefs, tc.args, &warn)

		assert.Equal(t, tc.ok, ok, name)
		assert.Equal(t, tc.warnings, warn.String(), name)

		if !tc.ok {
			continue
		}

		assert.Equal(t, tc.console, po.bools["console"], name)
		assert.Equal(t, tc.rest, po.rest, name)

		if tc.ints != nil {
			assert.Equal(t, tc.ints, po.ints, name)
		}

		if tc.strs != nil {
			assert.Equal(t, tc.strs, po.strs, name)
		}
	}
}
