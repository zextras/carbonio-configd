// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/provision"
	"github.com/zextras/carbonio-configd/internal/provision/provtest"
)

// provCall is one ModifyValues call.
type provCall = provtest.ModifyCall

// provFakeLDAP is the directory of RunProv in tests: the in-memory directory of
// the provisioning tests, which stores entries as the LDAP server would, with
// the global config and the default class of service a node has before
// bootstrap provisions it.
type provFakeLDAP struct {
	*provtest.Dir
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

// provTestSchema is the attribute schema the tests provision with: the
// fixture of the provisioning package.
func provTestSchema(t *testing.T) *provision.Schema {
	t.Helper()

	f, err := os.Open("../../provision/testdata/attrs.xml")
	require.NoError(t, err)

	defer func() { _ = f.Close() }()

	s, err := provision.ParseSchema(f)
	require.NoError(t, err)

	return s
}

// installProvFake makes RunProv on e use a new fake directory and the test
// schema.
func installProvFake(t *testing.T, e *Env) *provFakeLDAP {
	t.Helper()

	f := &provFakeLDAP{Dir: provtest.New()}
	f.Set(provGlobalDN, "objectClass", "zimbraGlobalConfig")
	f.Set("cn=default,cn=cos,cn=zimbra", "objectClass", "zimbraCOS")
	f.Set("cn=default,cn=cos,cn=zimbra", "zimbraId", "0f1e2d3c-0000-4000-8000-000000000001")

	f.addServer("mail.example.com")
	f.addServer("mta.example.com")
	f.addServer("ldap1.example.com")

	oldFake := provTestFake.Swap(f)
	e.provOpen = func(*Env) (provDirectory, error) {
		f.opened++

		if f.openErr != nil {
			return nil, f.openErr
		}

		return f, nil
	}

	provSchemas.Store(e, provTestSchema(t))

	t.Cleanup(func() {
		provTestFake.Store(oldFake)
		provSchemas.Delete(e)
	})

	return f
}

// Close counts the closes.
func (f *provFakeLDAP) Close() error {
	f.closed++

	return nil
}

// values is what the entry dn holds for attr.
func (f *provFakeLDAP) values(dn, attr string) []string { return f.Values(dn, attr) }

// set gives the entry dn the values of attr.
func (f *provFakeLDAP) set(dn, attr string, values ...string) { f.Set(dn, attr, values...) }

// addServer creates the server entry of host, as "cs" would.
func (f *provFakeLDAP) addServer(host string) {
	f.Set(provServerDN(host), "objectClass", "zimbraServer")
	f.Set(provServerDN(host), "zimbraServiceHostname", host)
}

// provGlobalDN is the global config of the fake directory.
const provGlobalDN = "cn=config,cn=zimbra"

// provServerDN is the entry of a server in the fake directory.
func provServerDN(host string) string { return "cn=" + host + ",cn=servers,cn=zimbra" }
