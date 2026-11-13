// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision_test

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/provision"
	"github.com/zextras/carbonio-configd/internal/provision/provtest"
)

const (
	host      = "mail.example.com"
	globalDN  = "cn=config,cn=zimbra"
	cosDN     = "cn=default,cn=cos,cn=zimbra"
	serverDN  = "cn=mail.example.com,cn=servers,cn=zimbra"
	domainDN  = "dc=example,dc=com"
	adminAddr = "admin@example.com"
	adminDN   = "uid=admin,ou=people,dc=example,dc=com"
	adminPass = "Sup3r-s3cret"
)

// uuidPattern is a zimbraId.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// hasCode reports whether err is a provisioning error with the code.
func hasCode(err error, code string) bool {
	var pe *provision.Error

	return errors.As(err, &pe) && pe.Code == code
}

// osFS reads the schema fixture.
type osFS struct{}

func (osFS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (osFS) ReadFile(name string) ([]byte, error)  { return os.ReadFile(name) }

// rig is a provisioner over an in-memory directory that holds the global config
// and the default class of service, as a node does before bootstrap provisions it.
type rig struct {
	*provision.Provisioner

	dir   *provtest.Dir
	lc    map[string]string
	warns []string
	files map[string][]byte
	ips   map[string][]net.IP
	pick  int
}

// at is the instant the tests create entries.
var at = time.Date(2026, 11, 12, 22, 36, 16, 47_000_000, time.UTC)

func schema(t *testing.T) *provision.Schema {
	t.Helper()

	s, err := provision.LoadSchema(osFS{}, "testdata")
	require.NoError(t, err)

	return s
}

func newRig(t *testing.T) *rig {
	t.Helper()

	r := &rig{dir: provtest.New(), lc: map[string]string{}, files: map[string][]byte{}, ips: map[string][]net.IP{}}

	r.dir.Set(globalDN, "objectClass", "zimbraGlobalConfig")
	r.dir.Set(globalDN, "zimbraAccountExtraObjectClass", "amavisAccount")
	r.dir.Set(globalDN, "zimbraDomainExtraObjectClass", "amavisAccount")
	r.dir.Set(globalDN, "zimbraLdapGentimeFractionalSecondsEnabled", "TRUE")
	r.dir.Set(cosDN, "objectClass", "zimbraCOS")
	r.dir.Set(cosDN, "zimbraId", "0f1e2d3c-0000-4000-8000-000000000001")
	r.dir.Set(cosDN, "cn", "default")

	r.Provisioner = provision.New(provision.Options{
		Dir:           r.dir,
		Schema:        schema(t),
		LocalHostname: host,
		ReadFile: func(p string) ([]byte, error) {
			if b, ok := r.files[p]; ok {
				return b, nil
			}

			return nil, os.ErrNotExist
		},
		SetLocalConfig: func(k, v string) error { r.lc[k] = v; return nil },
		Warn:           func(format string, args ...any) { r.warns = append(r.warns, format) },
		Now:            func() time.Time { return at },
		Resolve: func(h string) ([]net.IP, error) {
			if ips, ok := r.ips[h]; ok {
				return ips, nil
			}

			return nil, &net.DNSError{Err: "no such host", Name: h}
		},
		Pick: func(n int) int { return r.pick % n },
	})

	return r
}

// run executes the command and fails the test when it fails.
func (r *rig) run(t *testing.T, args ...string) string {
	t.Helper()

	id, err := r.Exec(args)
	require.NoError(t, err, "%v", args)

	return id
}

// withServer creates the local server, which runs the mail client service.
func (r *rig) withServer(t *testing.T) string {
	t.Helper()

	id := r.run(t, "cs", host)
	r.run(t, "ms", host, "+zimbraServiceEnabled", "service")

	return id
}

// withDomain creates example.com and its admin account.
func (r *rig) withDomain(t *testing.T) {
	t.Helper()

	r.run(t, "cd", "example.com")
}
