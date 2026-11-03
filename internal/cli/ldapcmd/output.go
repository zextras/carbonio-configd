// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package ldapcmd

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	carboldap "github.com/zextras/carbonio-configd/internal/ldap"
)

// pick keeps the attributes named in only (case-insensitively); an empty only
// keeps all.
func pick(attrs map[string]string, only []string) map[string]string {
	if len(only) == 0 {
		return attrs
	}

	picked := make(map[string]string, len(only))

	for _, want := range only {
		for name, value := range attrs {
			if strings.EqualFold(name, want) {
				picked[name] = value
			}
		}
	}

	return picked
}

// sortedNames returns the attribute names in the order zmprov lists them:
// case-insensitively.
func sortedNames(attrs map[string]string) []string {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}

	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a), strings.ToLower(b)), cmp.Compare(a, b))
	})

	return names
}

// printAttrs writes attrs, which hold normalized values (a multi-valued
// attribute's values joined with newlines), as zmprov does: one "attr: value"
// line per value; or as a JSON object of value lists.
func printAttrs(w io.Writer, attrs map[string]string, asJSON bool) error {
	if asJSON {
		return writeJSON(w, listValues(attrs))
	}

	for _, name := range sortedNames(attrs) {
		for value := range strings.SplitSeq(attrs[name], "\n") {
			if _, err := fmt.Fprintf(w, "%s: %s\n", name, value); err != nil {
				return err
			}
		}
	}

	return nil
}

// listValues turns normalized attrs into attribute -> values.
func listValues(attrs map[string]string) map[string][]string {
	values := make(map[string][]string, len(attrs))
	for name, joined := range attrs {
		values[name] = strings.Split(joined, "\n")
	}

	return values
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(v)
}

// searchEntry is one search result in JSON form.
type searchEntry struct {
	DN         string              `json:"dn"`
	Attributes map[string][]string `json:"attributes"`
}

// printEntries writes search results: per entry its DN, then the attributes as
// "attr: value" lines, entries separated by a blank line; or a JSON list.
func printEntries(w io.Writer, entries []*goldap.Entry, asJSON bool) error {
	normalized := make([]searchEntry, 0, len(entries))

	for _, entry := range entries {
		attrs := make(map[string]string, len(entry.Attributes))
		for _, a := range entry.Attributes {
			attrs[a.Name] = strings.Join(a.Values, "\n")
		}

		normalized = append(normalized, searchEntry{
			DN:         entry.DN,
			Attributes: listValues(carboldap.NormalizeAttrs(attrs)),
		})
	}

	if asJSON {
		return writeJSON(w, normalized)
	}

	for i, entry := range normalized {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}

		if err := printEntry(w, entry); err != nil {
			return err
		}
	}

	return nil
}

// printEntry writes one search result as its DN line and "attr: value" lines.
func printEntry(w io.Writer, entry searchEntry) error {
	if _, err := fmt.Fprintf(w, "dn: %s\n", entry.DN); err != nil {
		return err
	}

	flat := make(map[string]string, len(entry.Attributes))
	for name, values := range entry.Attributes {
		flat[name] = strings.Join(values, "\n")
	}

	return printAttrs(w, flat, false)
}
