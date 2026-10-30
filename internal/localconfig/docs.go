// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"maps"
	"slices"
)

// lcdocs.go is generated from carbonio-mailbox (LC.java + ZsMsg.properties, tag 4.36.6);
// see scripts/gen-lcdocs.py for the inputs and flags.
//
//go:generate python3 ../../scripts/gen-lcdocs.py

// KeyDoc is the documentation of a known localconfig key, as printed by the
// legacy "zmlocalconfig -i".
type KeyDoc struct {
	// Doc is the free-text description (ZsMsg.properties entry of the key).
	Doc string
	// Supported mirrors LC.java @Supported: keys shown by "-i" without "--all".
	Supported bool
	// Reloadable mirrors LC.java @Reloadable: changes apply without a server restart.
	Reloadable bool
}

// LookupDoc returns the documentation of key. ok is false when the key has no
// documentation entry (which includes unknown keys).
func LookupDoc(key string) (doc KeyDoc, ok bool) {
	doc, ok = lcDocs[key]

	return doc, ok
}

// IsKnownKey reports whether key has a compiled-in default (legacy KnownKey).
func IsKnownKey(key string) bool {
	_, ok := lcDefaults[key]

	return ok
}

// KnownKeys returns every known key name in ascending order (legacy
// Arrays.sort(KnownKey.getAll())).
func KnownKeys() []string {
	return slices.Sorted(maps.Keys(lcDefaults))
}
