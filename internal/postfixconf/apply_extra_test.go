// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package postfixconf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skipIfRoot(t *testing.T) {
	t.Helper()

	if os.Getuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()

	o := DefaultOptions(1234)
	if o.StagingDir != StagingDir || o.ConfDir != ConfDir || o.Postconf != PostconfPath ||
		o.LockPath != LockPath || o.StagingUID != 1234 || o.InstallUID != 0 || o.InstallGID != 0 || o.Run != nil {
		t.Errorf("DefaultOptions = %+v", o)
	}
}

// The real postconf runner: absolute path, clean environment, working
// directory /, combined output; a failing run carries the path and arguments.
func TestRealPostconfRunner(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	script := filepath.Join(t.TempDir(), "postconf")
	body := "#!/bin/sh\necho \"args=$*\"\necho \"pwd=$(pwd)\"\necho \"env=$(env | sort | tr '\\n' ' ')\"\n[ \"$1\" = -X ] && exit 3\nexit 0\n"

	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}

	e.opts.Run = nil
	e.opts.Postconf = script
	e.stage(t, SetFile, "relayhost=mx\n")

	if err := e.opts.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	out, err := e.opts.run(context.Background(), []string{"-e", "a=b"})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"args=-e a=b", "pwd=/", "LC_ALL=C", "PATH=/usr/bin:/bin"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}

	if strings.Contains(string(out), "MAIL_CONFIG") || strings.Contains(string(out), "HOME=") {
		t.Errorf("environment must be clean: %q", out)
	}

	out, err = e.opts.run(context.Background(), []string{"-X", "relayhost"})
	if err == nil || !strings.Contains(err.Error(), script) || !strings.Contains(string(out), "args=-X relayhost") {
		t.Errorf("failing run = %q, %v; want an error naming the binary and the output", out, err)
	}

	e.stage(t, UnsetFile, "relayhost\n")

	if err := e.opts.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "postconf -X failed") {
		t.Errorf("Apply with a failing postconf -X = %v", err)
	}
}

func TestLockErrors(t *testing.T) {
	t.Parallel()

	for name, prepare := range map[string]func(t *testing.T, lock string){
		"parent is a file": func(t *testing.T, lock string) {
			t.Helper()

			if err := os.MkdirAll(filepath.Dir(filepath.Dir(lock)), 0o700); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Dir(lock), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"lock path is a directory": func(t *testing.T, lock string) {
			t.Helper()

			if err := os.MkdirAll(lock, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"lock path is a symlink": func(t *testing.T, lock string) {
			t.Helper()

			if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
				t.Fatal(err)
			}

			if err := os.Symlink("/etc/passwd", lock); err != nil {
				t.Fatal(err)
			}
		},
		"grandparent is a file": func(t *testing.T, lock string) {
			t.Helper()

			if err := os.WriteFile(filepath.Dir(filepath.Dir(lock)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			e.opts.LockPath = filepath.Join(t.TempDir(), "run", "pf", "lock")
			prepare(t, e.opts.LockPath)

			if err := e.opts.Apply(context.Background()); err == nil {
				t.Fatal("Apply must fail when the lock cannot be taken")
			}

			if len(e.calls) != 0 {
				t.Errorf("nothing may run without the lock: %q", e.calls)
			}
		})
	}
}

func TestOpenConfDirErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		e.opts.ConfDir = filepath.Join(t.TempDir(), "nope")
		e.stage(t, SetFile, "relayhost=x\n")

		if err := e.opts.Apply(context.Background()); err == nil {
			t.Fatal("a missing conf dir must fail")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		link := filepath.Join(t.TempDir(), "conf")

		if err := os.Symlink(e.conf, link); err != nil {
			t.Fatal(err)
		}

		e.opts.ConfDir = link
		e.stage(t, SetFile, "relayhost=x\n")

		if err := e.opts.Apply(context.Background()); err == nil {
			t.Fatal("a symlinked conf dir must be refused")
		}
	})

	t.Run("master.cf is a directory", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		if err := os.Mkdir(filepath.Join(e.conf, MasterCf), 0o755); err != nil { //nolint:gosec // test
			t.Fatal(err)
		}

		e.stage(t, SetFile, "relayhost=x\n")

		if err := e.opts.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("err = %v, want a regular-file refusal", err)
		}
	})

	t.Run("master.cf owned by someone else", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		if err := os.WriteFile(filepath.Join(e.conf, MasterCf), nil, 0o644); err != nil { //nolint:gosec // test
			t.Fatal(err)
		}

		e.opts.InstallUID = os.Getuid() + 1 // the dir check trips first
		e.stage(t, SetFile, "relayhost=x\n")

		if err := e.opts.Apply(context.Background()); err == nil {
			t.Fatal("a foreign-owned conf dir must be refused")
		}
	})
}

func TestExistingMainCfIsKept(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	main := filepath.Join(e.conf, MainCf)

	if err := os.WriteFile(main, []byte("keep\n"), 0o644); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}

	e.stage(t, SetFile, "relayhost=x\n")

	if err := e.opts.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(main); string(got) != "keep\n" {
		t.Errorf("main.cf = %q, want it untouched", got)
	}
}

func TestInstallFailsInReadOnlyConfDir(t *testing.T) {
	skipIfRoot(t)

	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.conf, MainCf), nil, 0o644); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}

	if err := os.Chmod(e.conf, 0o500); err != nil { //nolint:gosec // read-only dir
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(e.conf, 0o700) }) //nolint:gosec // let TempDir clean up

	e.stage(t, MasterCf, goodMaster)

	if err := e.opts.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("err = %v, want a create failure", err)
	}
}

func TestEnsureMainCfFailsInReadOnlyConfDir(t *testing.T) {
	skipIfRoot(t)

	e := newEnv(t)
	if err := os.Chmod(e.conf, 0o500); err != nil { //nolint:gosec // read-only dir
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(e.conf, 0o700) }) //nolint:gosec // let TempDir clean up

	e.stage(t, SetFile, "relayhost=x\n")

	if err := e.opts.Apply(context.Background()); err == nil {
		t.Fatal("creating main.cf in a read-only dir must fail")
	}
}

// finishFile reports the step that failed and closes the file.
func TestFinishFileError(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}

	_ = f.Close()

	if err := finishFile(f, os.Getuid(), os.Getgid()); err == nil || !strings.Contains(err.Error(), "finish") {
		t.Errorf("finishFile on a closed file = %v", err)
	}
}

func TestInstallLeavesNoTempOnWriteFailure(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.opts.InstallUID = os.Getuid()

	// chown to a foreign uid fails for non-root: the temp must be cleaned up.
	if os.Getuid() == 0 {
		t.Skip("root can chown")
	}

	conf, err := e.opts.openConfDir()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conf.Close() }()

	e.opts.InstallUID = 0

	if err := e.opts.install(conf, MasterCf, []byte(goodMaster)); err == nil {
		t.Fatal("chown to root must fail for a non-root user")
	}

	ents, _ := os.ReadDir(e.conf)
	if len(ents) != 0 {
		t.Errorf("temp file left behind: %v", ents)
	}
}

func TestReadEntryRejectsBadStagedFiles(t *testing.T) {
	t.Parallel()

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		if err := os.Symlink("/etc/passwd", filepath.Join(e.staging, SetFile)); err != nil {
			t.Fatal(err)
		}

		if err := e.opts.Apply(context.Background()); err == nil {
			t.Fatal("a symlink in staging must be refused")
		}
	})

	t.Run("unknown name", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		e.stage(t, "evil.cf", "x")

		if err := e.opts.Apply(context.Background()); err == nil {
			t.Fatal("an unknown staging entry must be refused")
		}
	})
}
