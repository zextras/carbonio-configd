// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import "maps"

// Defaults holds the default value for every known local-config key. It is the
// Go equivalent of the KnownKey registry in the Java LC class and the fallback
// consulted by MergeDefaults for keys absent from localconfig.xml.
//
// The contents come from lcDefaults (a verbatim port of LC.java) so that
// "configd localconfig" is a true drop-in for the retired LocalConfigCLI.
var Defaults = buildDefaults()

// buildDefaults materialises the registry. It is a copy rather than a direct
// alias of lcDefaults so callers observing Defaults cannot mutate the ported
// table, and so future intentional deviations have an obvious place to land.
func buildDefaults() map[string]string {
	d := make(map[string]string, len(lcDefaults))
	maps.Copy(d, lcDefaults)

	return d
}
