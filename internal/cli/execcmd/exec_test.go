// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package execcmd

import (
	"bytes"
	"errors"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpandArgs(t *testing.T) {
	t.Parallel()

	vars := map[string]string{"java_xms": "512", "java_options": "-a -b  -c", "empty": "", "dir": "/x y"}

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"in-word braces", []string{"-Xms${java_xms}m"}, []string{"-Xms512m"}},
		{"braces keep spaces", []string{"${dir}/f"}, []string{"/x y/f"}},
		{"braces empty keeps word", []string{"${empty}"}, []string{""}},
		{"braces empty inside", []string{"a${empty}b"}, []string{"ab"}},
		{"whole word splits", []string{"$java_options"}, []string{"-a", "-b", "-c"}},
		{"whole word empty removed", []string{"x", "$empty", "y"}, []string{"x", "y"}},
		{"whole word unknown removed", []string{"$nope"}, []string{}},
		{"braces unknown empty", []string{"p${nope}q"}, []string{"pq"}},
		{"dollar dollar", []string{"$$HOME", "a$$b", "$${java_xms}"}, []string{"$HOME", "a$b", "${java_xms}"}},
		{"name not whole word stays", []string{"-X$java_xms"}, []string{"-X$java_xms"}},
		{"adjacent", []string{"${java_xms}${java_xms}-${dir}"}, []string{"512512-/x y"}},
		{"malformed braces", []string{"${a", "${}", "${a-b}", "$"}, []string{"${a", "${}", "${a-b}", "$"}},
		{"trailing dollar", []string{"a$"}, []string{"a$"}},
		{"plain", []string{"-c", "echo hi"}, []string{"-c", "echo hi"}},
		{"none", nil, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ExpandArgs(tt.args, vars))
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	load := func() (map[string]string, error) {
		return map[string]string{"mailboxd_java_heap_size": "1024", "zimbra_zmjava_options": "-x -y"}, nil
	}

	type call struct {
		path      string
		argv, env []string
	}

	newOpts := func(args []string, errOut *bytes.Buffer, got *call, execErr error) *Options {
		return &Options{
			Args:     args,
			Stderr:   errOut,
			Load:     load,
			Environ:  func() []string { return []string{"PATH=/bin", "java_xms=1", "KEEP=me"} },
			LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
			Exec: func(p string, a, e []string) error {
				*got = call{p, a, e}

				return execErr
			},
		}
	}

	t.Run("expands and overrides env", func(t *testing.T) {
		t.Parallel()

		var got call

		var errOut bytes.Buffer

		rc := Run(newOpts([]string{"/bin/echo", "-Xms${java_xms}m", "$java_options", "$$x"}, &errOut, &got, nil))
		assert.Equal(t, 0, rc)
		assert.Equal(t, "/bin/echo", got.path)
		assert.Equal(t, []string{"/bin/echo", "-Xms1024m", "-x", "-y", "$x"}, got.argv)
		assert.Contains(t, got.env, "KEEP=me")
		assert.Contains(t, got.env, "java_xms=1024")
		assert.Contains(t, got.env, "java_xmx=1024")
		assert.NotContains(t, got.env, "java_xms=1")
		assert.Empty(t, errOut.String())
	})

	t.Run("PATH lookup", func(t *testing.T) {
		t.Parallel()

		var got call

		Run(newOpts([]string{"true"}, &bytes.Buffer{}, &got, nil))
		assert.Equal(t, "/usr/bin/true", got.path)
		assert.Equal(t, []string{"true"}, got.argv)
	})

	t.Run("load failure", func(t *testing.T) {
		t.Parallel()

		var got call

		var errOut bytes.Buffer

		o := newOpts([]string{"/bin/true"}, &errOut, &got, nil)
		o.Load = func() (map[string]string, error) { return nil, errors.New("boom") }
		assert.Equal(t, 1, Run(o))
		assert.Contains(t, errOut.String(), "boom")
		assert.Empty(t, got.path)
	})

	t.Run("no program", func(t *testing.T) {
		t.Parallel()

		var got call

		assert.Equal(t, 1, Run(newOpts(nil, &bytes.Buffer{}, &got, nil)))
	})

	t.Run("exec not found", func(t *testing.T) {
		t.Parallel()

		var got call

		var errOut bytes.Buffer

		assert.Equal(t, 127, Run(newOpts([]string{"/nope"}, &errOut, &got, syscall.ENOENT)))
		assert.Contains(t, errOut.String(), "/nope")
	})

	t.Run("exec not executable", func(t *testing.T) {
		t.Parallel()

		var got call

		assert.Equal(t, 126, Run(newOpts([]string{"/etc/passwd"}, &bytes.Buffer{}, &got, syscall.EACCES)))
	})

	t.Run("lookpath not found", func(t *testing.T) {
		t.Parallel()

		var got call

		o := newOpts([]string{"nope"}, &bytes.Buffer{}, &got, nil)
		o.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
		assert.Equal(t, 127, Run(o))
		assert.Empty(t, got.path)
	})
}
