// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// provCall is one ModifyValues call.
type provCall struct {
	DN                string
	Add, Del, Replace map[string][]string
}

// provFakeLDAP is the directory of RunProv in tests: an in-memory store that
// applies ModifyValues as the real client does (add what is missing, delete
// what is there, replace) and records the calls.
type provFakeLDAP struct {
	base    string
	entries map[string]map[string][]string
	calls   []provCall
	// failDN makes ModifyValues of that entry fail with the error.
	failDN map[string]error
	// openErr makes provOpenLDAP fail.
	openErr error
	opened  int
	closed  int
}

// provTestFake is the fake of the latest testEnv. Parallel tests store it
// too, so it is atomic; only sequential tests read it (provTestLDAP).
var provTestFake atomic.Pointer[provFakeLDAP]

// provTestLDAP is the fake of the running (sequential) test.
func provTestLDAP() *provFakeLDAP { return provTestFake.Load() }

// installProvFake makes RunProv on e use a new fake directory.
func installProvFake(t *testing.T, e *Env) *provFakeLDAP {
	t.Helper()

	f := &provFakeLDAP{
		base:    "cn=zimbra",
		entries: map[string]map[string][]string{},
		failDN:  map[string]error{},
	}

	oldFake := provTestFake.Swap(f)
	e.provOpen = func(*Env) (provDirectory, error) {
		f.opened++

		if f.openErr != nil {
			return nil, f.openErr
		}

		return f, nil
	}

	t.Cleanup(func() { provTestFake.Store(oldFake) })

	return f
}

func (f *provFakeLDAP) BaseDN() string { return f.base }

func (f *provFakeLDAP) Close() error {
	f.closed++

	return nil
}

func (f *provFakeLDAP) ModifyValues(dn string, add, del, replace map[string][]string) error {
	f.calls = append(f.calls, provCall{dn, add, del, replace})

	if err := f.failDN[dn]; err != nil {
		return err
	}

	ent := f.entries[dn]
	if ent == nil {
		ent = map[string][]string{}
		f.entries[dn] = ent
	}

	for attr, values := range del {
		key := strings.ToLower(attr)
		if len(values) == 0 {
			delete(ent, key)

			continue
		}

		ent[key] = slices.DeleteFunc(slices.Clone(ent[key]), func(v string) bool { return slices.Contains(values, v) })
		if len(ent[key]) == 0 {
			delete(ent, key)
		}
	}

	for attr, values := range add {
		key := strings.ToLower(attr)

		for _, v := range values {
			if !slices.Contains(ent[key], v) {
				ent[key] = append(ent[key], v)
			}
		}
	}

	for attr, values := range replace {
		ent[strings.ToLower(attr)] = slices.Clone(values)
	}

	return nil
}

// values is what the entry dn holds for attr.
func (f *provFakeLDAP) values(dn, attr string) []string {
	return f.entries[dn][strings.ToLower(attr)]
}

// set gives the entry dn the values of attr.
func (f *provFakeLDAP) set(dn, attr string, values ...string) {
	if f.entries[dn] == nil {
		f.entries[dn] = map[string][]string{}
	}

	f.entries[dn][strings.ToLower(attr)] = values
}

// provGlobalDN is the global config of the fake directory.
const provGlobalDN = "cn=config,cn=zimbra"

// provServerDN is the entry of a server in the fake directory.
func provServerDN(host string) string { return "cn=" + host + ",cn=servers,cn=zimbra" }
