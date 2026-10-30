// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package transformer

import (
	"context"
	"testing"

	"github.com/zextras/carbonio-configd/internal/testutil"
)

// Legacy reference: carbonio-core 4.6.7 src/lib/jylibs/state.py (transform,
// xformLocalConfig, xformConfig).

func newParityTransformer() *Transformer {
	return NewTransformer(testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {
			"spam33":    "33",
			"spam75":    "75",
			"upd2h":     "2h",
			"upd30m":    "30m",
			"upd90s":    "90s",
			"upd1d":     "1d",
			"upd0h":     "0h",
			"upd3m":     "3m",
			"upd7":      "7",
			"rbls":      "a.example b.example",
			"services":  "mta cbpolicyd",
			"plainlist": "alpha beta",
		},
		"LOCAL": {
			"empty":    "",
			"blank":    "   ",
			"ldap_url": "ldap://h1.example:389 ldaps://h2.example:636",
			"plain":    "mailhost.example.com",
			"two":      "first second",
		},
	}), nil)
}

// state.py: `if line.count('@') < 2 and line.count('%') < 2: return line`.
// A lone '@' or '%' must leave the line byte-for-byte untouched: no trailing
// whitespace trim and no appended newline.
func TestTransform_GuardNeedsTwoMarkers(t *testing.T) {
	t.Parallel()

	tr := newParityTransformer()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"single at keeps trailing blanks", "user@example.com   ", "user@example.com   "},
		{"single at without newline", "root@host", "root@host"},
		{"single percent keeps trailing tab", "100% done\t", "100% done\t"},
		{"single at keeps CRLF", "a@b\r\n", "a@b\r\n"},
		{"two percents are processed", "50% and 60%   ", "50% and 60%\n"},
		{"two ats are processed", "a@b and c@d  \n", "a@b and c@d\n"},
		{"at and percent counted separately", "a@b 5%", "a@b 5%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := mustTransform(t, tr, context.Background(), tt.input); got != tt.want {
				t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Python's rstrip() also removes \v and \f.
func TestTransform_RstripFormFeedAndVerticalTab(t *testing.T) {
	t.Parallel()

	got := mustTransform(t, newParityTransformer(), context.Background(), "%%VAR:plainlist%% x\v\f \n")
	if want := "alpha beta x\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ' '.join((first, ' '.join(hosts))): the separating space stays even without URLs.
func TestXformLocalConfig_PerditionKeepsSeparatorSpace(t *testing.T) {
	t.Parallel()

	tr := newParityTransformer()

	tests := []struct {
		input string
		want  string
	}{
		{"[@@PERDITION_LDAP_SPLIT plain@@]", "[mailhost.example.com ]\n"},
		{"[@@PERDITION_LDAP_SPLIT ldap_url@@]", "[ldap://h1.example:389 h1.example h2.example]\n"},
	}

	for _, tt := range tests {
		if got := mustTransform(t, tr, context.Background(), tt.input); got != tt.want {
			t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// `%%range VAR:k lo hi%%` is ((val/100.00) * (hi-lo)) + lo and Jython renders it
// with str(float): 12 significant digits, ".0" for integral values. Rounding
// (%.0f) turned a 33% spam tag level of 6.6 into 7.
func TestRangeDirective_PythonFloatString(t *testing.T) {
	t.Parallel()

	tr := newParityTransformer()

	tests := []struct {
		input string
		want  string
	}{
		{"x = %%range VAR:spam33 0 20%%;", "x = 6.6;\n"},
		{"x = %%range VAR:spam75 0 20%%;", "x = 15.0;\n"},
		{"x = %%range VAR:spam33 5 20%%;", "x = 9.95;\n"},
		{"x = %%range VAR:spam33 0 1%%;", "x = 0.33;\n"},
	}

	for _, tt := range tests {
		if got := mustTransform(t, tr, context.Background(), tt.input); got != tt.want {
			t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPyStrFloat(t *testing.T) {
	t.Parallel()

	tests := map[float64]string{
		0:                    "0.0",
		50:                   "50.0",
		-5:                   "-5.0",
		6.6:                  "6.6",
		0.1 + 0.2:            "0.3",
		1e11:                 "100000000000.0",
		1e16:                 "1e+16",
		123456789012345678.0: "1.23456789012e+17",
		0.00001:              "1e-05",
	}

	for in, want := range tests {
		if got := pyStrFloat(in); got != want {
			t.Errorf("pyStrFloat(%v) = %q, want %q", in, got, want)
		}
	}
}

// freq: Python 2 integer division (floor) for m/s, product for d, total when
// the period is zero, and at least 1 when total > 1.
func TestFreqDirective_IntegerDivision(t *testing.T) {
	t.Parallel()

	tr := newParityTransformer()

	tests := []struct {
		key   string
		total string
		want  string
	}{
		{"upd2h", "24", "12"},
		{"upd30m", "24", "24"}, // 30/60 == 0 -> total
		{"upd90s", "24", "24"}, // 90/3600 == 0 -> total
		{"upd1d", "24", "1"},   // 1*24 -> 24/24
		{"upd0h", "24", "24"},
		{"upd3m", "24", "24"}, // 3/60 == 0 -> total
		{"upd7", "24", "3"},   // 24/7 floors to 3
		{"upd1d", "1", "0"},   // int(1/24) == 0 and the clamp needs total > 1
		{"upd1d", "2", "1"},   // int(2/24) == 0 -> clamped to 1
		{"upd7", "-3", "-1"},  // Python floors -3/7 to -1
	}

	for _, tt := range tests {
		in := "Checks %%freq VAR:" + tt.key + " " + tt.total + "%%"
		if got, want := mustTransform(t, tr, context.Background(), in), "Checks "+tt.want+"\n"; got != want {
			t.Errorf("Transform(%q) = %q, want %q", in, got, want)
		}
	}
}

// explode uses `explode (.*) ([^:]+):(\w+)`: exactly one space after the keyword
// is syntax, any further whitespace belongs to the base string.
func TestExplodeDirective_LegacyRegexBase(t *testing.T) {
	t.Parallel()

	tr := newParityTransformer()

	tests := []struct {
		input string
		want  string
	}{
		{"%%explode reject_rbl_client VAR:rbls%%", "reject_rbl_client a.example\nreject_rbl_client b.example\n"},
		{"%%explode   server VAR:rbls%%", "  server a.example\n  server b.example\n"},
	}

	for _, tt := range tests {
		if got := mustTransform(t, tr, context.Background(), tt.input); got != tt.want {
			t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// contains/exact strip replacement and altreplacement and fall back to the
// search string for an empty replacement (smtpd_recipient_restrictions.cf relies
// on "cbpolicyd^ check_policy_service inet:...").
func TestContainsExact_StripAndEmptyReplacement(t *testing.T) {
	t.Parallel()

	tr := newParityTransformer()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"replacement stripped", "%%contains VAR:services cbpolicyd^ check_policy_service inet:localhost:7780 %%", "check_policy_service inet:localhost:7780\n"},
		{"alt stripped", "%%contains VAR:services nope^yes^ no %%", "no\n"},
		{"empty replacement uses search", "%%contains VAR:services mta^^alt%%", "mta\n"},
		{"exact empty replacement uses search", "%%exact VAR:plainlist beta^^alt%%", "beta\n"},
		{"exact alt", "%%exact VAR:plainlist bet^x^ alt %%", "alt\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := mustTransform(t, tr, context.Background(), tt.input); got != tt.want {
				t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// binary/truefalse accept a "!" key like any lookUpConfig call (checkConditional).
func TestBinaryTrueFalse_NegatedKey(t *testing.T) {
	t.Parallel()

	tr := NewTransformer(testutil.NewMockConfigLookupWithData(map[string]map[string]string{
		"VAR": {"on": "TRUE", "off": "FALSE"},
	}), nil)

	tests := []struct {
		input string
		want  string
	}{
		{"a=%%binary VAR:!on%%;", "a=0;\n"},
		{"a=%%binary VAR:!off%%;", "a=1;\n"},
		{"a=%%binary VAR:!absent%%;", "a=1;\n"},
		{"a=%%truefalse VAR:!on%%;", "a=false;\n"},
		{"a=%%truefalse VAR:!off%%;", "a=true;\n"},
	}

	for _, tt := range tests {
		if got := mustTransform(t, tr, context.Background(), tt.input); got != tt.want {
			t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
