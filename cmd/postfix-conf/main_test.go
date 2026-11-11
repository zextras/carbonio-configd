// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zextras/carbonio-configd/internal/postfixconf"
)

type harness struct {
	staging, conf string
	calls         [][]string
	gotUID        uint32
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()
	h := &harness{staging: filepath.Join(root, "staging"), conf: filepath.Join(root, "conf"), gotUID: 99999}

	for _, d := range []string{h.staging, h.conf} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	return h
}

func (h *harness) options(root string) func(uint32) postfixconf.Options {
	return func(uid uint32) postfixconf.Options {
		h.gotUID = uid

		return postfixconf.Options{
			StagingDir: h.staging,
			ConfDir:    h.conf,
			Postconf:   "/fake/postconf",
			LockPath:   filepath.Join(root, "run", "lock"),
			StagingUID: uint32(os.Getuid()), // the test user owns the staged files
			InstallUID: os.Getuid(),
			InstallGID: os.Getgid(),
			Run: func(_ context.Context, _ string, args []string) ([]byte, error) {
				h.calls = append(h.calls, slices.Clone(args))

				return nil, nil
			},
		}
	}
}

func lookupAs(uid string) func(string) (*user.User, error) {
	return func(name string) (*user.User, error) {
		if name != "zextras" {
			return nil, errors.New("unexpected user " + name)
		}

		return &user.User{Uid: uid, Username: name}, nil
	}
}

func lookupNone(string) (*user.User, error) { return nil, user.UnknownUserError("zextras") }

func TestRun(t *testing.T) {
	t.Parallel()

	self := strconv.Itoa(os.Getuid())

	for _, tc := range []struct {
		name      string
		args      []string
		euid      int
		lookup    func(string) (*user.User, error)
		stage     map[string]string
		wantExit  int
		wantCalls [][]string
		wantErr   string
	}{
		{
			name: "set", args: []string{"set", "relayhost=mx", "smtpd_banner=hi"}, lookup: lookupNone,
			wantCalls: [][]string{{"-e", "relayhost=mx", "smtpd_banner=hi"}},
		},
		{
			name: "unset", args: []string{"unset", "relayhost"}, lookup: lookupNone,
			wantCalls: [][]string{{"-X", "relayhost"}},
		},
		{
			name: "apply", args: []string{"apply"}, lookup: lookupAs(self),
			stage:     map[string]string{postfixconf.SetFile: "relayhost=mx\n"},
			wantCalls: [][]string{{"-e", "relayhost=mx"}},
		},
		{
			name: "apply without the zextras user", args: []string{"apply"}, lookup: lookupNone,
			stage:     map[string]string{postfixconf.SetFile: "relayhost=mx\n"},
			wantCalls: [][]string{{"-e", "relayhost=mx"}},
		},
		{
			name: "apply with a bad uid", args: []string{"apply"}, lookup: lookupAs("not-a-number"),
			wantExit: postfixconf.ExitFailure, wantErr: "bad uid",
		},
		{
			name: "validation error", args: []string{"set", "daemon_directory=/tmp"}, lookup: lookupNone,
			wantExit: postfixconf.ExitValidation, wantErr: "not allowed",
		},
		{
			name: "staged validation error", args: []string{"apply"}, lookup: lookupAs(self),
			stage:    map[string]string{postfixconf.SetFile: "debugger_command=/tmp/x\n"},
			wantExit: postfixconf.ExitValidation, wantErr: "not allowed",
		},
		{
			name: "unknown subcommand", args: []string{"frobnicate"}, lookup: lookupNone,
			wantExit: postfixconf.ExitValidation, wantErr: "usage",
		},
		{
			name: "no subcommand", lookup: lookupNone,
			wantExit: postfixconf.ExitValidation, wantErr: "usage",
		},
		{
			name: "not root", args: []string{"set", "relayhost=mx"}, euid: 1000, lookup: lookupNone,
			wantExit: postfixconf.ExitFailure, wantErr: "must run as root",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			for name, content := range tc.stage {
				if err := os.WriteFile(filepath.Join(h.staging, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var stderr bytes.Buffer

			got := run(context.Background(), tc.args, tc.euid, &stderr, tc.lookup, h.options(filepath.Dir(h.staging)))
			if got != tc.wantExit {
				t.Fatalf("exit = %d, want %d (stderr %q)", got, tc.wantExit, stderr.String())
			}

			if !slices.EqualFunc(h.calls, tc.wantCalls, slices.Equal) {
				t.Errorf("postconf calls = %q, want %q", h.calls, tc.wantCalls)
			}

			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantErr)
			}

			if tc.wantExit == postfixconf.ExitOK && stderr.Len() != 0 {
				t.Errorf("success must be silent, stderr = %q", stderr.String())
			}
		})
	}
}

// Only apply looks the staging user up; set and unset work without it.
func TestRunStagingUID(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	opts := h.options(filepath.Dir(h.staging))

	var stderr bytes.Buffer

	if got := run(context.Background(), []string{"set", "relayhost=x"}, 0, &stderr, lookupAs("4242"), opts); got != 0 {
		t.Fatalf("set exit = %d (%s)", got, stderr.String())
	}

	if h.gotUID != 0 {
		t.Errorf("set must not use the zextras uid, got %d", h.gotUID)
	}

	if got := run(context.Background(), []string{"apply"}, 0, &stderr, lookupAs("4242"), opts); got != 0 {
		t.Fatalf("apply exit = %d (%s)", got, stderr.String())
	}

	if h.gotUID != 4242 {
		t.Errorf("apply must use the zextras uid, got %d", h.gotUID)
	}
}
