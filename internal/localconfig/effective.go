// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"strings"

	"github.com/zextras/carbonio-configd/internal/intern"
)

// EnvPrefix prefixes the environment variables that overlay localconfig: the
// variable CONFIGD_LC_<key> sets the localconfig key <key>.
const EnvPrefix = "CONFIGD_LC_"

// EnvVar returns the name of the environment variable that overrides key.
func EnvVar(key string) string {
	return EnvPrefix + key
}

// EnvOverride returns the value the environment overlay gives to key, and
// whether the overlay sets it at all (an empty value counts as set).
func EnvOverride(key string) (string, bool) {
	return os.LookupEnv(EnvVar(key))
}

// EnvOverrides returns every key the environment overlay sets, with its value.
func EnvOverrides() map[string]string {
	overrides := make(map[string]string)

	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, EnvPrefix) || len(name) == len(EnvPrefix) {
			continue
		}

		overrides[intern.Key(name[len(EnvPrefix):])] = value
	}

	return overrides
}

// LoadEffective returns the effective localconfig of the default
// localconfig.xml: see LoadEffectiveFrom.
func LoadEffective() (map[string]string, error) {
	return LoadEffectiveFrom(DefaultConfigPath(), nil)
}

// LoadEffectiveFrom builds the effective localconfig from these layers, lowest
// precedence first:
//
//  1. the LC.java defaults;
//  2. the localconfig.xml at path (a missing file is an empty layer; an
//     unreadable or malformed one is an error);
//  3. the CONFIGD_LC_<key> environment variables;
//  4. overrides (per-command flags).
//
// ${key} references are then expanded once, over the merged map, so a value
// from any layer may refer to a key of any other layer.
func LoadEffectiveFrom(path string, overrides map[string]string) (map[string]string, error) {
	effective, err := LoadLocalConfigFromFile(path)

	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		effective = make(map[string]string, len(Defaults))
	default:
		return nil, err
	}

	MergeDefaults(effective)
	maps.Copy(effective, EnvOverrides())
	maps.Copy(effective, overrides)
	Interpolate(effective)

	return effective, nil
}
