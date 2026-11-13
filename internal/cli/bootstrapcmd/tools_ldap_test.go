// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bootstrapcmd

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/bootstrap/setup"
	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

type ldapToolRun func(context.Context, sys.System, io.Writer, io.Writer, []string) int

type ldapToolCase struct {
	name string
	run  ldapToolRun
	// rootOnly marks the scripts that refused every user but root.
	rootOnly bool
	// zextrasOnly marks the one that refused every user but zextras.
	zextrasOnly bool
}

// ldapToolCases are the tools that run a setup port with the Env of toolEnv:
// the ports are replaced, until the test ends, by ones that record their call.
func ldapToolCases(t *testing.T, h *toolHarness) []ldapToolCase {
	t.Helper()

	script := func(name string) func(*setup.Env, []string, io.Writer, io.Writer) int {
		return func(e *setup.Env, args []string, stdout, stderr io.Writer) int {
			h.calls, h.env, h.args = append(h.calls, name), e, args

			_, _ = io.WriteString(stdout, name+" out\n")
			_, _ = io.WriteString(stderr, name+" err\n")

			return h.code
		}
	}

	oldS, oldU, oldR, oldM, oldI := runLDAPSchema, runLDAPUpdateLDIF, runLDAPEnableReplica, runLDAPEnableMMR, runIPTool

	t.Cleanup(func() {
		runLDAPSchema, runLDAPUpdateLDIF, runLDAPEnableReplica, runLDAPEnableMMR, runIPTool = oldS, oldU, oldR, oldM, oldI
	})

	runLDAPSchema, runLDAPUpdateLDIF, runLDAPEnableReplica = script("schema"), script("update"), script("replica")
	runLDAPEnableMMR, runIPTool = script("mmr"), script("iptool")

	return []ldapToolCase{
		{"ldap-schema", RunLDAPSchema, false, true},
		{"ldap-update-ldif", RunLDAPUpdateLDIF, true, false},
		{"ldap-enable-replica", RunLDAPEnableReplica, true, false},
		{"ldap-enable-mmr", RunLDAPEnableMMR, false, false},
		{"iptool", RunIPTool, false, false},
	}
}

func TestLDAPTools_PassTheOptionsAndTheScriptsStatus(t *testing.T) {
	for _, root := range []bool{true, false} {
		h := newToolHarness(t, root)
		h.code = 3
		zextrasUID = func() int { return 1000 }

		for _, c := range ldapToolCases(t, h) {
			if (c.rootOnly && !root) || (c.zextrasOnly && root) {
				continue
			}

			h.calls = nil

			var out, errb bytes.Buffer

			status := c.run(context.Background(), systest.New().System(), &out, &errb, []string{"-x", "y"})

			assert.Equal(t, 3, status, "%s: the status of the port, root %v", c.name, root)
			require.Len(t, h.calls, 1, c.name)
			assert.Equal(t, []string{"-x", "y"}, h.args, c.name)
			assert.Equal(t, h.calls[0]+" out\n", out.String(), c.name)
			assert.Equal(t, h.calls[0]+" err\n", errb.String(), c.name)
			assert.Nil(t, h.env.Cfg, c.name)
			assert.Equal(t, !root, h.env.IsZextras, c.name)
		}
	}
}

func TestLDAPTools_RefuseOtherUsers(t *testing.T) {
	h := newToolHarness(t, false)

	for _, c := range ldapToolCases(t, h) {
		var out, errb bytes.Buffer

		status := c.run(context.Background(), systest.New().System(), &out, &errb, nil)

		want := "Error: carbonio-bootstrap " + c.name + " must run as root or zextras\n"
		if c.rootOnly {
			want = "Error: must be run as root user\n"
		}

		if c.zextrasOnly {
			// zmldapschema said it with echo, so on stdout.
			assert.Equal(t, "Error: must be run as zextras user\n", out.String(), c.name)
			assert.Empty(t, errb.String(), c.name)
			assert.Equal(t, 1, status, c.name)

			continue
		}

		assert.Equal(t, 1, status, c.name)
		assert.Equal(t, want, errb.String(), c.name)
		assert.Empty(t, out.String(), c.name)
	}

	assert.Empty(t, h.calls)
}

// zmldapschema refused root as well as everybody else: the port ran for root
// too, and left root-owned files in the slapd configuration.
func TestRunLDAPSchema_RefusesRoot(t *testing.T) {
	h := newToolHarness(t, true)

	for _, c := range ldapToolCases(t, h) {
		if !c.zextrasOnly {
			continue
		}

		var out, errb bytes.Buffer

		status := c.run(context.Background(), systest.New().System(), &out, &errb, nil)

		assert.Equal(t, 1, status)
		assert.Equal(t, "Error: must be run as zextras user\n", out.String())
		assert.Empty(t, errb.String())
	}

	assert.Empty(t, h.calls)
}

// zminiutil checked no user: it runs for anybody, on the file system of the
// system it is given.
func TestRunIniUtil_RunsForAnyUser(t *testing.T) {
	h := newToolHarness(t, false)
	zextrasUID = func() int { return 999 }

	require.Empty(t, h.calls)
	fakes := systest.New()
	fakes.FS.Put("/etc/my.cnf", []byte("[mysqld]\nbind-address = ::1\n"), 0o644)

	var out, errb bytes.Buffer

	status := RunIniUtil(context.Background(), fakes.System(), &out, &errb,
		[]string{"--get", "--section=mysqld", "--key=bind-address", "/etc/my.cnf"})

	assert.Equal(t, 0, status)
	assert.Equal(t, "::1\n", out.String())
	assert.Empty(t, errb.String())
}
