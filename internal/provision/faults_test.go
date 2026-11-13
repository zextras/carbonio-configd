// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package provision_test

import (
	"errors"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/provision"
)

var errDirectoryDown = errors.New("directory down")

// flakyDir fails the call number failAt (1-based, any method) and counts the
// calls it receives, so a test can break the directory at each step of a command.
type flakyDir struct {
	provision.Directory

	failAt int
	calls  int
}

func (f *flakyDir) fail() error {
	f.calls++
	if f.calls == f.failAt {
		return errDirectoryDown
	}

	return nil
}

func (f *flakyDir) Search(base string, scope provision.Scope, filter string, attrs ...string) ([]provision.Entry, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}

	return f.Directory.Search(base, scope, filter, attrs...)
}

func (f *flakyDir) Add(dn string, attrs map[string][]string) error {
	if err := f.fail(); err != nil {
		return err
	}

	return f.Directory.Add(dn, attrs)
}

func (f *flakyDir) ModifyValues(dn string, add, del, replace map[string][]string) error {
	if err := f.fail(); err != nil {
		return err
	}

	return f.Directory.ModifyValues(dn, add, del, replace)
}

func (f *flakyDir) SetPassword(dn, password string) error {
	if err := f.fail(); err != nil {
		return err
	}

	return f.Directory.SetPassword(dn, password)
}

func (f *flakyDir) Delete(dn string) error {
	if err := f.fail(); err != nil {
		return err
	}

	return f.Directory.Delete(dn)
}

// provisionerOver is the provisioner of the rig working through dir.
func provisionerOver(t *testing.T, r *rig, dir provision.Directory) *provision.Provisioner {
	t.Helper()

	opts := r.Options
	opts.Dir = dir

	return provision.New(opts)
}

// refusingDir answers every modification with err.
type refusingDir struct {
	provision.Directory

	err error
}

func (d refusingDir) ModifyValues(string, map[string][]string, map[string][]string, map[string][]string) error {
	return d.err
}

// LdapProvisioning.modifyLdapAttrs maps the result codes of a refused
// modification the way LdapException.mapToLdapException does, and prefixes the
// message with its own text; zmprov printed "ERROR: account.INVALID_ATTR_NAME
// (invalid attr name: invalid attr name - unable to modify attributes: ...)".
func TestModify_ARefusedModificationIsMappedLikeJava(t *testing.T) {
	for name, tc := range map[string]struct {
		code    uint16
		wantErr string
	}{
		"undefined attribute type": {
			goldap.LDAPResultUndefinedAttributeType,
			provision.CodeInvalidAttrName + " (invalid attr name: invalid attr name - unable to modify attributes: zimbraNoSuchAttr: attribute type undefined)",
		},
		"constraint violation": {
			goldap.LDAPResultConstraintViolation,
			provision.CodeInvalidAttrValue + " (invalid attr value: invalid attr value - unable to modify attributes: zimbraNoSuchAttr: attribute type undefined)",
		},
		"invalid attribute syntax": {
			goldap.LDAPResultInvalidAttributeSyntax,
			provision.CodeInvalidAttrValue + " (invalid attr value: invalid attr value - unable to modify attributes: zimbraNoSuchAttr: attribute type undefined)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.withServer(t)

			cause := goldap.NewError(tc.code, errors.New("zimbraNoSuchAttr: attribute type undefined"))
			r.Provisioner = provisionerOver(t, r, refusingDir{Directory: r.dir, err: cause})

			_, err := r.Exec([]string{"ms", host, "zimbraNoSuchAttr", "x"})

			require.Error(t, err)
			assert.Equal(t, tc.wantErr, err.Error())
		})
	}

	t.Run("any other failure is service.FAILURE", func(t *testing.T) {
		r := newRig(t)
		r.withServer(t)

		cause := goldap.NewError(goldap.LDAPResultInsufficientAccessRights, errors.New("no access"))
		r.Provisioner = provisionerOver(t, r, refusingDir{Directory: r.dir, err: cause})

		_, err := r.Exec([]string{"ms", host, "zimbraMailPort", "8080"})

		assert.True(t, hasCode(err, provision.CodeFailure), "%v", err)
		assert.ErrorIs(t, err, cause)
	})
}

// A directory that fails at any step of a command is reported by the command:
// no failure is swallowed into a half-done success.
func TestExec_ADirectoryFailureAtAnyStepIsReported(t *testing.T) {
	// A scenario prepares the directory and returns the command to break.
	scenarios := map[string]func(t *testing.T, r *rig) []string{
		"create server": func(*testing.T, *rig) []string { return []string{"cs", "new.example.com"} },
		"create service server": func(*testing.T, *rig) []string {
			return []string{"cs", "new.example.com", "zimbraServiceEnabled", "service", "zimbraMailPort", "8081"}
		},
		"modify server ports": func(t *testing.T, r *rig) []string {
			r.withServer(t)

			return []string{"ms", host, "zimbraMailPort", "8080", "zimbraMailSSLPort", "8443"}
		},
		"modify server bind": func(t *testing.T, r *rig) []string {
			r.withServer(t)

			return []string{"ms", host, "zimbraMailMode", "http", "zimbraMailBindAddress", "10.0.0.5",
				"zimbraAdminBindAddress", "10.0.0.6"}
		},
		"modify global config": func(t *testing.T, r *rig) []string {
			r.withServer(t)

			return []string{"mcf", "zimbraMailMode", "https", "zimbraPop3BindPort", "7110"}
		},
		"modify cos pool": func(t *testing.T, r *rig) []string {
			return []string{"mc", "default", "zimbraMailHostPool", r.withServer(t)}
		},
		"create domain": func(*testing.T, *rig) []string {
			return []string{"cd", "example.com", "zimbraDomainType", "alias"}
		},
		"create account": func(t *testing.T, r *rig) []string {
			r.withServer(t)
			r.withDomain(t)

			return []string{"ca", "u@example.com", "secret-pass", "zimbraMailHost", host}
		},
		"create account with a transport": func(t *testing.T, r *rig) []string {
			r.withServer(t)
			r.withDomain(t)

			return []string{"ca", "u@example.com", "", "zimbraMailTransport", "lmtp:elsewhere:7025"}
		},
		"add alias": func(t *testing.T, r *rig) []string {
			r.withServer(t)
			r.withDomain(t)
			r.run(t, "ca", "u@example.com", "")

			return []string{"aaa", "u@example.com", "alias@example.com"}
		},
	}

	for name, prepare := range scenarios {
		t.Run(name, func(t *testing.T) {
			build := func(failAt int) (*rig, *flakyDir, []string) {
				r := newRig(t)
				args := prepare(t, r)

				flaky := &flakyDir{Directory: r.dir, failAt: failAt}
				r.Provisioner = provisionerOver(t, r, flaky)

				return r, flaky, args
			}

			r, probe, args := build(0)
			_, err := r.Exec(args)
			require.NoError(t, err, "the command works on a healthy directory")
			require.Positive(t, probe.calls)

			for n := 1; n <= probe.calls; n++ {
				r, _, args := build(n)
				warned := len(r.warns)

				_, err := r.Exec(args)

				assert.True(t, errors.Is(err, errDirectoryDown) || len(r.warns) > warned,
					"call %d of %d failed and the command reported nothing: %v", n, probe.calls, err)
			}
		})
	}
}
