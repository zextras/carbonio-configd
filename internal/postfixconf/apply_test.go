// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfixconf

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

type env struct {
	opts    Options
	calls   [][]string
	runErr  error
	runOut  string
	staging string
	conf    string
}

func newEnv(t *testing.T) *env {
	t.Helper()

	root := t.TempDir()
	e := &env{staging: filepath.Join(root, "staging"), conf: filepath.Join(root, "conf")}

	for _, d := range []string{e.staging, e.conf} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	e.opts = Options{
		StagingDir: e.staging,
		ConfDir:    e.conf,
		Postconf:   "/fake/postconf",
		LockPath:   filepath.Join(root, "lock"),
		StagingUID: uint32(os.Getuid()),
		InstallUID: os.Getuid(),
		InstallGID: os.Getgid(),
		Run: func(_ context.Context, _ string, args []string) ([]byte, error) {
			e.calls = append(e.calls, slices.Clone(args))

			return []byte(e.runOut), e.runErr
		},
	}

	return e
}

func (e *env) stage(t *testing.T, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(e.staging, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) staged(t *testing.T) []string {
	t.Helper()

	ents, err := os.ReadDir(e.staging)
	if err != nil {
		t.Fatal(err)
	}

	var out []string
	for _, en := range ents {
		out = append(out, en.Name())
	}

	return out
}

func TestApplyInstallsFilesThenUnsetThenSet(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.stage(t, MasterCf, goodMaster)
	e.stage(t, TagAsForeignRe, "/foreign/ x\n")
	e.stage(t, UnsetFile, "smtpd_banner\nrelayhost\n")
	e.stage(t, SetFile, "myhostname=h\nrelayhost=\n")

	if err := e.opts.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(e.conf, MasterCf))
	if string(got) != goodMaster {
		t.Errorf("master.cf not installed: %q", got)
	}

	if fi, err := os.Stat(filepath.Join(e.conf, MasterCf)); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("master.cf mode = %v, %v; want 0644", fi, err)
	}

	if _, err := os.Stat(filepath.Join(e.conf, MainCf)); err != nil {
		t.Errorf("main.cf must be created when missing: %v", err)
	}

	want := [][]string{{"-X", "smtpd_banner", "relayhost"}, {"-e", "myhostname=h", "relayhost="}}
	if !slices.EqualFunc(e.calls, want, slices.Equal) {
		t.Errorf("postconf calls = %q, want %q", e.calls, want)
	}

	if left := e.staged(t); len(left) != 0 {
		t.Errorf("staging not consumed: %q", left)
	}

	if ents, _ := os.ReadDir(e.conf); len(ents) != 3 { // main.cf, master.cf, tag_as_foreign.re: no temp left
		t.Errorf("unexpected files in conf dir: %v", ents)
	}
}

func TestApplyEmptyOrMissingStagingIsNoop(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	if err := e.opts.Apply(context.Background()); err != nil || len(e.calls) != 0 {
		t.Fatalf("empty staging: err %v calls %v", err, e.calls)
	}

	e.opts.StagingDir = filepath.Join(e.staging, "missing")
	if err := e.opts.Apply(context.Background()); err != nil {
		t.Fatalf("missing staging dir: %v", err)
	}
}

func TestApplyRejectsAndAppliesNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, e *env)
	}{
		{"unknown file", func(t *testing.T, e *env) {
			e.stage(t, SetFile, "relayhost=b\n")
			e.stage(t, "main.cf", "daemon_directory=/tmp\n")
		}},
		{"unknown temp leftover", func(t *testing.T, e *env) {
			e.stage(t, SetFile, "relayhost=b\n")
			e.stage(t, ".tmp123", "x")
		}},
		{"denied key", func(t *testing.T, e *env) {
			e.stage(t, MasterCf, goodMaster)
			e.stage(t, SetFile, "daemon_directory=/tmp\n")
		}},
		{"bad master.cf", func(t *testing.T, e *env) {
			e.stage(t, SetFile, "relayhost=b\n")
			e.stage(t, MasterCf, "smtp inet n - n - - /tmp/x\n")
		}},
		{"set line without equals", func(t *testing.T, e *env) { e.stage(t, SetFile, "novalue\n") }},
		{"denied unset", func(t *testing.T, e *env) { e.stage(t, UnsetFile, "queue_directory\n") }},
		{"symlink entry", func(t *testing.T, e *env) {
			target := filepath.Join(filepath.Dir(e.staging), "secret")
			if err := os.WriteFile(target, []byte("a=b\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.Symlink(target, filepath.Join(e.staging, SetFile)); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory entry", func(t *testing.T, e *env) {
			if err := os.Mkdir(filepath.Join(e.staging, SetFile), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"fifo entry", func(t *testing.T, e *env) {
			if err := syscall.Mkfifo(filepath.Join(e.staging, SetFile), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversize", func(t *testing.T, e *env) { e.stage(t, TagAsForeignRe, strings.Repeat("x", MaxFileSize+1)) }},
		{"foreign owner", func(t *testing.T, e *env) {
			if os.Getuid() == 0 {
				t.Skip("root-owned staging is accepted by design")
			}

			e.stage(t, SetFile, "relayhost=b\n")
			e.opts.StagingUID = uint32(os.Getuid()) + 1 // the directory check trips first too
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			tc.setup(t, e)

			err := e.opts.Apply(context.Background())
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}

			if len(e.calls) != 0 {
				t.Errorf("postconf ran despite rejection: %q", e.calls)
			}

			if ents, _ := os.ReadDir(e.conf); len(ents) != 0 {
				t.Errorf("conf dir touched despite rejection: %v", ents)
			}
		})
	}
}

func TestApplyWorldWritableStagingDirRejected(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.stage(t, SetFile, "relayhost=b\n")

	if err := os.Chmod(e.staging, 0o777); err != nil {
		t.Fatal(err)
	}

	if err := e.opts.Apply(context.Background()); !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestApplyPostconfFailureKeepsStaging(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.runErr = errors.New("exit status 1")
	e.runOut = "postconf: fatal: boom\n"
	e.stage(t, SetFile, "relayhost=b\n")

	err := e.opts.Apply(context.Background())
	if err == nil || errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want a non-validation postconf failure", err)
	}
}

func TestRunCLI(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		args      []string
		euid      int
		wantExit  int
		wantCalls [][]string
	}{
		{"set", []string{"set", "myhostname=h", "relayhost=y=z"}, 0, ExitOK, [][]string{{"-e", "myhostname=h", "relayhost=y=z"}}},
		{"unset", []string{"unset", "smtpd_banner", "relayhost"}, 0, ExitOK, [][]string{{"-X", "smtpd_banner", "relayhost"}}},
		{"apply empty", []string{"apply"}, 0, ExitOK, nil},
		{"not root", []string{"set", "relayhost=b"}, 1000, ExitFailure, nil},
		{"no args", nil, 0, ExitValidation, nil},
		{"unknown verb", []string{"-c", "/tmp"}, 0, ExitValidation, nil},
		{"apply extra args", []string{"apply", "x"}, 0, ExitValidation, nil},
		{"set denied", []string{"set", "myhostname=h", "daemon_directory=/x"}, 0, ExitValidation, nil},
		{"set no equals", []string{"set", "novalue"}, 0, ExitValidation, nil},
		{"set empty", []string{"set"}, 0, ExitValidation, nil},
		{"unset option-like", []string{"unset", "-c"}, 0, ExitValidation, nil},
		{"set LF", []string{"set", "relayhost=b\nc=d"}, 0, ExitValidation, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)

			var stderr bytes.Buffer

			if got := RunCLI(context.Background(), tc.args, tc.euid, &e.opts, &stderr); got != tc.wantExit {
				t.Fatalf("exit = %d, want %d (stderr %q)", got, tc.wantExit, stderr.String())
			}

			if !slices.EqualFunc(e.calls, tc.wantCalls, slices.Equal) {
				t.Errorf("postconf calls = %q, want %q", e.calls, tc.wantCalls)
			}

			if tc.wantExit != ExitOK && stderr.Len() == 0 {
				t.Error("a failure must explain itself on stderr")
			}
		})
	}
}

func TestApplyRefusesWritableConfDir(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.stage(t, SetFile, "relayhost=x\n")

	if err := os.Chmod(e.conf, 0o770); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}

	err := e.opts.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v, want a refusal for a group-writable conf dir", err)
	}

	if len(e.calls) != 0 {
		t.Errorf("postconf must not run, got %q", e.calls)
	}

	if left := e.staged(t); len(left) != 1 {
		t.Errorf("staging must be kept on refusal, got %q", left)
	}
}

func TestApplyRefusesBadConfDirOwner(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.opts.InstallUID++
	e.stage(t, SetFile, "relayhost=x\n")

	if err := e.opts.Apply(context.Background()); err == nil {
		t.Fatal("a conf dir owned by someone else must be refused")
	}
}

func TestApplyRefusesWritableMasterCf(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.stage(t, SetFile, "relayhost=x\n")

	if err := os.WriteFile(filepath.Join(e.conf, MasterCf), []byte("x"), 0o666); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Join(e.conf, MasterCf), 0o666); err != nil { //nolint:gosec // umask
		t.Fatal(err)
	}

	if err := e.opts.Apply(context.Background()); err == nil {
		t.Fatal("a group/world-writable master.cf must be refused")
	}
}

func TestApplyRefusesSymlinkedMainCf(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.stage(t, SetFile, "relayhost=x\n")

	if err := os.Symlink("/etc/passwd", filepath.Join(e.conf, MainCf)); err != nil {
		t.Fatal(err)
	}

	if err := e.opts.Apply(context.Background()); err == nil {
		t.Fatal("a symlinked main.cf must be refused")
	}
}

func TestLockDirIsCreated(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.opts.LockPath = filepath.Join(t.TempDir(), "run", "postfix-conf", "lock")

	if err := e.opts.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	fi, err := os.Stat(filepath.Dir(e.opts.LockPath))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("lock dir = %v, %v; want 0700", fi, err)
	}
}

func TestStagedGarbageIsNotEchoed(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.stage(t, SetFile, "root:x:0:0:SECRET-LINE\n")

	err := e.opts.Apply(context.Background())
	if err == nil || strings.Contains(err.Error(), "SECRET-LINE") {
		t.Fatalf("err = %v, want a validation error without staged content", err)
	}
}
