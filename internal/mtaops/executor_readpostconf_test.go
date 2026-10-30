// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package mtaops

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zextras/carbonio-configd/internal/sys"
	"github.com/zextras/carbonio-configd/internal/sys/systest"
)

func TestExecutor_ReadPostconf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		key          string
		explicitOnly bool
		handler      systest.Handler
		wantArgs     []string
		wantValue    string
		wantSet      bool
	}{
		{
			name:      "effective value is trimmed and always set",
			key:       "myhostname",
			handler:   systest.Reply("mail.example.com\n"),
			wantArgs:  []string{"-h", "myhostname"},
			wantValue: "mail.example.com",
			wantSet:   true,
		},
		{
			name:      "effective empty default is still set",
			key:       "relay_host",
			handler:   systest.Reply("\n"),
			wantArgs:  []string{"-h", "relay_host"},
			wantValue: "",
			wantSet:   true,
		},
		{
			name:      "effective wrapped value is unwrapped",
			key:       "mynetworks",
			handler:   systest.Reply("127.0.0.0/8, 10.0.0.0/8,\n\t192.168.0.0/16\n"),
			wantArgs:  []string{"-h", "mynetworks"},
			wantValue: "127.0.0.0/8, 10.0.0.0/8, 192.168.0.0/16",
			wantSet:   true,
		},
		{
			name:         "explicit value",
			key:          "myhostname",
			explicitOnly: true,
			handler:      systest.Reply("myhostname = mail.example.com\n"),
			wantArgs:     []string{"-n", "myhostname"},
			wantValue:    "mail.example.com",
			wantSet:      true,
		},
		{
			name:         "explicit key absent from main.cf is unset",
			key:          "relay_host",
			explicitOnly: true,
			handler:      systest.Reply(""),
			wantArgs:     []string{"-n", "relay_host"},
			wantValue:    "",
			wantSet:      false,
		},
		{
			name:         "explicit empty value is set",
			key:          "relay_host",
			explicitOnly: true,
			handler:      systest.Reply("relay_host = \n"),
			wantArgs:     []string{"-n", "relay_host"},
			wantValue:    "",
			wantSet:      true,
		},
		{
			name:         "explicit empty value without trailing space is set",
			key:          "relay_host",
			explicitOnly: true,
			handler:      systest.Reply("relay_host =\n"),
			wantArgs:     []string{"-n", "relay_host"},
			wantValue:    "",
			wantSet:      true,
		},
		{
			name:         "explicit wrapped value is unwrapped",
			key:          "mynetworks",
			explicitOnly: true,
			handler:      systest.Reply("mynetworks = 127.0.0.0/8, 10.0.0.0/8,\n\t192.168.0.0/16\n"),
			wantArgs:     []string{"-n", "mynetworks"},
			wantValue:    "127.0.0.0/8, 10.0.0.0/8, 192.168.0.0/16",
			wantSet:      true,
		},
		{
			name:         "a longer key sharing the prefix is not the key",
			key:          "mynetworks",
			explicitOnly: true,
			handler:      systest.Reply("mynetworks_style = host\n"),
			wantArgs:     []string{"-n", "mynetworks"},
			wantValue:    "",
			wantSet:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ex, fakes := newFakeExecutor(&mockLdapManager{})
			fakes.Exec.OnPath(postconfBin, tt.handler)

			value, set, err := ex.ReadPostconf(t.Context(), tt.key, tt.explicitOnly)

			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantSet, set)

			calls := fakes.Exec.Calls()
			require.Len(t, calls, 1)
			assert.Equal(t, postconfBin, calls[0].Path)
			assert.Equal(t, tt.wantArgs, calls[0].Args)
			assert.False(t, calls[0].Combined, "stderr warnings must not leak into the value")
		})
	}
}

func TestExecutor_ReadPostconf_ExecErrors(t *testing.T) {
	t.Parallel()

	spawnErr := os.ErrNotExist

	tests := []struct {
		name         string
		explicitOnly bool
		arrange      func(*systest.Fakes)
		wantErrIs    error
	}{
		{
			name: "effective: non-zero exit",
			arrange: func(f *systest.Fakes) {
				f.Exec.OnPath(postconfBin, systest.Fail(1, "postconf: warning: unknown parameter\n"))
			},
		},
		{
			name:         "explicit: non-zero exit",
			explicitOnly: true,
			arrange: func(f *systest.Fakes) {
				f.Exec.OnPath(postconfBin, systest.Fail(1, "postconf: warning: unknown parameter\n"))
			},
		},
		{
			name: "effective: binary cannot be spawned",
			arrange: func(f *systest.Fakes) {
				f.Exec.FailStart(func(*sys.Cmd) bool { return true }, spawnErr)
			},
			wantErrIs: spawnErr,
		},
		{
			name:         "explicit: binary cannot be spawned",
			explicitOnly: true,
			arrange: func(f *systest.Fakes) {
				f.Exec.FailStart(func(*sys.Cmd) bool { return true }, spawnErr)
			},
			wantErrIs: spawnErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ex, fakes := newFakeExecutor(&mockLdapManager{})
			tt.arrange(fakes)

			value, set, err := ex.ReadPostconf(t.Context(), "some_param", tt.explicitOnly)

			require.Error(t, err)
			assert.Empty(t, value)
			assert.False(t, set)

			if tt.wantErrIs != nil {
				assert.True(t, errors.Is(err, tt.wantErrIs), "got %v", err)
			}

			var exitErr *sys.ExitError
			if tt.wantErrIs == nil {
				assert.ErrorAs(t, err, &exitErr)
			}
		})
	}
}

func TestExecutor_ReadPostconf_InvalidKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "-e", "a b", "key=value", "k;rm"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			ex, fakes := newFakeExecutor(&mockLdapManager{})

			_, set, err := ex.ReadPostconf(t.Context(), key, false)

			require.ErrorIs(t, err, ErrInvalidPostconfKey)
			assert.False(t, set)
			assert.Empty(t, fakes.Exec.Calls(), "an invalid key must never reach postconf")
		})
	}
}
